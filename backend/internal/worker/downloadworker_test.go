package worker

import (
	"errors"
	"testing"
	"time"

	"meshdepot/internal/platforms"
)

func TestIsPermanentCoversEverySignature(t *testing.T) {
	for _, signature := range permanentErrors {
		if !isPermanent("prefix " + signature + " suffix") {
			t.Fatalf("%q was not recognised as permanent", signature)
		}
	}
	for _, message := range []string{"", "network boom", "error.timeout", "connection reset"} {
		if isPermanent(message) {
			t.Fatalf("%q was wrongly classified as permanent", message)
		}
	}
}

func TestIsSoftRateLimitCoversEverySignature(t *testing.T) {
	for _, signature := range softRateLimitErrors {
		if !isSoftRateLimit("prefix " + signature) {
			t.Fatalf("%q was not recognised as a rate limit", signature)
		}
	}
	for _, message := range []string{"", "network boom", "error.unsupported_url"} {
		if isSoftRateLimit(message) {
			t.Fatalf("%q was wrongly classified as a rate limit", message)
		}
	}
}

// A captcha is "try again later", not a failure: the job stays pending and
// retry_count is untouched, so it is never burned after maxRetries.
func TestSoftRateLimitKeepsJobPendingWithoutBurningRetries(t *testing.T) {
	database := newTestDB(t)
	id := enqueue(t, database, "makerworld")

	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) {
		return 0, errors.New("error.makerworld_captcha")
	}, func(string) int { return 0 })

	for round := 0; round < maxRetries+2; round++ {
		downloadWorker.RunOnce()
		status, retry, _ := statusOf(t, database, id)
		if status != "pending" {
			t.Fatalf("round %d: the job is %s", round+1, status)
		}
		if retry != 0 {
			t.Fatalf("round %d: retry_count was raised to %d", round+1, retry)
		}
	}

	var errorMessage string
	database.QueryRow("SELECT error_msg FROM download_queue WHERE id = ?", id).Scan(&errorMessage)
	if errorMessage != "error.makerworld_captcha" {
		t.Fatalf("unexpected error message %q", errorMessage)
	}
}

// A panic in Process is reported as an ordinary job failure so the queue keeps
// running.
func TestPanickingProcessBecomesRetryableFailure(t *testing.T) {
	database := newTestDB(t)
	id := enqueue(t, database, "printables")

	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) {
		panic("unexpected platform response")
	}, func(string) int { return 0 })
	downloadWorker.RunOnce()

	status, retry, _ := statusOf(t, database, id)
	if status != "pending" || retry != 1 {
		t.Fatalf("expected pending/retry=1, got %s/%d", status, retry)
	}
	var errorMessage string
	database.QueryRow("SELECT error_msg FROM download_queue WHERE id = ?", id).Scan(&errorMessage)
	if errorMessage != errDownloadPanicked.Error() {
		t.Fatalf("unexpected error message %q", errorMessage)
	}
}

// The two failure phases produce different log lines; both have to be reachable
// through the same error interface.
func TestLogFailureHandlesEveryStage(t *testing.T) {
	database := newTestDB(t)
	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) { return 0, nil }, func(string) int { return 0 })
	job := Job{ID: 1, Platform: "printables", SourceURL: "https://example.org/model/1"}

	for _, failure := range []error{
		platforms.MetadataError("error.printables_no_metadata"),
		platforms.FilesError("error.printables_no_files"),
		errors.New("network boom"),
	} {
		downloadWorker.logFailure(job, failure, false)
		downloadWorker.logFailure(job, failure, true)
	}
}

// A typed metadata error still carries its i18n key, so the permanence check
// keeps working through the wrapper.
func TestTypedDownloadErrorKeepsPermanenceClassification(t *testing.T) {
	database := newTestDB(t)
	id := enqueue(t, database, "printables")

	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) {
		return 0, platforms.FilesError("error.printables_no_files")
	}, func(string) int { return 0 })
	downloadWorker.RunOnce()

	status, retry, _ := statusOf(t, database, id)
	if status != "failed" {
		t.Fatalf("expected failed on the first attempt, got %s", status)
	}
	if retry != 0 {
		t.Fatalf("a permanent failure raised retry_count to %d", retry)
	}
}

func TestRunOnceWithEmptyQueueDoesNothing(t *testing.T) {
	database := newTestDB(t)
	called := false

	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) {
		called = true
		return 0, nil
	}, func(string) int { return 0 })
	downloadWorker.RunOnce()

	if called {
		t.Fatal("Process ran although the queue was empty")
	}
}

