package worker

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"meshdepot/internal/publicid"
)

// enqueueSync inserts a pending sync job and, since sync_queue.design_id is a
// foreign key, the design it refers to.
func enqueueSync(t *testing.T, database *sql.DB, designID int) int {
	t.Helper()
	insertDesign(t, database, designID)
	result, failure := database.Exec(
		"INSERT INTO sync_queue (design_id, user_id, status) VALUES (?, 1, 'pending')", designID,
	)
	if failure != nil {
		t.Fatalf("insert sync job: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func insertDesign(t *testing.T, database *sql.DB, designID int) {
	t.Helper()
	// Its own public id: the unique index means a second design that leaves the
	// column at its default collides, and INSERT OR IGNORE would swallow that
	// silently - leaving the sync job below without the design it references.
	_, failure := database.Exec(
		"INSERT OR IGNORE INTO designs (id, public_id, user_id, name, source_platform) VALUES (?, ?, 1, 'Design', 'manual')",
		designID, publicid.New(),
	)
	if failure != nil {
		t.Fatalf("insert design: %v", failure)
	}
}

// syncStatusOf reads status, progress and error message of a sync job.
func syncStatusOf(t *testing.T, database *sql.DB, id int) (string, int, sql.NullString) {
	t.Helper()
	var status string
	var progress int
	var errorMessage sql.NullString
	failure := database.QueryRow(
		"SELECT status, progress, error_msg FROM sync_queue WHERE id = ?", id,
	).Scan(&status, &progress, &errorMessage)
	if failure != nil {
		t.Fatalf("read sync job: %v", failure)
	}
	return status, progress, errorMessage
}

func TestSyncWorkerMarksJobDone(t *testing.T) {
	database := newTestDB(t)
	id := enqueueSync(t, database, 7)
	var processed SyncJob

	syncWorker := NewSyncWorker(database, func(job SyncJob) error {
		processed = job
		return nil
	})
	syncWorker.RunOnce()

	status, progress, errorMessage := syncStatusOf(t, database, id)
	if status != "done" || progress != 100 {
		t.Fatalf("expected done/100, got %s/%d", status, progress)
	}
	if errorMessage.Valid {
		t.Fatalf("an error message was written: %v", errorMessage)
	}
	if processed.ID != id || processed.DesignID != 7 || processed.UserID != 1 {
		t.Fatalf("the job was handed over incompletely: %+v", processed)
	}
}

func TestSyncWorkerMarksJobFailed(t *testing.T) {
	database := newTestDB(t)
	id := enqueueSync(t, database, 7)

	syncWorker := NewSyncWorker(database, func(SyncJob) error {
		return errors.New("error.sync_failed")
	})
	syncWorker.RunOnce()

	status, _, errorMessage := syncStatusOf(t, database, id)
	if status != "failed" {
		t.Fatalf("expected failed, got %s", status)
	}
	if !errorMessage.Valid || errorMessage.String != "error.sync_failed" {
		t.Fatalf("unexpected error message %v", errorMessage)
	}
}

// A panic in Process comes from a type assertion on foreign platform JSON. It
// must cost the one job, not the process.
func TestSyncWorkerSurvivesPanickingProcess(t *testing.T) {
	database := newTestDB(t)
	id := enqueueSync(t, database, 7)

	syncWorker := NewSyncWorker(database, func(SyncJob) error {
		panic("unexpected platform response")
	})
	syncWorker.RunOnce()

	status, _, errorMessage := syncStatusOf(t, database, id)
	if status != "failed" {
		t.Fatalf("expected failed, got %s", status)
	}
	if !errorMessage.Valid || errorMessage.String != errSyncPanicked.Error() {
		t.Fatalf("unexpected error message %v", errorMessage)
	}
}

func TestSyncWorkerWithoutJobDoesNothing(t *testing.T) {
	database := newTestDB(t)
	called := false

	syncWorker := NewSyncWorker(database, func(SyncJob) error {
		called = true
		return nil
	})
	syncWorker.RunOnce()

	if called {
		t.Fatal("Process ran although the queue was empty")
	}
}

// Claiming has to move the row out of 'pending' before Process starts, so a
// second pass cannot pick up the same job.
func TestSyncWorkerClaimsOnlyOneJobPerRun(t *testing.T) {
	database := newTestDB(t)
	first := enqueueSync(t, database, 7)
	second := enqueueSync(t, database, 8)

	var processedIDs []int
	syncWorker := NewSyncWorker(database, func(job SyncJob) error {
		processedIDs = append(processedIDs, job.ID)
		return nil
	})
	syncWorker.RunOnce()

	if len(processedIDs) != 1 || processedIDs[0] != first {
		t.Fatalf("unexpected processing order %v", processedIDs)
	}
	status, _, _ := syncStatusOf(t, database, second)
	if status != "pending" {
		t.Fatalf("the second job is already %s", status)
	}

	syncWorker.RunOnce()
	if len(processedIDs) != 2 || processedIDs[1] != second {
		t.Fatalf("the second run did not pick up the second job: %v", processedIDs)
	}
}

func TestSyncWorkerClaimNextSetsStartedAt(t *testing.T) {
	database := newTestDB(t)
	id := enqueueSync(t, database, 7)

	syncWorker := NewSyncWorker(database, func(SyncJob) error { return nil })
	job, ok := syncWorker.claimNext()
	if !ok || job.ID != id {
		t.Fatalf("the job was not claimed: %+v/%v", job, ok)
	}

	var status string
	var startedAt sql.NullString
	database.QueryRow("SELECT status, started_at FROM sync_queue WHERE id = ?", id).Scan(&status, &startedAt)
	if status != "running" {
		t.Fatalf("unexpected status %s", status)
	}
	if !startedAt.Valid {
		t.Fatal("started_at was not set")
	}

	if _, ok := syncWorker.claimNext(); ok {
		t.Fatal("the same job was claimed twice")
	}
}

func TestSyncWorkerClaimNextOnBrokenDatabase(t *testing.T) {
	database := newTestDB(t)
	database.Close()

	syncWorker := NewSyncWorker(database, func(SyncJob) error { return nil })
	if _, ok := syncWorker.claimNext(); ok {
		t.Fatal("a closed database reported a claimed job")
	}
}

// The cutoff is the claim time, not the queue time: a job that waited longer
// than stuckTimeout used to be reset the instant it started running.
func TestSyncWorkerResetStuckUsesStartedAt(t *testing.T) {
	database := newTestDB(t)
	longAgo := time.Now().Add(-20 * time.Minute).UTC().Format("2006-01-02 15:04:05")

	insertDesign(t, database, 1)
	insertDesign(t, database, 2)
	hanging, _ := database.Exec(
		"INSERT INTO sync_queue (design_id, user_id, status, started_at) VALUES (1, 1, 'running', ?)", longAgo,
	)
	hangingID, _ := hanging.LastInsertId()
	// Queued long ago, but claimed just now - must keep running.
	freshlyStarted, _ := database.Exec(
		"INSERT INTO sync_queue (design_id, user_id, status, created_at, started_at) VALUES (2, 1, 'running', ?, CURRENT_TIMESTAMP)", longAgo,
	)
	freshlyStartedID, _ := freshlyStarted.LastInsertId()

	syncWorker := NewSyncWorker(database, func(SyncJob) error { return nil })
	syncWorker.resetStuck()

	if status, _, _ := syncStatusOf(t, database, int(hangingID)); status != "pending" {
		t.Fatalf("the hanging job is %s", status)
	}
	if status, _, _ := syncStatusOf(t, database, int(freshlyStartedID)); status != "running" {
		t.Fatalf("a job that just started was reset to %s", status)
	}
}

// Rows written before started_at existed fall back to created_at.
func TestSyncWorkerResetStuckFallsBackToCreatedAt(t *testing.T) {
	database := newTestDB(t)
	longAgo := time.Now().Add(-20 * time.Minute).UTC().Format("2006-01-02 15:04:05")

	insertDesign(t, database, 1)
	result, _ := database.Exec(
		"INSERT INTO sync_queue (design_id, user_id, status, created_at, started_at) VALUES (1, 1, 'running', ?, NULL)", longAgo,
	)
	id, _ := result.LastInsertId()

	syncWorker := NewSyncWorker(database, func(SyncJob) error { return nil })
	syncWorker.resetStuck()

	if status, _, _ := syncStatusOf(t, database, int(id)); status != "pending" {
		t.Fatalf("the legacy row is %s", status)
	}
}

func TestSyncWorkerStartProcessesQueueAndStops(t *testing.T) {
	database := newTestDB(t)
	id := enqueueSync(t, database, 7)
	processed := make(chan struct{}, 1)

	syncWorker := NewSyncWorker(database, func(SyncJob) error {
		processed <- struct{}{}
		return nil
	})
	stop := make(chan struct{})
	syncWorker.Start(stop)

	select {
	case <-processed:
	case <-time.After(30 * time.Second):
		t.Fatal("the loop did not pick up the job")
	}

	close(stop)
	if !syncWorker.Wait(30 * time.Second) {
		t.Fatal("the loop did not leave after stop")
	}
	if status, _, _ := syncStatusOf(t, database, id); status != "done" {
		t.Fatalf("unexpected status %s", status)
	}
}

// Wait must not block forever - the supervisor kills the process long before a
// large download finishes.
func TestSyncWorkerWaitTimesOut(t *testing.T) {
	database := newTestDB(t)
	syncWorker := NewSyncWorker(database, func(SyncJob) error { return nil })

	if syncWorker.Wait(20 * time.Millisecond) {
		t.Fatal("Wait reported a finished loop although none was started")
	}
}
