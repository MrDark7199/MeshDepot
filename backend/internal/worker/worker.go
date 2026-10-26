// Package worker processes the job queue - imports of new designs and update
// checks of existing ones alike: atomic claiming, timeout reset, retries,
// per-platform cooldowns and the anti-bot pause. One worker for both, because
// both fetch from the same platforms and those rules only hold when every
// request passes through the same runner. The platform work itself is injected
// via Process, so this package does not depend on the platforms package.
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
	"meshdepot/internal/jobqueue"
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

// Job is one entry of the job queue.
type Job struct {
	ID int
	// Kind is jobqueue.KindImport (a new design from SourceURL) or
	// jobqueue.KindUpdate (a new version of DesignID).
	Kind      string
	DesignID  int
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

// QueueWorker processes the job queue, imports and updates alike.
type QueueWorker struct {
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

func NewQueueWorker(db *sql.DB, process func(Job) (int, error), cooldownFor func(string) int) *QueueWorker {
	return &QueueWorker{
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
func (queueWorker *QueueWorker) Start(stop <-chan struct{}) {
	queueWorker.Heartbeat.Register(health.QueueWorker, tickInterval)
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
				queueWorker.Heartbeat.Beat(health.QueueWorker)
				safego.Run("download-worker.ResetStuck", queueWorker.ResetStuck)
			}
		}
	})

	for _, platform := range runnerPlatforms() {
		platform := platform
		group.Add(1)
		safego.Go("download-worker.runner", func() {
			defer group.Done()
			queueWorker.runPlatform(stop, platform)
		})
	}

	// The queue is only idle once every runner has left after stop.
	safego.Go("download-worker.finisher", func() {
		group.Wait()
		close(queueWorker.finished)
	})
}

// runPlatform claims and processes one job of its platform per tick.
func (queueWorker *QueueWorker) runPlatform(stop <-chan struct{}, platform string) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Per tick, so a panic costs one job instead of the whole runner.
			safego.Run("download-worker.runOne", func() { queueWorker.runOne(platform) })
		}
	}
}

func (queueWorker *QueueWorker) runOne(platform string) {
	job, ok := queueWorker.claimNextFor(platform)
	if !ok {
		return
	}
	queueWorker.process(job)
}

