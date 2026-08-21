// Package worker implements the download-queue processing as goroutines
// (replacement for the DownloadWorker shell loops). The engine handles atomic
// claiming, timeout reset, retries and per-platform cooldowns; the actual
// platform processing is injected via Process.
package worker

import (
	"database/sql"
	"errors"
	"log"
	"meshdepot/internal/dbutil"
	"strconv"
	"strings"
	"sync"
	"time"

	"meshdepot/internal/health"
	"meshdepot/internal/platforms"
	"meshdepot/internal/queuestate"
	"meshdepot/internal/safego"
)

// tickInterval is how often the loop looks for a claimable job.
const tickInterval = 2 * time.Second

// maxRetries is the maximum number of attempts per job.
const maxRetries = 3

// stuckTimeout is the duration after which a hanging 'downloading' job is reset.
const stuckTimeout = 15 * time.Minute

// defaultBlockThreshold is how many anti-bot/rate-limit hits in a row a platform
// may take before its whole queue is auto-paused. Overridable via the
// queue_block_threshold app setting.
const defaultBlockThreshold = 3

// defaultBlockHours is how long an automatic platform block lasts. Overridable
// via the queue_block_hours app setting.
const defaultBlockHours = 24

// errDownloadPanicked is the job failure reported when Process panicked. The
// panic itself (with stack) is logged by safego; the job is retried like any
// other transient failure.
var errDownloadPanicked = errors.New("error.download_panic")

// Job describes a download task.
type Job struct {
	ID        int
	Platform  string
	SourceURL string
	UserID    int
	// UserPublicID addresses the owner's storage tree. It is read together with
	// the job rather than looked up later: the download runs with an open
	// transaction at the end, and the database has a single connection, so a
	// lookup at that point would deadlock.
	UserPublicID string
	RetryCount   int
}

// permanentErrors are error signatures that are not retried.
var permanentErrors = []string{
	"error.unsupported_url", "error.platform_credentials_required",
	"_no_token", "_auth_failed", "_restricted", "_requires_purchase", "_no_files",
}

// isPermanent detects permanent errors (no retry).
func isPermanent(message string) bool {
	for _, signature := range permanentErrors {
		if strings.Contains(message, signature) {
			return true
		}
	}
	return false
}

// softRateLimitErrors are temporary rate limits/anti-bot captchas - not a real
// error but "try again later". Such jobs are NOT burned as failed after
// maxRetries; they stay pending and are retried, spaced out after the cooldown,
// until they succeed (download "bit by bit").
var softRateLimitErrors = []string{
	"error.makerworld_captcha",
	"error.cults3d_captcha",
}

// isSoftRateLimit detects temporary rate-limit/captcha errors.
func isSoftRateLimit(message string) bool {
	for _, signature := range softRateLimitErrors {
		if strings.Contains(message, signature) {
			return true
		}
	}
	return false
}

// DownloadWorker processes the download_queue.
type DownloadWorker struct {
	DB *sql.DB
	// Process runs the actual platform download + the saving and returns the new
	// design_id. Injected by main.go so the worker does not depend on the
	// platforms package.
	Process func(Job) (int, error)
	// CooldownFor returns the cooldown of a platform in seconds (from app_settings).
	CooldownFor func(platform string) int
	// Heartbeat records the liveness of the loop for the health endpoint. Wired
	// by main.go; nil elsewhere, which the registry tolerates.
	Heartbeat *health.Registry

	mutex     sync.Mutex
	cooldowns map[string]time.Time // platform -> free from
	// blockStrikes counts consecutive anti-bot/rate-limit hits per platform. It
	// is reset to zero on the platform's next success or once it triggers an
	// automatic block. In-memory on purpose: a restart empties the queue's
	// momentum anyway, and the block itself (which must survive a restart) lives
	// in app_settings via the queuestate package.
	blockStrikes map[string]int

	// finished is closed when the loop has left after stop - see Wait.
	finished chan struct{}
}

func NewDownloadWorker(db *sql.DB, process func(Job) (int, error), cooldownFor func(string) int) *DownloadWorker {
	return &DownloadWorker{
		DB:           db,
		Process:      process,
		CooldownFor:  cooldownFor,
		cooldowns:    map[string]time.Time{},
		blockStrikes: map[string]int{},
		finished:     make(chan struct{}),
	}
}

