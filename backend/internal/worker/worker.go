// Package worker processes the download queue: atomic claiming, timeout reset,
// retries and per-platform cooldowns. The platform work itself is injected via
// Process, so this package does not depend on the platforms package.
package worker

import (
	"database/sql"
	"errors"
	"meshdepot/internal/dbutil"
	"strconv"
	"strings"
	"sync"
	"time"

	"meshdepot/internal/health"
	"meshdepot/internal/notify"
	"meshdepot/internal/platforms"
	"meshdepot/internal/queuestate"
	"meshdepot/internal/safego"

	"meshdepot/internal/logx"
)

const tickInterval = 2 * time.Second

// MaxRetries is how often a job is tried before it counts as failed. Exported
// because the queue display names the attempt.
const MaxRetries = 3

const stuckTimeout = 15 * time.Minute

// defaultBlockThreshold is how many anti-bot hits in a row a platform may take
// before its queue is auto-paused. Overridable via queue_block_threshold.
const defaultBlockThreshold = 3

// defaultBlockHours is how long an automatic block lasts. Overridable via
// queue_block_hours.
const defaultBlockHours = 24

// errDownloadPanicked is reported when Process panicked. safego logs the stack;
// the job is retried like any other transient failure.
var errDownloadPanicked = errors.New("error.download_panic")

// Job describes a download task.
type Job struct {
	ID        int
	Platform  string
	SourceURL string
	UserID    int
	// UserPublicID addresses the owner's storage tree. Read together with the job:
	// the download ends in an open transaction, and with a single connection a
	// lookup at that point would deadlock.
	UserPublicID string
	RetryCount   int
}

// permanentErrors are error signatures that are not retried.
var permanentErrors = []string{
	"error.unsupported_url", "error.platform_credentials_required",
	"_no_token", "_auth_failed", "_restricted", "_requires_purchase", "_no_files",
	// A full account will not empty itself; retrying only repeats the refusal.
	"error.storage_quota_exceeded",
}

func isPermanent(message string) bool {
	for _, signature := range permanentErrors {
		if strings.Contains(message, signature) {
			return true
		}
	}
	return false
}

// softRateLimitErrors are temporary rate limits and captchas - "try again
// later" rather than a failure. Such jobs are never burned after MaxRetries;
// they stay pending and are retried, spaced out, until they succeed.
var softRateLimitErrors = []string{
	"error.makerworld_captcha",
	"error.cults3d_captcha",
}

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
	// Process runs the platform download and the saving, and returns the new
	// design_id. Injected by main.go.
	Process func(Job) (int, error)
	// CooldownFor returns the cooldown of a platform in seconds (from app_settings).
	CooldownFor func(platform string) int
	// Heartbeat records the loop's liveness for the health endpoint. Wired by
	// main.go; nil elsewhere, which the registry tolerates.
	Heartbeat *health.Registry

	mutex     sync.Mutex
	cooldowns map[string]time.Time // platform -> free from
	// blockStrikes counts consecutive anti-bot hits per platform, reset on the next
	// success or once a block is triggered. In memory on purpose: the block itself
	// has to survive a restart and lives in app_settings via queuestate.
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

// otherPlatformsRunner is the sentinel for the catch-all runner, so a job from
// an unexpected platform is never stranded.
const otherPlatformsRunner = "\x00other"

// runnerPlatforms lists the per-platform runners plus the catch-all.
func runnerPlatforms() []string {
	return append(append([]string{}, queuestate.Platforms...), otherPlatformsRunner)
}

// Start runs the processing until stop is closed. One runner goroutine per
// platform plus a catch-all, because a single loop let a slow download on one
// platform stall every other platform behind it (issue #8). A coordinator keeps
// the heartbeat fresh and resets hanging jobs while every runner is busy.
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

	// The queue is only idle once every runner has left after stop.
	safego.Go("download-worker.finisher", func() {
		group.Wait()
		close(downloadWorker.finished)
	})
}

// runPlatform claims and processes one job of its platform per tick.
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

func (downloadWorker *DownloadWorker) runOne(platform string) {
	job, ok := downloadWorker.claimNextFor(platform)
	if !ok {
		return
	}
	downloadWorker.process(job)
}

// Wait blocks until the loop has left after stop, at most for the given
// duration - the download of a large model may have minutes to go, and the
// supervisor kills the process long before that. Reports whether it finished.
func (downloadWorker *DownloadWorker) Wait(limit time.Duration) bool {
	select {
	case <-downloadWorker.finished:
		return true
	case <-time.After(limit):
		return false
	}
}