func TestClaimNextHandsOverTheWholeJob(t *testing.T) {
	database := newTestDB(t)
	if _, failure := database.Exec(
		"INSERT INTO download_queue (user_id, source_url, platform, status, retry_count) VALUES (1, 'https://example.org/model/1', 'cults3d', 'pending', 2)",
	); failure != nil {
		t.Fatalf("insert job: %v", failure)
	}

	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) { return 0, nil }, func(string) int { return 0 })
	job, ok := downloadWorker.claimNext()
	if !ok {
		t.Fatal("no job was claimed")
	}
	if job.Platform != "cults3d" || job.SourceURL != "https://example.org/model/1" {
		t.Fatalf("unexpected job %+v", job)
	}
	if job.UserID != 1 || job.RetryCount != 2 {
		t.Fatalf("user or retry count was lost: %+v", job)
	}

	if _, ok := downloadWorker.claimNext(); ok {
		t.Fatal("the same job was claimed twice")
	}
}

func TestClaimNextOnBrokenDatabase(t *testing.T) {
	database := newTestDB(t)
	database.Close()

	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) { return 0, nil }, func(string) int { return 0 })
	if _, ok := downloadWorker.claimNext(); ok {
		t.Fatal("a closed database reported a claimed job")
	}
}

// The cooldown only blocks the platform it was set for.
func TestCooldownIsPerPlatform(t *testing.T) {
	database := newTestDB(t)
	blocked := enqueue(t, database, "makerworld")
	other := enqueue(t, database, "printables")

	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) { return 1, nil }, func(platform string) int {
		if platform == "makerworld" {
			return 30
		}
		return 0
	})
	downloadWorker.RunOnce()
	downloadWorker.RunOnce()

	if status, _, _ := statusOf(t, database, blocked); status != "done" {
		t.Fatalf("the first job is %s", status)
	}
	if status, _, _ := statusOf(t, database, other); status != "done" {
		t.Fatalf("a different platform was blocked as well: %s", status)
	}
}

func TestMarkCooldownIgnoresZeroAndMissingConfiguration(t *testing.T) {
	database := newTestDB(t)

	withoutConfiguration := NewDownloadWorker(database, nil, nil)
	withoutConfiguration.markCooldown("printables")
	if withoutConfiguration.onCooldown("printables") {
		t.Fatal("a platform is blocked although no cooldown is configured")
	}

	withZero := NewDownloadWorker(database, nil, func(string) int { return 0 })
	withZero.markCooldown("printables")
	if withZero.onCooldown("printables") {
		t.Fatal("a cooldown of 0 seconds blocks the platform")
	}
}

func TestOnCooldownExpires(t *testing.T) {
	database := newTestDB(t)
	downloadWorker := NewDownloadWorker(database, nil, func(string) int { return 1 })

	downloadWorker.markCooldown("printables")
	if !downloadWorker.onCooldown("printables") {
		t.Fatal("the cooldown is not active right after it was set")
	}

	downloadWorker.mutex.Lock()
	downloadWorker.cooldowns["printables"] = time.Now().Add(-time.Second)
	downloadWorker.mutex.Unlock()

	if downloadWorker.onCooldown("printables") {
		t.Fatal("an expired cooldown still blocks")
	}
}

func TestResetStuckLeavesRunningJobsAlone(t *testing.T) {
	database := newTestDB(t)
	result, _ := database.Exec(
		"INSERT INTO download_queue (user_id, source_url, platform, status, started_at, retry_count) VALUES (1, 'u', 'printables', 'downloading', CURRENT_TIMESTAMP, 0)",
	)
	id, _ := result.LastInsertId()

	downloadWorker := NewDownloadWorker(database, nil, func(string) int { return 0 })
	downloadWorker.ResetStuck()

	if status, retry, _ := statusOf(t, database, int(id)); status != "downloading" || retry != 0 {
		t.Fatalf("a job that just started was reset: %s/%d", status, retry)
	}
}

func TestStartProcessesQueueAndStops(t *testing.T) {
	database := newTestDB(t)
	id := enqueue(t, database, "printables")
	processed := make(chan struct{}, 1)

	downloadWorker := NewDownloadWorker(database, func(Job) (int, error) {
		processed <- struct{}{}
		return 42, nil
	}, func(string) int { return 0 })
	stop := make(chan struct{})
	downloadWorker.Start(stop)

	select {
	case <-processed:
	case <-time.After(30 * time.Second):
		t.Fatal("the loop did not pick up the job")
	}

	close(stop)
	if !downloadWorker.Wait(30 * time.Second) {
		t.Fatal("the loop did not leave after stop")
	}
	if status, _, designID := statusOf(t, database, id); status != "done" || designID.Int64 != 42 {
		t.Fatalf("unexpected result %s/%v", status, designID)
	}
}

// Waiting forever is not an option: the supervisor kills the process long before
// a large download finishes.
func TestWaitTimesOut(t *testing.T) {
	database := newTestDB(t)
	downloadWorker := NewDownloadWorker(database, nil, func(string) int { return 0 })

	if downloadWorker.Wait(20 * time.Millisecond) {
		t.Fatal("Wait reported a finished loop although none was started")
	}
}