// otherPlatformsRunner is the sentinel for the catch-all runner: it processes
// any queued job whose platform is not one of the known per-platform runners, so
// a job from a future or unexpected platform is never stranded.
const otherPlatformsRunner = "\x00other"

// runnerPlatforms lists the per-platform runners plus the catch-all. Each known
// download platform gets its own runner; the catch-all covers everything else.
func runnerPlatforms() []string {
	return append(append([]string{}, queuestate.Platforms...), otherPlatformsRunner)
}

// Start runs the processing until stop is closed. Instead of a single loop that
// takes one job at a time - where a slow download on one platform stalled every
// other platform behind it (issue #8) - it runs one runner goroutine per
// platform (plus a catch-all), each claiming and processing only its own
// platform's jobs. A coordinator keeps the health heartbeat fresh and resets
// hanging jobs, so the health endpoint stays live even while every runner is
// busy. Each runner ticks every 2 s and honours its platform's cooldown and
// pause independently, so rate limits are still respected per platform.
func (downloadWorker *DownloadWorker) Start(stop <-chan struct{}) {
	downloadWorker.Heartbeat.Register(health.DownloadWorker, tickInterval)
	var group sync.WaitGroup

	group.Add(1)
	safego.Go("download-worker.coordinator", func() {
		defer group.Done()
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				downloadWorker.Heartbeat.Beat(health.DownloadWorker)
				safego.Run("download-worker.ResetStuck", downloadWorker.ResetStuck)
			}
		}
	})

	for _, platform := range runnerPlatforms() {
		platform := platform
		group.Add(1)
		safego.Go("download-worker.runner", func() {
			defer group.Done()
			downloadWorker.runPlatform(stop, platform)
		})
	}

	// The queue is only truly idle once every runner has left after stop, so the
	// finished signal (see Wait) is closed only then.
	safego.Go("download-worker.finisher", func() {
		group.Wait()
		close(downloadWorker.finished)
	})
}

// runPlatform is one platform's runner loop: every tick it claims and processes
// one job of that platform, until stop is closed.
func (downloadWorker *DownloadWorker) runPlatform(stop <-chan struct{}, platform string) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Per tick, so a panic costs one job instead of the whole runner.
			safego.Run("download-worker.runOne", func() { downloadWorker.runOne(platform) })
		}
	}
}

// runOne claims and processes a single job for one platform (or the catch-all).
func (downloadWorker *DownloadWorker) runOne(platform string) {
	job, ok := downloadWorker.claimNextFor(platform)
	if !ok {
		return
	}
	downloadWorker.process(job)
}

// Wait blocks until the loop has left after stop, at most for the given
// duration. RunOnce is synchronous, so a returned loop means no job is halfway
// through its file writes and DB rows - exactly the state A1 describes. Waiting
// forever is not an option either: the download of a large model may still have
// minutes to go, and the supervisor kills the process long before that. Reports
// whether the loop actually finished.
func (downloadWorker *DownloadWorker) Wait(limit time.Duration) bool {
	select {
	case <-downloadWorker.finished:
		return true
	case <-time.After(limit):
		return false
	}
}

// procTimeout limits the duration of a single download. If a job hangs longer
// (typically a blocked headless browser or a dead network/Tor connection) it is
// aborted so the single worker loop - and thus the rest of the queue - does NOT
// freeze with it. A timeout counts as a failed attempt.
const procTimeout = 10 * time.Minute

// procResult bundles the result of a Process call for the timeout select.
type procResult struct {
	designID int
	failure  error
}

// RunOnce resets hanging jobs and processes one claimable job.
func (downloadWorker *DownloadWorker) RunOnce() {
	downloadWorker.ResetStuck()
	job, ok := downloadWorker.claimNext()
	if !ok {
		return
	}
	downloadWorker.process(job)
}

