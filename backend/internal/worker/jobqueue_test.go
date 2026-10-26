package worker

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"meshdepot/internal/queuestate"
)

// enqueueUpdate writes an update check for one of the seeded designs, as the
// scheduler (automatic) or a person (not) would.
func enqueueUpdate(t *testing.T, database *sql.DB, designID int, platform string, automatic bool) int {
	t.Helper()
	flag := 0
	if automatic {
		flag = 1
	}
	result, failure := database.Exec(`INSERT INTO job_queue (kind, user_id, design_id, source_url, platform, automatic, status)
		VALUES ('update', 1, ?, ?, ?, ?, 'pending')`, designID, "https://x/"+platform+"/update", platform, flag)
	if failure != nil {
		t.Fatal(failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func jobStatus(t *testing.T, database *sql.DB, id int) (status string, retries int) {
	t.Helper()
	if failure := database.QueryRow("SELECT status, retry_count FROM job_queue WHERE id = ?", id).Scan(&status, &retries); failure != nil {
		t.Fatal(failure)
	}
	return status, retries
}

// The captcha that started all this: an update check turned away by the
// platform waits instead of failing, and the platform is paused - the same as a
// download, because it is the same door.
func TestUpdateCheckTurnedAwayPausesThePlatform(t *testing.T) {
	database := newTestDB(t)
	database.Exec("INSERT OR REPLACE INTO app_settings (key, value) VALUES ('queue_block_threshold', '1')")
	id := enqueueUpdate(t, database, 5, "makerworld", true)

	queueWorker := NewQueueWorker(database, func(Job) (int, error) {
		return 0, errors.New("error.makerworld_captcha:MakerWorld blocked the download")
	}, func(string) int { return 0 })
	queueWorker.RunOnce()

	if status, retries := jobStatus(t, database, id); status != "pending" || retries != 0 {
		t.Fatalf("the check is %s after %d attempts - a captcha is a wait, not a failure", status, retries)
	}
	if !queuestate.Suspended(database, "makerworld", time.Now()) {
		t.Fatal("makerworld was not paused, so the next check walks into the same captcha")
	}
}

// Once a platform is paused, nothing of it runs - imports and update checks
// alike, which is what two queues could not promise.
func TestPausedPlatformHoldsUpdatesAndImportsAlike(t *testing.T) {
	database := newTestDB(t)
	update := enqueueUpdate(t, database, 6, "makerworld", false)
	download := enqueue(t, database, "makerworld")
	queuestate.Block(database, "makerworld", time.Now().Add(time.Hour), "captcha", time.Now())

	ran := 0
	queueWorker := NewQueueWorker(database, func(Job) (int, error) { ran++; return 6, nil }, func(string) int { return 0 })
	queueWorker.RunOnce()
	queueWorker.RunOnce()

	if ran != 0 {
		t.Fatalf("%d job(s) ran on a paused platform", ran)
	}
	for _, id := range []int{update, download} {
		if status, _ := jobStatus(t, database, id); status != "pending" {
			t.Fatalf("job %d is %s rather than waiting", id, status)
		}
	}
}

// An update check fails the way a download does: three attempts, then failed
// with its message.
func TestUpdateCheckIsTriedThreeTimes(t *testing.T) {
	database := newTestDB(t)
	id := enqueueUpdate(t, database, 7, "printables", false)

	queueWorker := NewQueueWorker(database, func(Job) (int, error) { return 0, errors.New("network boom") },
		func(string) int { return 0 })
	for attempt := 1; attempt < MaxRetries; attempt++ {
		queueWorker.RunOnce()
		if status, retries := jobStatus(t, database, id); status != "pending" || retries != attempt {
			t.Fatalf("after attempt %d the check is %s with %d retries", attempt, status, retries)
		}
	}
	queueWorker.RunOnce()
	if status, _ := jobStatus(t, database, id); status != "failed" {
		t.Fatalf("after %d attempts the check is %s", MaxRetries, status)
	}
}

// What a person asked for goes first: an import must not wait behind a batch
// of checks the scheduler queued earlier.
func TestRequestedJobsGoBeforeAutomaticOnes(t *testing.T) {
	database := newTestDB(t)
	enqueueUpdate(t, database, 8, "printables", true)
	enqueueUpdate(t, database, 9, "printables", true)
	asked := enqueue(t, database, "printables")

	var first Job
	queueWorker := NewQueueWorker(database, func(job Job) (int, error) {
		if first.ID == 0 {
			first = job
		}
		return 10, nil
	}, func(string) int { return 0 })
	queueWorker.RunOnce()

	if first.ID != asked {
		t.Fatalf("job %d (%s) ran first instead of the requested import %d", first.ID, first.Kind, asked)
	}
}

// An update ends on the design it was about, whatever the process reports.
func TestUpdateCheckKeepsItsDesign(t *testing.T) {
	database := newTestDB(t)
	id := enqueueUpdate(t, database, 11, "printables", false)

	queueWorker := NewQueueWorker(database, func(job Job) (int, error) {
		if job.Kind != "update" || job.DesignID != 11 {
			t.Errorf("the process saw %q for design %d", job.Kind, job.DesignID)
		}
		return 0, nil
	}, func(string) int { return 0 })
	queueWorker.RunOnce()

	var status string
	var designID int
	database.QueryRow("SELECT status, design_id FROM job_queue WHERE id = ?", id).Scan(&status, &designID)
	if status != "done" || designID != 11 {
		t.Fatalf("the check ended %s on design %d", status, designID)
	}
}
