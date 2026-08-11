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
	"strings"
	"sync"
	"time"

	"meshdepot/internal/health"
	"meshdepot/internal/platforms"
	"meshdepot/internal/safego"
)

// tickInterval is how often the loop looks for a claimable job.
const tickInterval = 2 * time.Second

// maxRetries is the maximum number of attempts per job.
const maxRetries = 3

// stuckTimeout is the duration after which a hanging 'downloading' job is reset.
const stuckTimeout = 15 * time.Minute

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

	// finished is closed when the loop has left after stop - see Wait.
	finished chan struct{}
}

func NewDownloadWorker(db *sql.DB, process func(Job) (int, error), cooldownFor func(string) int) *DownloadWorker {
	return &DownloadWorker{
		DB:          db,
		Process:     process,
		CooldownFor: cooldownFor,
		cooldowns:   map[string]time.Time{},
		finished:    make(chan struct{}),
	}
}

// Start runs the processing until stop is closed (tick every 2 s).
func (downloadWorker *DownloadWorker) Start(stop <-chan struct{}) {
	downloadWorker.Heartbeat.Register(health.DownloadWorker, tickInterval)
	safego.Go("download-worker", func() {
		defer close(downloadWorker.finished)
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				downloadWorker.Heartbeat.Beat(health.DownloadWorker)
				// Per tick, so a panic costs one job instead of the whole loop.
				safego.Run("download-worker.RunOnce", downloadWorker.RunOnce)
			}
		}
	})
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
	// From here on the loop cannot tick: RunOnce waits for the job. Without this
	// mark the health endpoint would read the silence as a dead loop on every
	// download that takes longer than a few seconds.
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
			log.Printf("[worker] Job #%d (%s): temporarily blocked by anti-bot/rate-limit - stays in the queue, "+
				"will be retried after the cooldown. URL=%s detail: %v", job.ID, job.Platform, job.SourceURL, failure)
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