// process runs one claimed job to completion: the download runs under a timeout
// in its own goroutine (a hang must not freeze the runner), and the job's final
// queue status is written here - never in Process - so a late-returning
// straggler goroutine can no longer overwrite it. Shared by RunOnce (single
// shot, used by the tests) and the per-platform runners.
func (downloadWorker *DownloadWorker) process(job Job) {
	// Working marks the loop busy for the health endpoint while the job runs, so a
	// download that takes minutes is not misread as a dead loop. The registry
	// counts overlapping runners (busyDepth), so the worker stays "busy" until the
	// last runner finishes.
	endBusy := downloadWorker.Heartbeat.Working(health.DownloadWorker)
	defer endBusy()
	downloadWorker.markCooldown(job.Platform)

	// Run Process in its own goroutine with a timeout: a hanging download must not
	// block the worker loop (a single ticker) - otherwise the whole queue stalls
	// behind that one job. The channel is buffered so a late-returning goroutine
	// does not block forever; its result is simply discarded after the timeout. All
	// status updates of the download_queue happen exclusively here (not in
	// Process), so the straggler goroutine can no longer overwrite the job status.
	done := make(chan procResult, 1)
	go func() {
		// A panic inside Process (unchecked type assertions on foreign platform
		// JSON) would otherwise take the whole process down. Report it as a normal
		// job failure so the queue keeps running and the job can be retried.
		result := procResult{failure: errDownloadPanicked}
		safego.Run("download-worker.Process", func() {
			designID, failure := downloadWorker.Process(job)
			result = procResult{designID: designID, failure: failure}
		})
		done <- result
	}()

	var designID int
	var failure error
	select {
	case result := <-done:
		designID, failure = result.designID, result.failure
	case <-time.After(procTimeout):
		downloadWorker.markCooldown(job.Platform) // block the platform from now on (hang = overload/block)
		final := job.RetryCount+1 >= maxRetries
		suffix := "will be retried"
		if final {
			suffix = "marked as permanently failed"
		}
		log.Printf("[worker] Job #%d (%s): download TIMEOUT after %s - aborted so the "+
			"queue keeps running (hanging browser/network?). URL=%s attempt %d/%d, %s.",
			job.ID, job.Platform, procTimeout, job.SourceURL, job.RetryCount+1, maxRetries, suffix)
		if final {
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='failed', error_msg='error.timeout', done_at=CURRENT_TIMESTAMP WHERE id=?", job.ID)
		} else {
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='pending', started_at=NULL, retry_count=retry_count+1, error_msg='error.timeout' WHERE id=?", job.ID)
		}
		return
	}

	if failure != nil {
		downloadWorker.markCooldown(job.Platform) // block again from now on on failure
		// Temporary rate limit/captcha: do NOT burn the job - leave it pending,
		// retry_count untouched; it is retried, spaced out after the cooldown, until
		// it succeeds (download "bit by bit").
		if isSoftRateLimit(failure.Error()) {
			if downloadWorker.registerBlockStrike(job.Platform, failure.Error()) {
				log.Printf("[worker] Job #%d (%s): anti-bot/rate-limit block threshold reached - the whole %s "+
					"queue is auto-paused and resumes on its own. URL=%s detail: %v",
					job.ID, job.Platform, job.Platform, job.SourceURL, failure)
			} else {
				log.Printf("[worker] Job #%d (%s): temporarily blocked by anti-bot/rate-limit - stays in the queue, "+
					"will be retried after the cooldown. URL=%s detail: %v", job.ID, job.Platform, job.SourceURL, failure)
			}
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='pending', started_at=NULL, error_msg=? WHERE id=?", failure.Error(), job.ID)
			return
		}
		final := isPermanent(failure.Error()) || job.RetryCount+1 >= maxRetries
		downloadWorker.logFailure(job, failure, final)
		if final {
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='failed', error_msg=?, done_at=CURRENT_TIMESTAMP WHERE id=?", failure.Error(), job.ID)
		} else {
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='pending', started_at=NULL, retry_count=retry_count+1, error_msg=? WHERE id=?", failure.Error(), job.ID)
		}
		return
	}
	dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='done', design_id=?, done_at=CURRENT_TIMESTAMP WHERE id=?", designID, job.ID)
	// A success means the platform is answering normally again: forget any
	// accumulated block strikes so a later isolated hit starts counting fresh.
	downloadWorker.clearBlockStrikes(job.Platform)
}

