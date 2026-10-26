package worker

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"meshdepot/internal/db"
)

// newTestDB creates a fresh SQLite with schema + admin (id=1).
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, failure := db.Open(filepath.Join(t.TempDir(), "w.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	// The fake processes below report design ids of their own making, and the
	// queue points at designs through a foreign key - so the ids they use exist.
	for designID := 1; designID <= 64; designID++ {
		if _, failure := database.Exec(
			"INSERT INTO designs (id, public_id, user_id, name, source_platform) VALUES (?, ?, 1, 'Design', 'manual')",
			designID, fmt.Sprintf("design-%d", designID)); failure != nil {
			t.Fatalf("seed design %d: %v", designID, failure)
		}
	}
	return database
}

func enqueue(t *testing.T, database *sql.DB, platform string) int {
	t.Helper()
	result, failure := database.Exec("INSERT INTO job_queue (user_id, source_url, platform, status) VALUES (1, ?, ?, 'pending')", "https://x/"+platform, platform)
	if failure != nil {
		t.Fatal(failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func statusOf(t *testing.T, database *sql.DB, id int) (string, int, sql.NullInt64) {
	t.Helper()
	var status string
	var retry int
	var designID sql.NullInt64
	if failure := database.QueryRow("SELECT status, retry_count, design_id FROM job_queue WHERE id=?", id).Scan(&status, &retry, &designID); failure != nil {
		t.Fatal(failure)
	}
	return status, retry, designID
}

func TestSuccess(t *testing.T) {
	database := newTestDB(t)
	id := enqueue(t, database, "printables")
	queueWorker := NewQueueWorker(database, func(Job) (int, error) { return 42, nil }, func(string) int { return 0 })
	queueWorker.RunOnce()
	status, _, designID := statusOf(t, database, id)
	if status != "done" || !designID.Valid || designID.Int64 != 42 {
		t.Fatalf("expected done/design_id=42, got %s/%v", status, designID)
	}
}

func TestPermanentFailure(t *testing.T) {
	database := newTestDB(t)
	id := enqueue(t, database, "thingiverse")
	queueWorker := NewQueueWorker(database, func(Job) (int, error) { return 0, errors.New("error.platform_credentials_required:thingiverse") }, func(string) int { return 0 })
	queueWorker.RunOnce()
	status, retry, _ := statusOf(t, database, id)
	if status != "failed" || retry != 0 {
		t.Fatalf("expected failed/retry=0, got %s/%d", status, retry)
	}
}

func TestTransientRetry(t *testing.T) {
	database := newTestDB(t)
	id := enqueue(t, database, "printables")
	queueWorker := NewQueueWorker(database, func(Job) (int, error) { return 0, errors.New("network boom") }, func(string) int { return 0 })
	queueWorker.RunOnce()
	status, retry, _ := statusOf(t, database, id)
	if status != "pending" || retry != 1 {
		t.Fatalf("expected pending/retry=1, got %s/%d", status, retry)
	}
	// After MaxRetries-1 more failed attempts -> failed.
	queueWorker.RunOnce() // retry 1->2 (pending)
	queueWorker.RunOnce() // retry 2 -> failed (2+1>=3)
	status, _, _ = statusOf(t, database, id)
	if status != "failed" {
		t.Fatalf("expected failed after 3 attempts, got %s", status)
	}
}

func TestCooldownSkips(t *testing.T) {
	database := newTestDB(t)
	first := enqueue(t, database, "makerworld")
	second := enqueue(t, database, "makerworld")
	calls := 0
	queueWorker := NewQueueWorker(database, func(Job) (int, error) { calls++; return calls, nil }, func(platform string) int { return 30 })
	queueWorker.RunOnce() // claims first, sets cooldown
	queueWorker.RunOnce() // second is blocked by the cooldown
	statusFirst, _, _ := statusOf(t, database, first)
	statusSecond, _, _ := statusOf(t, database, second)
	if statusFirst != "done" {
		t.Fatalf("first should be done, is %s", statusFirst)
	}
	if statusSecond != "pending" {
		t.Fatalf("second should still be pending due to cooldown, is %s", statusSecond)
	}
	if calls != 1 {
		t.Fatalf("Process should run once, ran %d times", calls)
	}
}

func TestResetStuck(t *testing.T) {
	database := newTestDB(t)
	old := time.Now().Add(-20 * time.Minute).UTC().Format("2006-01-02 15:04:05")
	// hanging job with retry<3 -> back to pending
	result1, _ := database.Exec("INSERT INTO job_queue (user_id, source_url, platform, status, started_at, retry_count) VALUES (1,'u','printables','running',?,0)", old)
	id1, _ := result1.LastInsertId()
	// hanging job with retry>=3 -> failed
	result2, _ := database.Exec("INSERT INTO job_queue (user_id, source_url, platform, status, started_at, retry_count) VALUES (1,'u','printables','running',?,3)", old)
	id2, _ := result2.LastInsertId()

	queueWorker := NewQueueWorker(database, func(Job) (int, error) { return 0, nil }, func(string) int { return 0 })
	queueWorker.ResetStuck()

	status1, retry1, _ := statusOf(t, database, int(id1))
	if status1 != "pending" || retry1 != 1 {
		t.Fatalf("id1: expected pending/retry=1, got %s/%d", status1, retry1)
	}
	status2, _, _ := statusOf(t, database, int(id2))
	if status2 != "failed" {
		t.Fatalf("id2: expected failed, got %s", status2)
	}
}

// The download runs with an open transaction at the end and the database has a
// single connection, so the owner's public id has to travel with the job rather
// than being looked up later.
func TestClaimCarriesThePublicIDOfTheJobOwner(t *testing.T) {
	database := newTestDB(t)
	var publicID string
	if failure := database.QueryRow("SELECT public_id FROM users WHERE id = 1").Scan(&publicID); failure != nil {
		t.Fatalf("read the owner: %v", failure)
	}
	database.Exec("INSERT INTO job_queue (user_id, source_url, platform, status) VALUES (1, 'https://example.org/model/1', 'thingiverse', 'pending')")

	queueWorker := NewQueueWorker(database, func(job Job) (int, error) {
		if job.UserPublicID != publicID {
			t.Errorf("the job carries %q, want %q", job.UserPublicID, publicID)
		}
		return 1, nil
	}, nil)
	queueWorker.RunOnce()
}
