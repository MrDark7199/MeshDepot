package api

import (
	"fmt"
	"net/http"
	"testing"

	"meshdepot/internal/coerce"
)

// printablesURL is the URL of choice wherever the queueing itself is what is
// under test. Printables needs a platform account like every other platform, so
// those tests call withPrintablesAccount first.
const printablesURL = "https://www.printables.com/model/98765-lampe"

const thingiverseURL = "https://www.thingiverse.com/thing:12345"

// withPrintablesAccount stores the login the printables downloader needs, so a
// queue request gets past the credential check.
func withPrintablesAccount(testHarness *harness) *harness {
	testHarness.insertPlatformAccount(testHarness.userID, "printables", "", "member@example.org")
	return testHarness
}

func TestDownloadQueueAcceptsASupportedURL(t *testing.T) {
	testHarness := withPrintablesAccount(newHarness(t))

	answer := testHarness.asUser(http.MethodPost, "/api/v1/download", map[string]any{"source_url": printablesURL})

	if answer.status != http.StatusCreated {
		t.Fatalf("queueing answered %d: %s", answer.status, answer.rawBody)
	}
	queued := answer.data(t)
	if queued["platform"] != "printables" || queued["status"] != "pending" {
		t.Fatalf("unexpected queue entry %s", answer.rawBody)
	}
	stored := testHarness.count("SELECT COUNT(*) FROM download_queue WHERE user_id = ? AND source_url = ? AND status = 'pending'",
		testHarness.userID, printablesURL)
	if stored != 1 {
		t.Fatal("no pending job was stored")
	}
}

func TestDownloadQueueNeedsAURL(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPost, "/api/v1/download", map[string]any{"source_url": "   "})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a blank url answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.url_required" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// The platform is taken from the parsed host. A URL that only mentions a
// platform in its query string is not that platform - that check is what keeps
// the downloaders from being pointed at an arbitrary server.
func TestDownloadQueueRejectsAnUnsupportedURL(t *testing.T) {
	testHarness := newHarness(t)

	for _, url := range []string{
		"https://example.org/model/1",
		"http://attacker.example/?x=cults3d.com",
		"https://thingiverse.com.attacker.example/thing:1",
	} {
		answer := testHarness.asUser(http.MethodPost, "/api/v1/download", map[string]any{"source_url": url})
		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("%q answered %d", url, answer.status)
		}
		if key := answer.errorKey(t); key != "error.unsupported_platform" {
			t.Fatalf("%q produced the error key %q", url, key)
		}
	}
	if testHarness.count("SELECT COUNT(*) FROM download_queue") != 0 {
		t.Fatal("a job was queued anyway")
	}
}

func TestDownloadQueueDemandsPlatformCredentials(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPost, "/api/v1/download", map[string]any{"source_url": thingiverseURL})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a platform without credentials answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.platform_credentials_required:thingiverse" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestDownloadQueueAcceptsAPlatformOnceItsCredentialsExist(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertPlatformAccount(testHarness.userID, "thingiverse", "ein-token", "")

	answer := testHarness.asUser(http.MethodPost, "/api/v1/download", map[string]any{"source_url": thingiverseURL})

	if answer.status != http.StatusCreated {
		t.Fatalf("queueing answered %d: %s", answer.status, answer.rawBody)
	}
}