// Wait blocks until the loop has left after stop, at most for the given
// duration - the download of a large model may have minutes to go, and the
// supervisor kills the process long before that. Reports whether it finished.
func (queueWorker *QueueWorker) Wait(limit time.Duration) bool {
	select {
	case <-queueWorker.finished:
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
func (queueWorker *QueueWorker) RunOnce() {
	queueWorker.ResetStuck()
	job, ok := queueWorker.claimNext()
	if !ok {
		return
	}
	queueWorker.process(job)
}

// process runs one claimed job to completion. The download runs under a timeout
// in its own goroutine, and the final queue status is written here rather than
// in Process, so a late-returning straggler cannot overwrite it.
func (queueWorker *QueueWorker) process(job Job) {
	// Marks the loop busy while the job runs, so a download taking minutes is not
	// misread as a dead loop. The registry counts overlapping runners.
	endBusy := queueWorker.Heartbeat.Working(health.QueueWorker)
	defer endBusy()
	queueWorker.markCooldown(job.Platform)

	// Buffered, so a late-returning goroutine does not block forever; its result is
	// discarded after the timeout.
	done := make(chan procResult, 1)
	go func() {
		// A panic inside Process - unchecked type assertions on foreign platform JSON -
		// would otherwise take the process down. Reported as a normal job failure.
		result := procResult{failure: errDownloadPanicked}
		safego.Run("download-worker.Process", func() {
			designID, failure := queueWorker.Process(job)
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
		queueWorker.markCooldown(job.Platform) // block the platform from now on (hang = overload/block)
		final := job.RetryCount+1 >= MaxRetries
		suffix := "will be retried"
		if final {
			suffix = "marked as permanently failed"
		}
		logx.Errorf("[worker] Job #%d (%s): download TIMEOUT after %s - aborted so the "+
			"queue keeps running (hanging browser/network?). URL=%s attempt %d/%d, %s.",
			job.ID, job.Platform, procTimeout, job.SourceURL, job.RetryCount+1, MaxRetries, suffix)
		if final {
			dbutil.ExecLogged(queueWorker.DB, "UPDATE job_queue SET status='failed', error_msg='error.timeout', done_at=CURRENT_TIMESTAMP WHERE id=?", job.ID)
			queueWorker.notifyFailed(job, "error.timeout")
		} else {
			dbutil.ExecLogged(queueWorker.DB, "UPDATE job_queue SET status='pending', started_at=NULL, retry_count=retry_count+1, error_msg='error.timeout' WHERE id=?", job.ID)
		}
		return
	}

	if failure != nil {
		queueWorker.markCooldown(job.Platform) // block again from now on on failure
		// Temporary rate limit or captcha: leave the job pending with retry_count
		// untouched, so it is retried spaced out until it succeeds.
		if isSoftRateLimit(failure.Error()) {
			if queueWorker.registerBlockStrike(job.Platform, failure.Error()) {
				logx.Warnf("[worker] Job #%d (%s): anti-bot/rate-limit block threshold reached - the whole %s "+
					"queue is auto-paused and resumes on its own. URL=%s detail: %v",
					job.ID, job.Platform, job.Platform, job.SourceURL, failure)
			} else {
				logx.Warnf("[worker] Job #%d (%s): temporarily blocked by anti-bot/rate-limit - stays in the queue, "+
					"will be retried after the cooldown. URL=%s detail: %v", job.ID, job.Platform, job.SourceURL, failure)
			}
			dbutil.ExecLogged(queueWorker.DB, "UPDATE job_queue SET status='pending', started_at=NULL, error_msg=? WHERE id=?", failure.Error(), job.ID)
			return
		}
		final := isPermanent(failure.Error()) || job.RetryCount+1 >= MaxRetries
		queueWorker.logFailure(job, failure, final)
		if final {
			dbutil.ExecLogged(queueWorker.DB, "UPDATE job_queue SET status='failed', error_msg=?, done_at=CURRENT_TIMESTAMP WHERE id=?", failure.Error(), job.ID)
			queueWorker.notifyFailed(job, failure.Error())
		} else {
			dbutil.ExecLogged(queueWorker.DB, "UPDATE job_queue SET status='pending', started_at=NULL, retry_count=retry_count+1, error_msg=? WHERE id=?", failure.Error(), job.ID)
		}
		return
	}
	// An update already knows its design; an import learns it here.
	if job.Kind == jobqueue.KindUpdate {
		designID = job.DesignID
	}
	dbutil.ExecLogged(queueWorker.DB, "UPDATE job_queue SET status='done', progress=100, design_id=?, done_at=CURRENT_TIMESTAMP WHERE id=?", designID, job.ID)
	queueWorker.notifyDone(job, designID)
	// The platform is answering normally again, so a later isolated hit starts fresh.
	queueWorker.clearBlockStrikes(job.Platform)
}

// notifyFailed and notifyDone tell the member how their download ended. Raised
// only where the job reaches its final state: a pending retry is not an outcome,
// and notifying per attempt would report one download as failed several times.
func (queueWorker *QueueWorker) notifyFailed(job Job, message string) {
	// An update that gives up says so where it is watched - the design's own page
	// and the queue - and a new version announces itself when there is one. A
	// notification on top would be the third report of a check nobody asked for.
	if job.Kind == jobqueue.KindUpdate {
		return
	}
	// The URL travels along so the digest can drop this again if the member
	// re-queued it and it succeeded before the summary went out.
	notify.UserWithReference(queueWorker.DB, job.UserID, "download_failed",
		"Download failed",
		readableError(message)+"\n\n"+job.SourceURL, job.SourceURL, nil)
}

func (queueWorker *QueueWorker) notifyDone(job Job, designID int) {
	// A finished update has nothing to say unless it brought something new, and
	// that is announced where the new version is saved.
	if job.Kind == jobqueue.KindUpdate {
		return
	}
	name := job.SourceURL
	var stored string
	if queueWorker.DB.QueryRow("SELECT name FROM designs WHERE id = ?", designID).Scan(&stored) == nil && stored != "" {
		name = stored
	}
	designReference := designID
	notify.User(queueWorker.DB, job.UserID, "download_done",
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
func (queueWorker *QueueWorker) registerBlockStrike(platform, reason string) bool {
	queueWorker.mutex.Lock()
	queueWorker.blockStrikes[platform]++
	strikes := queueWorker.blockStrikes[platform]
	queueWorker.mutex.Unlock()

	if strikes < queueWorker.settingInt("queue_block_threshold", defaultBlockThreshold) {
		return false
	}

	hours := queueWorker.settingInt("queue_block_hours", defaultBlockHours)
	now := time.Now()
	queuestate.Block(queueWorker.DB, platform, now.Add(time.Duration(hours)*time.Hour), reason, now)

	queueWorker.mutex.Lock()
	queueWorker.blockStrikes[platform] = 0
	queueWorker.mutex.Unlock()
	return true
}

func (queueWorker *QueueWorker) clearBlockStrikes(platform string) {
	queueWorker.mutex.Lock()
	delete(queueWorker.blockStrikes, platform)
	queueWorker.mutex.Unlock()
}

// settingInt reads a positive integer app setting, falling back when the row is
// missing, blank or not a positive number.
func (queueWorker *QueueWorker) settingInt(key string, fallback int) int {
	var value string
	if queueWorker.DB.QueryRow("SELECT value FROM app_settings WHERE key=?", key).Scan(&value) != nil {
		return fallback
	}
	if number, failure := strconv.Atoi(strings.TrimSpace(value)); failure == nil && number > 0 {
		return number
	}
	return fallback
}

// logFailure distinguishes the failure phase (cf. platforms.DownloadError): no
// metadata found, versus a model found whose files would not download.
func (queueWorker *QueueWorker) logFailure(job Job, failure error, final bool) {
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

// ResetStuck resets running jobs that run too long, or marks them failed.
func (queueWorker *QueueWorker) ResetStuck() {
	cutoff := time.Now().Add(-stuckTimeout).UTC().Format("2006-01-02 15:04:05")
	dbutil.ExecLogged(queueWorker.DB, `UPDATE job_queue SET status='pending', retry_count=retry_count+1
		WHERE status='running' AND (started_at IS NULL OR started_at < ?) AND retry_count < ?`, cutoff, MaxRetries)
	dbutil.ExecLogged(queueWorker.DB, `UPDATE job_queue SET status='failed', error_msg='error.timeout', done_at=CURRENT_TIMESTAMP
		WHERE status='running' AND (started_at IS NULL OR started_at < ?) AND retry_count >= ?`, cutoff, MaxRetries)
}

// jobColumns and scanJob are the one way a job is read, for every claim below.
const jobColumns = `jq.id, jq.kind, COALESCE(jq.design_id, 0), jq.platform, jq.source_url, jq.user_id,
	COALESCE(u.public_id, ''), jq.retry_count`

// jobOrder puts what a person asked for ahead of what the scheduler queued, and
// otherwise keeps arrival order - an import must not wait behind a batch of
// automatic update checks.
const jobOrder = " ORDER BY jq.automatic ASC, jq.created_at ASC, jq.id ASC"

// readJobs collects the candidates and closes the rows before anything else is
// asked: with a single connection, a query while they are still open waits for
// ever.
func readJobs(rows *sql.Rows) []Job {
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var job Job
		if rows.Scan(&job.ID, &job.Kind, &job.DesignID, &job.Platform, &job.SourceURL, &job.UserID,
			&job.UserPublicID, &job.RetryCount) == nil {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// claimNext claims the next processable job atomically, respecting cooldown.
func (queueWorker *QueueWorker) claimNext() (Job, bool) {
	rows, failure := queueWorker.DB.Query(`SELECT ` + jobColumns + `
		FROM job_queue jq JOIN users u ON u.id = jq.user_id
		WHERE jq.status='pending'` + jobOrder)
	if failure != nil {
		return Job{}, false
	}
	candidates := readJobs(rows)

	for _, job := range candidates {
		// A paused or auto-blocked platform is skipped entirely; its jobs stay pending.
		if queuestate.Suspended(queueWorker.DB, job.Platform, time.Now()) {
			continue
		}
		if queueWorker.onCooldown(job.Platform) {
			continue
		}
		updateResult, failure := queueWorker.DB.Exec("UPDATE job_queue SET status='running', started_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending'", job.ID)
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
func (queueWorker *QueueWorker) claimNextFor(platform string) (Job, bool) {
	catchAll := platform == otherPlatformsRunner
	if !catchAll {
		// One check covers every job of a concrete platform.
		if queuestate.Suspended(queueWorker.DB, platform, time.Now()) || queueWorker.onCooldown(platform) {
			return Job{}, false
		}
	}

	query := `SELECT ` + jobColumns + `
		FROM job_queue jq JOIN users u ON u.id = jq.user_id
		WHERE jq.status='pending' AND `
	var args []any
	if catchAll {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(queuestate.Platforms)), ",")
		query += "jq.platform NOT IN (" + placeholders + ")"
		for _, known := range queuestate.Platforms {
			args = append(args, known)
		}
	} else {
		query += "jq.platform = ?"
		args = append(args, platform)
	}
	query += jobOrder

	rows, failure := queueWorker.DB.Query(query, args...)
	if failure != nil {
		return Job{}, false
	}
	candidates := readJobs(rows)

	for _, job := range candidates {
		// The catch-all mixes platforms, so each candidate needs its own check.
		if catchAll && (queuestate.Suspended(queueWorker.DB, job.Platform, time.Now()) || queueWorker.onCooldown(job.Platform)) {
			continue
		}
		updateResult, failure := queueWorker.DB.Exec("UPDATE job_queue SET status='running', started_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending'", job.ID)
		if failure != nil {
			continue
		}
		if affected, _ := updateResult.RowsAffected(); affected > 0 {
			return job, true
		}
	}
	return Job{}, false
}

func (queueWorker *QueueWorker) onCooldown(platform string) bool {
	queueWorker.mutex.Lock()
	defer queueWorker.mutex.Unlock()
	until, ok := queueWorker.cooldowns[platform]
	return ok && time.Now().Before(until)
}

func (queueWorker *QueueWorker) markCooldown(platform string) {
	seconds := 0
	if queueWorker.CooldownFor != nil {
		seconds = queueWorker.CooldownFor(platform)
	}
	if seconds <= 0 {
		return
	}
	queueWorker.mutex.Lock()
	queueWorker.cooldowns[platform] = time.Now().Add(time.Duration(seconds) * time.Second)
	queueWorker.mutex.Unlock()
}