// registerBlockStrike records one consecutive anti-bot/rate-limit hit for the
// platform. Once the configured threshold is reached it auto-pauses the whole
// platform (via queuestate) for the configured number of hours and resets the
// counter. Reports whether this call is the one that triggered the pause.
func (downloadWorker *DownloadWorker) registerBlockStrike(platform, reason string) bool {
	downloadWorker.mutex.Lock()
	downloadWorker.blockStrikes[platform]++
	strikes := downloadWorker.blockStrikes[platform]
	downloadWorker.mutex.Unlock()

	if strikes < downloadWorker.settingInt("queue_block_threshold", defaultBlockThreshold) {
		return false
	}

	hours := downloadWorker.settingInt("queue_block_hours", defaultBlockHours)
	now := time.Now()
	queuestate.Block(downloadWorker.DB, platform, now.Add(time.Duration(hours)*time.Hour), reason, now)

	downloadWorker.mutex.Lock()
	downloadWorker.blockStrikes[platform] = 0
	downloadWorker.mutex.Unlock()
	return true
}

// clearBlockStrikes forgets the platform's accumulated block strikes.
func (downloadWorker *DownloadWorker) clearBlockStrikes(platform string) {
	downloadWorker.mutex.Lock()
	delete(downloadWorker.blockStrikes, platform)
	downloadWorker.mutex.Unlock()
}

// settingInt reads a positive integer app setting, falling back to fallback when
// the row is missing, blank or not a positive number.
func (downloadWorker *DownloadWorker) settingInt(key string, fallback int) int {
	var value string
	if downloadWorker.DB.QueryRow("SELECT value FROM app_settings WHERE key=?", key).Scan(&value) != nil {
		return fallback
	}
	if number, failure := strconv.Atoi(strings.TrimSpace(value)); failure == nil && number > 0 {
		return number
	}
	return fallback
}

// logFailure writes a precise log line per failed attempt, distinguishing the
// failure phase (cf. platforms.DownloadError): "no metadata" (model info not
// found) vs. "files failed" (model found, but no download succeeded). For all
// other errors the generic fallback applies.
func (downloadWorker *DownloadWorker) logFailure(job Job, failure error, final bool) {
	suffix := "will be retried automatically"
	if final {
		suffix = "permanently failed"
	}
	attempt := job.RetryCount + 1
	var downloadError *platforms.DownloadError
	switch {
	case errors.As(failure, &downloadError) && downloadError.Stage == platforms.StageMetadata:
		log.Printf("[worker] Job #%d (%s): model metadata could NOT be loaded "+
			"- no info (name etc.) was found at all. URL=%s attempt %d/%d, %s. Detail: %v",
			job.ID, job.Platform, job.SourceURL, attempt, maxRetries, suffix, failure)
	case errors.As(failure, &downloadError) && downloadError.Stage == platforms.StageFiles:
		log.Printf("[worker] Job #%d (%s): model found, but the FILES could NOT "+
			"be downloaded. URL=%s attempt %d/%d, %s. Detail: %v",
			job.ID, job.Platform, job.SourceURL, attempt, maxRetries, suffix, failure)
	default:
		log.Printf("[worker] Job #%d (%s): download failed. URL=%s attempt %d/%d, %s. Detail: %v",
			job.ID, job.Platform, job.SourceURL, attempt, maxRetries, suffix, failure)
	}
}

// ResetStuck resets 'downloading' jobs that run too long, or marks them failed.
func (downloadWorker *DownloadWorker) ResetStuck() {
	cutoff := time.Now().Add(-stuckTimeout).UTC().Format("2006-01-02 15:04:05")
	dbutil.ExecLogged(downloadWorker.DB, `UPDATE download_queue SET status='pending', retry_count=retry_count+1
		WHERE status='downloading' AND (started_at IS NULL OR started_at < ?) AND retry_count < ?`, cutoff, maxRetries)
	dbutil.ExecLogged(downloadWorker.DB, `UPDATE download_queue SET status='failed', error_msg='error.timeout', done_at=CURRENT_TIMESTAMP
		WHERE status='downloading' AND (started_at IS NULL OR started_at < ?) AND retry_count >= ?`, cutoff, maxRetries)
}