// MakerWorld needs both halves of the credentials, so a token alone is not
// enough.
func TestDownloadQueueDemandsBothMakerWorldCredentials(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertPlatformAccount(testHarness.userID, "makerworld", "ein-token", "")

	answer := testHarness.asUser(http.MethodPost, "/api/v1/download",
		map[string]any{"source_url": "https://makerworld.com/en/models/55555"})

	if key := answer.errorKey(t); key != "error.platform_credentials_required:makerworld" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// Cults3D downloads with the login, not with the API key, so the username alone
// gets the job through.
func TestDownloadQueueAcceptsCults3dWithOnlyAUsername(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertPlatformAccount(testHarness.userID, "cults3d", "", "jemand@example.org")

	answer := testHarness.asUser(http.MethodPost, "/api/v1/download",
		map[string]any{"source_url": "https://cults3d.com/en/3d-model/tool/mein-modell"})

	if answer.status != http.StatusCreated {
		t.Fatalf("queueing answered %d: %s", answer.status, answer.rawBody)
	}
}

func TestDownloadQueueRefusesAnAlreadyDownloadedDesign(t *testing.T) {
	testHarness := withPrintablesAccount(newHarness(t))
	designID := testHarness.insertDesign(testHarness.userID, "Schon da")
	testHarness.setDesignFields(designID, map[string]any{"source_url": printablesURL})

	answer := testHarness.asUser(http.MethodPost, "/api/v1/download", map[string]any{"source_url": printablesURL})

	if answer.status != http.StatusConflict {
		t.Fatalf("a duplicate answered %d", answer.status)
	}
	// The design name travels in the key as a parameter so the frontend can name
	// the design in its message.
	if key := answer.errorKey(t); key != "error.duplicate_design:Schon da" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// The duplicate check is per user: the same model may be downloaded by two
// people.
func TestDownloadQueueIgnoresTheSameURLInAForeignLibrary(t *testing.T) {
	testHarness := withPrintablesAccount(newHarness(t))
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")
	testHarness.setDesignFields(foreign, map[string]any{"source_url": printablesURL})

	answer := testHarness.asUser(http.MethodPost, "/api/v1/download", map[string]any{"source_url": printablesURL})

	if answer.status != http.StatusCreated {
		t.Fatalf("queueing answered %d: %s", answer.status, answer.rawBody)
	}
}

func TestDownloadListShowsRunningAndRecentJobsOnly(t *testing.T) {
	testHarness := newHarness(t)
	pending := testHarness.insertDownloadJob(testHarness.userID, printablesURL, "pending", "")
	freshlyDone := testHarness.insertDownloadJob(testHarness.userID, printablesURL, "done", "datetime('now', '-10 seconds')")
	testHarness.insertDownloadJob(testHarness.userID, printablesURL, "done", "datetime('now', '-10 minutes')")
	recentlyFailed := testHarness.insertDownloadJob(testHarness.userID, printablesURL, "failed", "datetime('now', '-2 days')")
	testHarness.insertDownloadJob(testHarness.userID, printablesURL, "failed", "datetime('now', '-30 days')")
	testHarness.insertDownloadJob(testHarness.adminID, printablesURL, "pending", "")

	jobs := testHarness.asUser(http.MethodGet, "/api/v1/download/queue", nil).list(t)

	found := map[int]bool{}
	for _, job := range jobs {
		found[coerce.Int(job.(map[string]any)["id"])] = true
	}
	if len(jobs) != 3 {
		t.Fatalf("%d jobs were listed: %v", len(jobs), found)
	}
	if !found[pending] || !found[freshlyDone] || !found[recentlyFailed] {
		t.Fatalf("a job is missing from the list: %v", found)
	}
}

func TestDownloadStatusReturnsTheJobWithItsDesignName(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Fertig geladen")
	jobID := testHarness.insertDownloadJob(testHarness.userID, printablesURL, "done", "datetime('now')")
	if _, failure := testHarness.database.Exec("UPDATE download_queue SET design_id = ? WHERE id = ?", designID, jobID); failure != nil {
		t.Fatalf("link the design: %v", failure)
	}

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/download/%d", jobID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("status answered %d: %s", answer.status, answer.rawBody)
	}
	if name := answer.data(t)["design_name"]; name != "Fertig geladen" {
		t.Fatalf("the design name is %v", name)
	}
}

func TestDownloadStatusRejectsAForeignJob(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDownloadJob(testHarness.adminID, printablesURL, "pending", "")

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/download/%d", foreign), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign job answered %d", answer.status)
	}
}