// procTimeout limits a single download, typically a blocked headless browser or
// a dead Tor connection. A timeout counts as a failed attempt.
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

// process runs one claimed job to completion. The download runs under a timeout
// in its own goroutine, and the final queue status is written here rather than
// in Process, so a late-returning straggler cannot overwrite it.
func (downloadWorker *DownloadWorker) process(job Job) {
	// Marks the loop busy while the job runs, so a download taking minutes is not
	// misread as a dead loop. The registry counts overlapping runners.
	endBusy := downloadWorker.Heartbeat.Working(health.DownloadWorker)
	defer endBusy()
	downloadWorker.markCooldown(job.Platform)

	// Buffered, so a late-returning goroutine does not block forever; its result is
	// discarded after the timeout.
	done := make(chan procResult, 1)
	go func() {
		// A panic inside Process - unchecked type assertions on foreign platform JSON -
		// would otherwise take the process down. Reported as a normal job failure.
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
		final := job.RetryCount+1 >= MaxRetries
		suffix := "will be retried"
		if final {
			suffix = "marked as permanently failed"
		}
		logx.Errorf("[worker] Job #%d (%s): download TIMEOUT after %s - aborted so the "+
			"queue keeps running (hanging browser/network?). URL=%s attempt %d/%d, %s.",
			job.ID, job.Platform, procTimeout, job.SourceURL, job.RetryCount+1, MaxRetries, suffix)
		if final {
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='failed', error_msg='error.timeout', done_at=CURRENT_TIMESTAMP WHERE id=?", job.ID)
			downloadWorker.notifyFailed(job, "error.timeout")
		} else {
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='pending', started_at=NULL, retry_count=retry_count+1, error_msg='error.timeout' WHERE id=?", job.ID)
		}
		return
	}

	if failure != nil {
		downloadWorker.markCooldown(job.Platform) // block again from now on on failure
		// Temporary rate limit or captcha: leave the job pending with retry_count
		// untouched, so it is retried spaced out until it succeeds.
		if isSoftRateLimit(failure.Error()) {
			if downloadWorker.registerBlockStrike(job.Platform, failure.Error()) {
				logx.Warnf("[worker] Job #%d (%s): anti-bot/rate-limit block threshold reached - the whole %s "+
					"queue is auto-paused and resumes on its own. URL=%s detail: %v",
					job.ID, job.Platform, job.Platform, job.SourceURL, failure)
			} else {
				logx.Warnf("[worker] Job #%d (%s): temporarily blocked by anti-bot/rate-limit - stays in the queue, "+
					"will be retried after the cooldown. URL=%s detail: %v", job.ID, job.Platform, job.SourceURL, failure)
			}
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='pending', started_at=NULL, error_msg=? WHERE id=?", failure.Error(), job.ID)
			return
		}
		final := isPermanent(failure.Error()) || job.RetryCount+1 >= MaxRetries
		downloadWorker.logFailure(job, failure, final)
		if final {
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='failed', error_msg=?, done_at=CURRENT_TIMESTAMP WHERE id=?", failure.Error(), job.ID)
			downloadWorker.notifyFailed(job, failure.Error())
		} else {
			dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='pending', started_at=NULL, retry_count=retry_count+1, error_msg=? WHERE id=?", failure.Error(), job.ID)
		}
		return
	}
	dbutil.ExecLogged(downloadWorker.DB, "UPDATE download_queue SET status='done', design_id=?, done_at=CURRENT_TIMESTAMP WHERE id=?", designID, job.ID)
	downloadWorker.notifyDone(job, designID)
	// The platform is answering normally again, so a later isolated hit starts fresh.
	downloadWorker.clearBlockStrikes(job.Platform)
}

// notifyFailed and notifyDone tell the member how their download ended. Raised
// only where the job reaches its final state: a pending retry is not an outcome,
// and notifying per attempt would report one download as failed several times.
func (downloadWorker *DownloadWorker) notifyFailed(job Job, message string) {
	// The URL travels along so the digest can drop this again if the member
	// re-queued it and it succeeded before the summary went out.
	notify.UserWithReference(downloadWorker.DB, job.UserID, "download_failed",
		"Download failed",
		readableError(message)+"\n\n"+job.SourceURL, job.SourceURL, nil)
}

