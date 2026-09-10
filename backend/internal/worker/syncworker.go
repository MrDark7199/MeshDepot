package worker

import (
	"database/sql"
	"errors"
	"meshdepot/internal/dbutil"
	"time"

	"meshdepot/internal/health"
	"meshdepot/internal/safego"

	"meshdepot/internal/logx"
)

const syncTickInterval = 5 * time.Second

// errSyncPanicked is recorded when Process panicked; safego logs the stack.
var errSyncPanicked = errors.New("error.sync_panic")

var errSyncTimeout = errors.New("error.sync_timeout")

type SyncJob struct {
	ID       int
	DesignID int
	UserID   int
	// UserPublicID addresses the owner's storage tree; see Job.UserPublicID.
	UserPublicID string
}

type SyncWorker struct {
	DB *sql.DB
	// Process runs the re-download and saving. Injected by main.go, so the worker
	// does not depend on the platforms package.
	Process func(SyncJob) error
	// Heartbeat is wired by main.go; nil elsewhere, which the registry tolerates.
	Heartbeat *health.Registry

	// finished is closed when the loop has left after stop - see Wait.
	finished chan struct{}
}

func NewSyncWorker(db *sql.DB, process func(SyncJob) error) *SyncWorker {
	return &SyncWorker{DB: db, Process: process, finished: make(chan struct{})}
}

// Start runs the processing until stop is closed.
func (syncWorker *SyncWorker) Start(stop <-chan struct{}) {
	syncWorker.Heartbeat.Register(health.SyncWorker, syncTickInterval)
	safego.Go("sync-worker", func() {
		defer close(syncWorker.finished)
		ticker := time.NewTicker(syncTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				syncWorker.Heartbeat.Beat(health.SyncWorker)
				// Per tick, so a panic costs one job instead of the whole loop.
				safego.Run("sync-worker.RunOnce", syncWorker.RunOnce)
			}
		}
	})
}

// Wait blocks until the loop has left after stop; see DownloadWorker.Wait.
func (syncWorker *SyncWorker) Wait(limit time.Duration) bool {
	select {
	case <-syncWorker.finished:
		return true
	case <-time.After(limit):
		return false
	}
}

func (syncWorker *SyncWorker) RunOnce() {
	syncWorker.resetStuck()
	job, ok := syncWorker.claimNext()
	if !ok {
		return
	}
	// The loop cannot tick while it waits for the job.
	endBusy := syncWorker.Heartbeat.Working(health.SyncWorker)
	defer endBusy()
	// Process runs under a timeout in its own goroutine: called synchronously, one
	// hanging sync would freeze the whole queue until the process restarts. The
	// channel is buffered so a late-returning goroutine does not leak.
	done := make(chan error, 1)
	go func() {
		failure := errSyncPanicked
		safego.Run("sync-worker.Process", func() { failure = syncWorker.Process(job) })
		done <- failure
	}()

	var failure error
	select {
	case failure = <-done:
	case <-time.After(procTimeout):
		failure = errSyncTimeout
		logx.Errorf("[sync] Job #%d (design %d): TIMEOUT after %s - aborted so the sync queue keeps running.",
			job.ID, job.DesignID, procTimeout)
	}
	if failure != nil {
		dbutil.ExecLogged(syncWorker.DB, "UPDATE sync_queue SET status='failed', error_msg=?, done_at=CURRENT_TIMESTAMP WHERE id=?", failure.Error(), job.ID)
		return
	}
	dbutil.ExecLogged(syncWorker.DB, "UPDATE sync_queue SET status='done', progress=100, done_at=CURRENT_TIMESTAMP WHERE id=?", job.ID)
}

// resetStuck measures from the claim time, not created_at: the latter measures
// how long the job waited in the queue, so a long wait used to reset the job the
// instant it started. started_at is NULL for rows written before the column.
func (syncWorker *SyncWorker) resetStuck() {
	cutoff := time.Now().Add(-stuckTimeout).UTC().Format("2006-01-02 15:04:05")
	dbutil.ExecLogged(syncWorker.DB, "UPDATE sync_queue SET status='pending' WHERE status='running' AND COALESCE(started_at, created_at) < ?", cutoff)
}

func (syncWorker *SyncWorker) claimNext() (SyncJob, bool) {
	rows, failure := syncWorker.DB.Query(`SELECT sq.id, sq.design_id, sq.user_id, COALESCE(u.public_id, '')
		FROM sync_queue sq JOIN users u ON u.id = sq.user_id
		WHERE sq.status='pending' ORDER BY sq.created_at ASC`)
	if failure != nil {
		return SyncJob{}, false
	}
	var candidates []SyncJob
	for rows.Next() {
		var job SyncJob
		if rows.Scan(&job.ID, &job.DesignID, &job.UserID, &job.UserPublicID) == nil {
			candidates = append(candidates, job)
		}
	}
	rows.Close()
	for _, job := range candidates {
		updateResult, failure := syncWorker.DB.Exec("UPDATE sync_queue SET status='running', started_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending'", job.ID)
		if failure != nil {
			continue
		}
		if affected, _ := updateResult.RowsAffected(); affected > 0 {
			return job, true
		}
	}
	return SyncJob{}, false
}