func TestDownloadRetryResetsTheJob(t *testing.T) {
	testHarness := withPrintablesAccount(newHarness(t))
	jobID := testHarness.insertDownloadJob(testHarness.userID, printablesURL, "failed", "datetime('now')")
	if _, failure := testHarness.database.Exec(
		"UPDATE download_queue SET error_msg = 'kaputt', retry_count = 3 WHERE id = ?", jobID); failure != nil {
		t.Fatalf("prepare the failed job: %v", failure)
	}

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/download/%d/retry", jobID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("retry answered %d: %s", answer.status, answer.rawBody)
	}
	reset := testHarness.count(
		"SELECT COUNT(*) FROM download_queue WHERE id = ? AND status = 'pending' AND error_msg IS NULL AND done_at IS NULL AND retry_count = 0", jobID)
	if reset != 1 {
		t.Fatal("the job was not reset")
	}
}

// A retry runs into the same credential check as the first attempt - otherwise
// it would just fail again in the worker.
func TestDownloadRetryChecksThePlatformCredentials(t *testing.T) {
	testHarness := newHarness(t)
	jobID := testHarness.insertDownloadJobForPlatform(testHarness.userID, thingiverseURL, "thingiverse", "failed")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/download/%d/retry", jobID), nil)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("the retry without credentials answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.platform_credentials_required:thingiverse" {
		t.Fatalf("unexpected error key %q", key)
	}
	if status := testHarness.scalar("SELECT status FROM download_queue WHERE id = ?", jobID); status != "failed" {
		t.Fatalf("the job was reset to %q anyway", status)
	}
}

func TestDownloadRetryRejectsAForeignJob(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDownloadJob(testHarness.adminID, printablesURL, "failed", "datetime('now')")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/download/%d/retry", foreign), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign job answered %d", answer.status)
	}
	if status := testHarness.scalar("SELECT status FROM download_queue WHERE id = ?", foreign); status != "failed" {
		t.Fatalf("the foreign job was reset to %q", status)
	}
}

func TestDownloadCancelRemovesAPendingJob(t *testing.T) {
	testHarness := newHarness(t)
	jobID := testHarness.insertDownloadJob(testHarness.userID, printablesURL, "pending", "")

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/download/%d", jobID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("cancel answered %d: %s", answer.status, answer.rawBody)
	}
	if testHarness.count("SELECT COUNT(*) FROM download_queue WHERE id = ?", jobID) != 0 {
		t.Fatal("the job survived")
	}
}

// A finished job is no longer cancellable - the answer has to say so instead of
// pretending success.
func TestDownloadCancelRefusesAFinishedJob(t *testing.T) {
	testHarness := newHarness(t)
	jobID := testHarness.insertDownloadJob(testHarness.userID, printablesURL, "done", "datetime('now')")

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/download/%d", jobID), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("cancelling a finished job answered %d", answer.status)
	}
	if testHarness.count("SELECT COUNT(*) FROM download_queue WHERE id = ?", jobID) != 1 {
		t.Fatal("the finished job was deleted")
	}
}

func TestDownloadCancelRejectsAForeignJob(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDownloadJob(testHarness.adminID, printablesURL, "pending", "")

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/download/%d", foreign), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign job answered %d", answer.status)
	}
	if testHarness.count("SELECT COUNT(*) FROM download_queue WHERE id = ?", foreign) != 1 {
		t.Fatal("a foreign job was cancelled")
	}
}