// claimNext selects the next processable job (respecting cooldown) and claims it
// atomically (UPDATE ... WHERE status='pending').
func (downloadWorker *DownloadWorker) claimNext() (Job, bool) {
	rows, failure := downloadWorker.DB.Query(`SELECT dq.id, dq.platform, dq.source_url, dq.user_id, COALESCE(u.public_id, ''), dq.retry_count
		FROM download_queue dq JOIN users u ON u.id = dq.user_id
		WHERE dq.status='pending' ORDER BY dq.created_at ASC`)
	if failure != nil {
		return Job{}, false
	}
	var candidates []Job
	for rows.Next() {
		var job Job
		if rows.Scan(&job.ID, &job.Platform, &job.SourceURL, &job.UserID, &job.UserPublicID, &job.RetryCount) == nil {
			candidates = append(candidates, job)
		}
	}
	rows.Close()

	for _, job := range candidates {
		// A manually paused or auto-blocked platform is skipped entirely - its
		// jobs stay pending and are picked up again once the platform runs again.
		if queuestate.Suspended(downloadWorker.DB, job.Platform, time.Now()) {
			continue
		}
		if downloadWorker.onCooldown(job.Platform) {
			continue
		}
		updateResult, failure := downloadWorker.DB.Exec("UPDATE download_queue SET status='downloading', started_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending'", job.ID)
		if failure != nil {
			continue
		}
		if affected, _ := updateResult.RowsAffected(); affected > 0 {
			return job, true
		}
	}
	return Job{}, false
}

// claimNextFor is the per-platform variant used by the runners: it claims the
// oldest pending job of one platform (or, for the catch-all sentinel, of any
// platform without its own runner), honouring that platform's cooldown and
// pause. Scoping the claim to one platform is what lets platforms download in
// parallel without one blocking another.
func (downloadWorker *DownloadWorker) claimNextFor(platform string) (Job, bool) {
	catchAll := platform == otherPlatformsRunner
	if !catchAll {
		// A concrete platform: one suspension/cooldown check covers all its jobs,
		// so a paused platform costs nothing but the check.
		if queuestate.Suspended(downloadWorker.DB, platform, time.Now()) || downloadWorker.onCooldown(platform) {
			return Job{}, false
		}
	}

	query := `SELECT dq.id, dq.platform, dq.source_url, dq.user_id, COALESCE(u.public_id, ''), dq.retry_count
		FROM download_queue dq JOIN users u ON u.id = dq.user_id
		WHERE dq.status='pending' AND `
	var args []any
	if catchAll {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(queuestate.Platforms)), ",")
		query += "dq.platform NOT IN (" + placeholders + ")"
		for _, known := range queuestate.Platforms {
			args = append(args, known)
		}
	} else {
		query += "dq.platform = ?"
		args = append(args, platform)
	}
	query += " ORDER BY dq.created_at ASC"

	rows, failure := downloadWorker.DB.Query(query, args...)
	if failure != nil {
		return Job{}, false
	}
	var candidates []Job
	for rows.Next() {
		var job Job
		if rows.Scan(&job.ID, &job.Platform, &job.SourceURL, &job.UserID, &job.UserPublicID, &job.RetryCount) == nil {
			candidates = append(candidates, job)
		}
	}
	rows.Close()

	for _, job := range candidates {
		// The catch-all mixes platforms, so each candidate needs its own check.
		if catchAll && (queuestate.Suspended(downloadWorker.DB, job.Platform, time.Now()) || downloadWorker.onCooldown(job.Platform)) {
			continue
		}
		updateResult, failure := downloadWorker.DB.Exec("UPDATE download_queue SET status='downloading', started_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending'", job.ID)
		if failure != nil {
			continue
		}
		if affected, _ := updateResult.RowsAffected(); affected > 0 {
			return job, true
		}
	}
	return Job{}, false
}

// onCooldown checks whether the platform is currently blocked.
func (downloadWorker *DownloadWorker) onCooldown(platform string) bool {
	downloadWorker.mutex.Lock()
	defer downloadWorker.mutex.Unlock()
	until, ok := downloadWorker.cooldowns[platform]
	return ok && time.Now().Before(until)
}

// markCooldown resets the platform's cooldown (from now on).
func (downloadWorker *DownloadWorker) markCooldown(platform string) {
	seconds := 0
	if downloadWorker.CooldownFor != nil {
		seconds = downloadWorker.CooldownFor(platform)
	}
	if seconds <= 0 {
		return
	}
	downloadWorker.mutex.Lock()
	downloadWorker.cooldowns[platform] = time.Now().Add(time.Duration(seconds) * time.Second)
	downloadWorker.mutex.Unlock()
}