func (downloadWorker *DownloadWorker) notifyDone(job Job, designID int) {
	name := job.SourceURL
	var stored string
	if downloadWorker.DB.QueryRow("SELECT name FROM designs WHERE id = ?", designID).Scan(&stored) == nil && stored != "" {
		name = stored
	}
	designReference := designID
	notify.User(downloadWorker.DB, job.UserID, "download_done",
		"Download finished", name, &designReference)
}

// readableError turns <key>:<sentence> into the sentence. Notification bodies
// are stored as written and never run through the translation table, so a bare
// key would reach the member verbatim.
func readableError(message string) string {
	if _, sentence, found := strings.Cut(message, ":"); found && strings.TrimSpace(sentence) != "" {
		return strings.TrimSpace(sentence)
	}
	return message
}

// registerBlockStrike records one consecutive anti-bot hit. At the configured
// threshold it pauses the whole platform via queuestate and resets the counter.
// Reports whether this call is the one that triggered the pause.
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

func (downloadWorker *DownloadWorker) clearBlockStrikes(platform string) {
	downloadWorker.mutex.Lock()
	delete(downloadWorker.blockStrikes, platform)
	downloadWorker.mutex.Unlock()
}

// settingInt reads a positive integer app setting, falling back when the row is
// missing, blank or not a positive number.
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

// logFailure distinguishes the failure phase (cf. platforms.DownloadError): no
// metadata found, versus a model found whose files would not download.
func (downloadWorker *DownloadWorker) logFailure(job Job, failure error, final bool) {
	suffix := "will be retried automatically"
	if final {
		suffix = "permanently failed"
	}
	attempt := job.RetryCount + 1
	var downloadError *platforms.DownloadError
	switch {
	case errors.As(failure, &downloadError) && downloadError.Stage == platforms.StageMetadata:
		logx.Errorf("[worker] Job #%d (%s): model metadata could NOT be loaded "+
			"- no info (name etc.) was found at all. URL=%s attempt %d/%d, %s. Detail: %v",
			job.ID, job.Platform, job.SourceURL, attempt, MaxRetries, suffix, failure)
	case errors.As(failure, &downloadError) && downloadError.Stage == platforms.StageFiles:
		logx.Errorf("[worker] Job #%d (%s): model found, but the FILES could NOT "+
			"be downloaded. URL=%s attempt %d/%d, %s. Detail: %v",
			job.ID, job.Platform, job.SourceURL, attempt, MaxRetries, suffix, failure)
	default:
		logx.Errorf("[worker] Job #%d (%s): download failed. URL=%s attempt %d/%d, %s. Detail: %v",
			job.ID, job.Platform, job.SourceURL, attempt, MaxRetries, suffix, failure)
	}
}

// ResetStuck resets 'downloading' jobs that run too long, or marks them failed.
func (downloadWorker *DownloadWorker) ResetStuck() {
	cutoff := time.Now().Add(-stuckTimeout).UTC().Format("2006-01-02 15:04:05")
	dbutil.ExecLogged(downloadWorker.DB, `UPDATE download_queue SET status='pending', retry_count=retry_count+1
		WHERE status='downloading' AND (started_at IS NULL OR started_at < ?) AND retry_count < ?`, cutoff, MaxRetries)
	dbutil.ExecLogged(downloadWorker.DB, `UPDATE download_queue SET status='failed', error_msg='error.timeout', done_at=CURRENT_TIMESTAMP
		WHERE status='downloading' AND (started_at IS NULL OR started_at < ?) AND retry_count >= ?`, cutoff, MaxRetries)
}

// claimNext claims the next processable job atomically, respecting cooldown.
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
		// A paused or auto-blocked platform is skipped entirely; its jobs stay pending.
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

// claimNextFor is the per-platform variant used by the runners; the catch-all
// sentinel covers every platform without its own runner. Scoping the claim to
// one platform is what lets platforms download in parallel.
func (downloadWorker *DownloadWorker) claimNextFor(platform string) (Job, bool) {
	catchAll := platform == otherPlatformsRunner
	if !catchAll {
		// One check covers every job of a concrete platform.
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

func (downloadWorker *DownloadWorker) onCooldown(platform string) bool {
	downloadWorker.mutex.Lock()
	defer downloadWorker.mutex.Unlock()
	until, ok := downloadWorker.cooldowns[platform]
	return ok && time.Now().Before(until)
}

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