func TestDownloadDismissRemovesOnlyFailedJobs(t *testing.T) {
	testHarness := newHarness(t)
	failed := testHarness.insertDownloadJob(testHarness.userID, printablesURL, "failed", "datetime('now')")
	pending := testHarness.insertDownloadJob(testHarness.userID, printablesURL, "pending", "")
	foreign := testHarness.insertDownloadJob(testHarness.adminID, printablesURL, "failed", "datetime('now')")

	testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/download/%d/dismiss", failed), nil)
	testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/download/%d/dismiss", pending), nil)
	testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/download/%d/dismiss", foreign), nil)

	if testHarness.count("SELECT COUNT(*) FROM download_queue WHERE id = ?", failed) != 0 {
		t.Fatal("the failed job survived the dismissal")
	}
	if testHarness.count("SELECT COUNT(*) FROM download_queue WHERE id = ?", pending) != 1 {
		t.Fatal("a pending job was dismissed")
	}
	if testHarness.count("SELECT COUNT(*) FROM download_queue WHERE id = ?", foreign) != 1 {
		t.Fatal("a foreign job was dismissed")
	}
}

func TestDownloadEndpointsWithANonNumericIDAreNotFound(t *testing.T) {
	testHarness := newHarness(t)

	calls := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/download/abc"},
		{http.MethodPost, "/api/v1/download/abc/retry"},
		{http.MethodDelete, "/api/v1/download/abc"},
		{http.MethodDelete, "/api/v1/download/abc/dismiss"},
	}

	for _, call := range calls {
		answer := testHarness.asUser(call.method, call.path, nil)
		if answer.status != http.StatusNotFound {
			t.Fatalf("%s %s answered %d", call.method, call.path, answer.status)
		}
	}
}

func TestSyncAllQueuesEverySyncableDesign(t *testing.T) {
	testHarness := newHarness(t)
	withSource := testHarness.insertDesign(testHarness.userID, "Mit Quelle")
	testHarness.setDesignFields(withSource, map[string]any{"source_url": printablesURL})
	testHarness.insertDesign(testHarness.userID, "Ohne Quelle")
	blank := testHarness.insertDesign(testHarness.userID, "Leere Quelle")
	testHarness.setDesignFields(blank, map[string]any{"source_url": ""})
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")
	testHarness.setDesignFields(foreign, map[string]any{"source_url": printablesURL})

	answer := testHarness.asUser(http.MethodPost, "/api/v1/designs/sync-all", nil)

	if queued := coerce.Int(answer.data(t)["queued"]); queued != 1 {
		t.Fatalf("%d designs were queued, expected the one with a source url", queued)
	}
	if testHarness.count("SELECT COUNT(*) FROM sync_queue WHERE design_id = ?", withSource) != 1 {
		t.Fatal("the syncable design was not queued")
	}
	if testHarness.count("SELECT COUNT(*) FROM sync_queue") != 1 {
		t.Fatal("a design without a source url or a foreign design was queued")
	}
}

func TestSyncAllSkipsDesignsThatAreAlreadyQueued(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Quelle")
	testHarness.setDesignFields(designID, map[string]any{"source_url": printablesURL})

	testHarness.asUser(http.MethodPost, "/api/v1/designs/sync-all", nil)
	// The run leaves a ten-minute cooldown behind; what is under test here is the
	// per-design guard, so the stamp is cleared instead of waited out.
	testHarness.clearUpdateAllCooldown(testHarness.userID)
	answer := testHarness.asUser(http.MethodPost, "/api/v1/designs/sync-all", nil)

	if queued := coerce.Int(answer.data(t)["queued"]); queued != 0 {
		t.Fatalf("the second run queued %d designs", queued)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM sync_queue WHERE design_id = ?", designID); count != 1 {
		t.Fatalf("%d jobs exist for the same design", count)
	}
}

// Every queued design is a full re-download, so a second run right after the
// first is refused - clicking it repeatedly is the burst that gets a platform
// account rate-limited.
func TestSyncAllRefusesASecondRunWithinTheCooldown(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Quelle")
	testHarness.setDesignFields(designID, map[string]any{"source_url": printablesURL})

	testHarness.asUser(http.MethodPost, "/api/v1/designs/sync-all", nil)
	answer := testHarness.asUser(http.MethodPost, "/api/v1/designs/sync-all", nil)

	if answer.status != http.StatusTooManyRequests {
		t.Fatalf("the second run answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.sync_cooldown" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// The cooldown is stamped even when nothing was queued: a library that is
// already fully queued would otherwise reopen the button immediately.
func TestSyncAllStartsTheCooldownWithoutQueueingAnything(t *testing.T) {
	testHarness := newHarness(t)

	testHarness.asUser(http.MethodPost, "/api/v1/designs/sync-all", nil)

	state := testHarness.asUser(http.MethodGet, "/api/v1/users/"+testHarness.publicID(testHarness.userID)+"/sync-state", nil).data(t)
	if seconds := coerce.Int(state["update_all_cooldown_seconds"]); seconds <= 0 {
		t.Fatalf("an empty run left no cooldown behind: %v", seconds)
	}
}

// The buttons in the account settings read their state from here; without the
// queue depth a second run could be started while the first is still being
// worked off.
func TestSyncStateReportsAccountsCooldownsAndQueue(t *testing.T) {
	testHarness := newHarness(t)
	path := "/api/v1/users/" + testHarness.publicID(testHarness.userID) + "/sync-state"

	fresh := testHarness.asUser(http.MethodGet, path, nil).data(t)
	if fresh["has_accounts"] != false || fresh["queue_busy"] != false {
		t.Fatalf("a fresh account reports %v", fresh)
	}
	if coerce.Int(fresh["library_cooldown_seconds"]) != 0 || coerce.Int(fresh["update_all_cooldown_seconds"]) != 0 {
		t.Fatalf("a fresh account is already on cooldown: %v", fresh)
	}

	testHarness.insertPlatformAccount(testHarness.userID, "printables", "", "member@example.org")
	designID := testHarness.insertDesign(testHarness.userID, "Mit Quelle")
	testHarness.insertSyncJob(testHarness.userID, designID, "pending", "")
	testHarness.insertDownloadJob(testHarness.userID, printablesURL, "pending", "")

	busy := testHarness.asUser(http.MethodGet, path, nil).data(t)
	if busy["has_accounts"] != true || busy["queue_busy"] != true {
		t.Fatalf("a busy account reports %v", busy)
	}
	if coerce.Int(busy["downloads_pending"]) != 1 || coerce.Int(busy["syncs_pending"]) != 1 {
		t.Fatalf("the queue depth is %v", busy)
	}
}

// The state belongs to the account it describes.
func TestSyncStateRefusesAForeignAccount(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodGet, "/api/v1/users/"+testHarness.publicID(testHarness.adminID)+"/sync-state", nil)

	if answer.status != http.StatusForbidden && answer.status != http.StatusNotFound {
		t.Fatalf("a foreign sync state answered %d: %s", answer.status, answer.rawBody)
	}
}

func TestSyncStatusShowsRunningAndRecentJobsOnly(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Quelle")
	pending := testHarness.insertSyncJob(testHarness.userID, designID, "pending", "")
	freshlyDone := testHarness.insertSyncJob(testHarness.userID, designID, "done", "datetime('now', '-5 seconds')")
	testHarness.insertSyncJob(testHarness.userID, designID, "done", "datetime('now', '-5 minutes')")
	foreignDesign := testHarness.insertDesign(testHarness.adminID, "Fremd")
	testHarness.insertSyncJob(testHarness.adminID, foreignDesign, "pending", "")

	jobs := testHarness.asUser(http.MethodGet, "/api/v1/designs/sync-status", nil).list(t)

	found := map[int]bool{}
	for _, job := range jobs {
		entry := job.(map[string]any)
		found[coerce.Int(entry["id"])] = true
		if entry["design_name"] != "Mit Quelle" {
			t.Fatalf("the design name is missing: %v", entry["design_name"])
		}
	}
	if len(jobs) != 2 || !found[pending] || !found[freshlyDone] {
		t.Fatalf("%d jobs were listed: %v", len(jobs), found)
	}
}
