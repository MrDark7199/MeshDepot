package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"meshdepot/internal/coerce"
	"meshdepot/internal/health"
	"meshdepot/internal/scheduler"
)

// soleAdmin removes every admin except the given one; the schema seeds a
// bootstrap admin, so without this there are always two.
func (testHarness *harness) soleAdmin(keepID int) {
	testHarness.t.Helper()
	if _, failure := testHarness.database.Exec(
		"UPDATE users SET admin = 0 WHERE admin = 1 AND id != ?", keepID); failure != nil {
		testHarness.t.Fatalf("demote the other admins: %v", failure)
	}
}

func TestAdminListReturnsEveryUserWithStatistics(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	testHarness.insertFileVersion(designID, 4096, "wuerfel.stl")

	answer := testHarness.asAdmin(http.MethodGet, "/api/v1/admin/users", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the list answered %d: %s", answer.status, answer.rawBody)
	}
	users := answer.list(t)
	// The bootstrap admin plus the two accounts of the harness.
	if len(users) != 3 {
		t.Fatalf("%d users were listed", len(users))
	}
	var found bool
	for _, entry := range users {
		row := entry.(map[string]any)
		// Accounts are addressed by their public id.
		if row["id"] != testHarness.publicID(testHarness.userID) {
			continue
		}
		found = true
		if coerce.Int(row["design_count"]) != 1 || coerce.Int(row["used_bytes"]) != 4096 {
			t.Fatalf("the statistics are %v / %v", row["design_count"], row["used_bytes"])
		}
	}
	if !found {
		t.Fatal("the member is missing from the list")
	}
}

func TestAdminRoutesAreClosedToOrdinaryUsers(t *testing.T) {
	testHarness := newHarness(t)

	calls := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodPost, "/api/v1/admin/users"},
		{http.MethodPut, "/api/v1/admin/users/1"},
		{http.MethodDelete, "/api/v1/admin/users/1"},
		{http.MethodPost, "/api/v1/admin/users/1/reset-password"},
		{http.MethodGet, "/api/v1/admin/users/1/detail"},
		{http.MethodGet, "/api/v1/admin/stats"},
		{http.MethodGet, "/api/v1/admin/health"},
		{http.MethodPost, "/api/v1/admin/translations/backfill"},
		{http.MethodGet, "/api/v1/admin/settings"},
		{http.MethodPut, "/api/v1/admin/settings"},
		{http.MethodPost, "/api/v1/admin/library-sync/run"},
	}

	for _, call := range calls {
		answer := testHarness.asUser(call.method, call.path, nil)
		if answer.status != http.StatusForbidden {
			t.Fatalf("%s %s answered %d", call.method, call.path, answer.status)
		}
		if key := answer.errorKey(t); key != "error.forbidden" {
			t.Fatalf("%s %s produced the error key %q", call.method, call.path, key)
		}
	}
}

func TestAdminRoutesAreClosedWithoutASession(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.anonymous(http.MethodGet, "/api/v1/admin/users", nil)

	if answer.status != http.StatusUnauthorized {
		t.Fatalf("an anonymous call answered %d", answer.status)
	}
}

func TestAdminCreateStoresAUsableAccount(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"name": "Neuling", "email": "neuling@example.org", "password": testPassword,
	})

	if answer.status != http.StatusOK {
		t.Fatalf("the creation answered %d: %s", answer.status, answer.rawBody)
	}
	created := answer.data(t)
	if created["name"] != "Neuling" || created["email"] != "neuling@example.org" {
		t.Fatalf("the account carries %v", created)
	}
	if coerce.Int(created["admin"]) != 0 || coerce.Int(created["must_change_password"]) != 0 {
		t.Fatalf("the flags are %v / %v", created["admin"], created["must_change_password"])
	}
	// The password has to be usable, not merely stored.
	if token := testHarness.login("neuling@example.org"); token == "" {
		t.Fatal("the new account cannot log in")
	}
}

func TestAdminCreateTakesTheFlags(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"name": "Zweiter Admin", "password": testPassword, "admin": 1, "must_change_password": 1,
	})

	created := answer.data(t)
	if coerce.Int(created["admin"]) != 1 || coerce.Int(created["must_change_password"]) != 1 {
		t.Fatalf("the flags are %v / %v", created["admin"], created["must_change_password"])
	}
	// The column stays NULL rather than empty, which a UNIQUE index would collide on.
	if count := testHarness.count("SELECT COUNT(*) FROM users WHERE public_id = ? AND email IS NULL", created["id"]); count != 1 {
		t.Fatal("the empty email was not stored as NULL")
	}
}

func TestAdminCreateNeedsNameAndPassword(t *testing.T) {
	testHarness := newHarness(t)

	bodies := []map[string]any{
		{"password": testPassword},
		{"name": "Ohne Passwort"},
		{"name": "  ", "password": testPassword},
	}

	for _, body := range bodies {
		answer := testHarness.asAdmin(http.MethodPost, "/api/v1/admin/users", body)
		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("%v answered %d", body, answer.status)
		}
		if key := answer.errorKey(t); key != "error.credentials_required" {
			t.Fatalf("%v produced the error key %q", body, key)
		}
	}
}

func TestAdminCreateRefusesAShortPassword(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"name": "Neuling", "password": "kurz",
	})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a short password answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.password_too_short" {
		t.Fatalf("unexpected error key %q", key)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM users WHERE name = 'Neuling'"); count != 0 {
		t.Fatal("the account was created anyway")
	}
}

func TestAdminUpdateChangesOnlyTheSubmittedFields(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%s", testHarness.publicID(testHarness.userID)),
		map[string]any{"name": "  Umbenannt  ", "must_change_password": 1})

	if answer.status != http.StatusOK {
		t.Fatalf("the update answered %d: %s", answer.status, answer.rawBody)
	}
	updated := answer.data(t)
	if updated["name"] != "Umbenannt" {
		t.Fatalf("the name is %v", updated["name"])
	}
	if coerce.Int(updated["must_change_password"]) != 1 {
		t.Fatalf("the flag is %v", updated["must_change_password"])
	}
	// The email was not part of the request and must survive untouched.
	if updated["email"] != "user@example.org" {
		t.Fatalf("the email is %v", updated["email"])
	}
}

func TestAdminUpdateClearsTheEmailAsNull(t *testing.T) {
	testHarness := newHarness(t)

	testHarness.asAdmin(http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%s", testHarness.publicID(testHarness.userID)),
		map[string]any{"email": "   "})

	if count := testHarness.count("SELECT COUNT(*) FROM users WHERE id = ? AND email IS NULL", testHarness.userID); count != 1 {
		t.Fatal("the cleared email was not stored as NULL")
	}
}

// Deactivating has to cut the live sessions, or the cookie keeps working.
func TestAdminUpdateEndsTheSessionsOfADeactivatedUser(t *testing.T) {
	testHarness := newHarness(t)

	testHarness.asAdmin(http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%s", testHarness.publicID(testHarness.userID)),
		map[string]any{"state": "inactive"})

	if state := testHarness.scalar("SELECT state FROM users WHERE id = ?", testHarness.userID); state != "inactive" {
		t.Fatalf("the state is %q", state)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM sessions WHERE user_id = ?", testHarness.userID); count != 0 {
		t.Fatal("the sessions survived the deactivation")
	}
	answer := testHarness.asUser(http.MethodGet, "/api/v1/designs", nil)
	if answer.status != http.StatusUnauthorized {
		t.Fatalf("the deactivated user still answered %d", answer.status)
	}
}

func TestAdminUpdateKeepsTheLastAdmin(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.soleAdmin(testHarness.adminID)

	for _, body := range []map[string]any{{"admin": 0}, {"state": "inactive"}} {
		answer := testHarness.asAdmin(http.MethodPut,
			fmt.Sprintf("/api/v1/admin/users/%s", testHarness.publicID(testHarness.adminID)), body)
		if answer.status != http.StatusConflict {
			t.Fatalf("%v answered %d", body, answer.status)
		}
		if key := answer.errorKey(t); key != "error.last_admin" {
			t.Fatalf("%v produced the error key %q", body, key)
		}
	}
	if flag := testHarness.scalarInt("SELECT admin FROM users WHERE id = ?", testHarness.adminID); flag != 1 {
		t.Fatal("the last admin lost the role anyway")
	}
	if state := testHarness.scalar("SELECT state FROM users WHERE id = ?", testHarness.adminID); state != "active" {
		t.Fatalf("the last admin is %q", state)
	}
}

func TestAdminUpdateDemotesAnAdminWhenAnotherOneRemains(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodPut,
		fmt.Sprintf("/api/v1/admin/users/%s", testHarness.publicID(testHarness.adminID)), map[string]any{"admin": 0})

	if answer.status != http.StatusOK {
		t.Fatalf("the demotion answered %d: %s", answer.status, answer.rawBody)
	}
	if flag := testHarness.scalarInt("SELECT admin FROM users WHERE id = ?", testHarness.adminID); flag != 0 {
		t.Fatal("the role was kept")
	}
}

func TestAdminDeleteRemovesTheAccountAndItsSessions(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%s", testHarness.publicID(testHarness.userID)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM users WHERE id = ?", testHarness.userID); count != 0 {
		t.Fatal("the account survived")
	}
	if count := testHarness.count("SELECT COUNT(*) FROM sessions WHERE user_id = ?", testHarness.userID); count != 0 {
		t.Fatal("the sessions survived")
	}
}

func TestAdminDeleteKeepsTheLastAdmin(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.soleAdmin(testHarness.adminID)

	answer := testHarness.asAdmin(http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%s", testHarness.publicID(testHarness.adminID)), nil)

	if answer.status != http.StatusConflict {
		t.Fatalf("the deletion answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.last_admin" {
		t.Fatalf("unexpected error key %q", key)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM users WHERE id = ?", testHarness.adminID); count != 1 {
		t.Fatal("the last admin was deleted anyway")
	}
}

func TestAdminResetPasswordForcesAChangeAndEndsTheSessions(t *testing.T) {
	testHarness := newHarness(t)
	oldHash := testHarness.scalar("SELECT hash FROM users WHERE id = ?", testHarness.userID)

	answer := testHarness.asAdmin(http.MethodPost,
		fmt.Sprintf("/api/v1/admin/users/%s/reset-password", testHarness.publicID(testHarness.userID)),
		map[string]any{"new_password": "ein ganz neues passwort"})

	if answer.status != http.StatusOK {
		t.Fatalf("the reset answered %d: %s", answer.status, answer.rawBody)
	}
	if newHash := testHarness.scalar("SELECT hash FROM users WHERE id = ?", testHarness.userID); newHash == oldHash {
		t.Fatal("the hash was not replaced")
	}
	if flag := testHarness.scalarInt("SELECT must_change_password FROM users WHERE id = ?", testHarness.userID); flag != 1 {
		t.Fatal("the change was not enforced")
	}
	// The account stays active, so a session that still worked would be the reset's
	// own doing.
	if used := testHarness.asUser(http.MethodGet, "/api/v1/designs", nil); used.status != http.StatusUnauthorized {
		t.Fatalf("the old session still answered %d", used.status)
	}
}

func TestAdminResetPasswordChecksTheNewPassword(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/admin/users/%s/reset-password", testHarness.publicID(testHarness.userID))

	empty := testHarness.asAdmin(http.MethodPost, path, map[string]any{"new_password": ""})
	if key := empty.errorKey(t); empty.status != http.StatusUnprocessableEntity || key != "error.credentials_required" {
		t.Fatalf("an empty password answered %d / %q", empty.status, key)
	}

	short := testHarness.asAdmin(http.MethodPost, path, map[string]any{"new_password": "kurz"})
	if key := short.errorKey(t); short.status != http.StatusUnprocessableEntity || key != "error.password_too_short" {
		t.Fatalf("a short password answered %d / %q", short.status, key)
	}
}

func TestAdminDetailReturnsTheAccount(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%s/detail", testHarness.publicID(testHarness.userID)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the detail answered %d: %s", answer.status, answer.rawBody)
	}
	detail := answer.data(t)
	if detail["email"] != "user@example.org" || detail["name"] != "member" {
		t.Fatalf("the account carries %v", detail)
	}
}

func TestAdminDetailIsNotFoundForAnUnknownAccount(t *testing.T) {
	testHarness := newHarness(t)

	unknown := testHarness.asAdmin(http.MethodGet, "/api/v1/admin/users/999999/detail", nil)
	if unknown.status != http.StatusNotFound {
		t.Fatalf("an unknown account answered %d", unknown.status)
	}

	notANumber := testHarness.asAdmin(http.MethodGet, "/api/v1/admin/users/abc/detail", nil)
	if notANumber.status != http.StatusNotFound {
		t.Fatalf("a non-numeric id answered %d", notANumber.status)
	}
}

func TestAdminUserRoutesWithNonNumericIDsAreNotFound(t *testing.T) {
	testHarness := newHarness(t)

	calls := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/v1/admin/users/abc"},
		{http.MethodDelete, "/api/v1/admin/users/abc"},
		{http.MethodPost, "/api/v1/admin/users/abc/reset-password"},
	}

	for _, call := range calls {
		answer := testHarness.asAdmin(call.method, call.path, nil)
		if answer.status != http.StatusNotFound {
			t.Fatalf("%s %s answered %d", call.method, call.path, answer.status)
		}
	}
}

func TestAdminStatsCountsTheWholeInstallation(t *testing.T) {
	testHarness := newHarness(t)
	ownDesign := testHarness.insertDesign(testHarness.userID, "Eigenes")
	testHarness.insertFileVersion(ownDesign, 2048, "wuerfel.stl")
	foreignDesign := testHarness.insertDesign(testHarness.adminID, "Fremdes")
	testHarness.insertFileVersion(foreignDesign, 1024, "kugel.stl")
	testHarness.setDesignFields(ownDesign, map[string]any{"source_url": "https://example.org/modell", "source_platform": "printables"})
	testHarness.insertTag(testHarness.userID, "technik", "manual")
	testHarness.insertCollection(testHarness.userID, "Sammlung")

	answer := testHarness.asAdmin(http.MethodGet, "/api/v1/admin/stats", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the statistics answered %d: %s", answer.status, answer.rawBody)
	}
	stats := answer.data(t)
	if coerce.Int(stats["design_count"]) != 2 || coerce.Int(stats["file_count"]) != 2 {
		t.Fatalf("the counts are %v / %v", stats["design_count"], stats["file_count"])
	}
	if coerce.Int(stats["total_bytes"]) != 3072 {
		t.Fatalf("the total size is %v", stats["total_bytes"])
	}
	if coerce.Int(stats["tag_count"]) != 1 || coerce.Int(stats["collection_count"]) != 1 {
		t.Fatalf("the counts are %v / %v", stats["tag_count"], stats["collection_count"])
	}
	if coerce.Int(stats["synced_count"]) != 1 {
		t.Fatalf("the synced count is %v", stats["synced_count"])
	}
	// The bootstrap admin plus the two harness accounts, all active.
	if coerce.Int(stats["user_count"]) != 3 || coerce.Int(stats["active_users"]) != 3 {
		t.Fatalf("the user counts are %v / %v", stats["user_count"], stats["active_users"])
	}
	if len(stats["platforms"].([]any)) == 0 {
		t.Fatal("the platform breakdown is empty")
	}
	if stats["newest_design_at"] == nil {
		t.Fatal("the newest design is missing")
	}
}

func TestAdminStatsListsOnlyActiveUsersPerUser(t *testing.T) {
	testHarness := newHarness(t)
	if _, failure := testHarness.database.Exec(
		"UPDATE users SET state = 'inactive' WHERE id = ?", testHarness.userID); failure != nil {
		t.Fatalf("deactivate the member: %v", failure)
	}

	stats := testHarness.asAdmin(http.MethodGet, "/api/v1/admin/stats", nil).data(t)

	for _, entry := range stats["per_user"].([]any) {
		if coerce.Int(entry.(map[string]any)["id"]) == testHarness.userID {
			t.Fatal("the deactivated user is in the breakdown")
		}
	}
	if coerce.Int(stats["active_users"]) != 2 {
		t.Fatalf("%v users count as active", stats["active_users"])
	}
}

func TestAdminHealthReportsEverySubsystem(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodGet, "/api/v1/admin/health", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the health check answered %d: %s", answer.status, answer.rawBody)
	}
	checks := answer.data(t)["checks"].(map[string]any)
	for _, name := range []string{"storage", "download_worker", "sync_worker", "scheduler", "tor", "browser"} {
		check, present := checks[name].(map[string]any)
		if !present {
			t.Fatalf("the check %q is missing", name)
		}
		switch check["status"] {
		case "ok", "warn", "error", "off":
		default:
			t.Fatalf("the check %q reports the status %v", name, check["status"])
		}
		if check["label_key"] == "" || check["label_key"] == nil {
			t.Fatalf("the check %q has no label", name)
		}
	}
	// The database has no tile: this response is only reached through a session
	// looked up in that very database.
	if _, present := checks["database"]; present {
		t.Fatal("the database check is back")
	}
}

func TestAdminHealthWarnsAboutFailedDownloads(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertDownloadJob(testHarness.userID, "https://example.org/eins", "failed", "datetime('now')")
	testHarness.insertDownloadJob(testHarness.userID, "https://example.org/zwei", "pending", "")

	checks := testHarness.asAdmin(http.MethodGet, "/api/v1/admin/health", nil).data(t)["checks"].(map[string]any)

	download := checks["download_worker"].(map[string]any)
	if download["status"] != "warn" {
		t.Fatalf("the download check reports %v", download["status"])
	}
	counters := download["vars"].(map[string]any)
	if coerce.Int(counters["pending"]) != 1 || coerce.Int(counters["failed"]) != 1 {
		t.Fatalf("the queue depth is %v", counters)
	}
}

func healthCheck(t *testing.T, testHarness *harness, name string) map[string]any {
	t.Helper()
	checks := testHarness.asAdmin(http.MethodGet, "/api/v1/admin/health", nil).data(t)["checks"].(map[string]any)
	check, present := checks[name].(map[string]any)
	if !present {
		t.Fatalf("the check %q is missing", name)
	}
	return check
}

func TestAdminHealthReportsTheLoopHeartbeat(t *testing.T) {
	testHarness := newHarness(t)

	scheduler := healthCheck(t, testHarness, "scheduler")

	if scheduler["status"] != "ok" || scheduler["message_key"] != "health_scheduler_ok" {
		t.Fatalf("a running scheduler reports %v", scheduler)
	}
	if age := scheduler["vars"].(map[string]any)["age"]; age != "0s" {
		t.Fatalf("the age of a fresh heartbeat is %v", age)
	}
}

// A ticking loop with the sync switched off is not a healthy one, and reporting
// it in green with a sentence underneath was read as "all good".
func TestAdminHealthReportsASwitchedOffScheduler(t *testing.T) {
	for _, testCase := range []struct {
		setting string
		message string
	}{
		{"library_sync_enabled", "health_scheduler_library_off"},
		{"design_update_enabled", "health_scheduler_updates_off"},
	} {
		t.Run(testCase.setting, func(t *testing.T) {
			testHarness := newHarness(t)
			testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{testCase.setting: 0})

			check := healthCheck(t, testHarness, "scheduler")

			if check["status"] != "off" || check["message_key"] != testCase.message {
				t.Fatalf("a scheduler with %s off reports %v", testCase.setting, check)
			}
		})
	}
}

// The case the old check could not see: the loop is gone, the queue stands still,
// and nothing in the database shows it.
func TestAdminHealthReportsAStoppedLoop(t *testing.T) {
	testHarness := newHarness(t)
	// Everything registered, but the last tick was minutes ago.
	testHarness.server.Health.Now = func() time.Time { return time.Now().Add(31 * time.Minute) }

	for _, name := range []string{"download_worker", "sync_worker", "scheduler"} {
		check := healthCheck(t, testHarness, name)
		if check["status"] != "error" || check["message_key"] != "health_loop_stale" {
			t.Fatalf("the silent loop %q reports %v", name, check)
		}
	}
}

func TestAdminHealthReportsALoopThatNeverStarted(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.server.Health = health.New()

	check := healthCheck(t, testHarness, "download_worker")

	if check["status"] != "error" || check["message_key"] != "health_loop_down" {
		t.Fatalf("a loop that was never started reports %v", check)
	}
}

func TestAdminHealthReportsABusyWorkerAsRunning(t *testing.T) {
	testHarness := newHarness(t)
	endJob := testHarness.server.Health.Working(health.DownloadWorker)
	defer endJob()
	testHarness.server.Health.Now = func() time.Time { return time.Now().Add(9 * time.Minute) }

	check := healthCheck(t, testHarness, "download_worker")

	if check["status"] != "ok" || check["message_key"] != "health_download_busy" {
		t.Fatalf("a running download reports %v", check)
	}
	if age := check["vars"].(map[string]any)["age"]; age != "9m 0s" {
		t.Fatalf("the runtime of the job is %v", age)
	}
}

func TestAdminHealthWarnsAboutASyncBacklog(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Rückstau")
	for count := 0; count < syncBacklogWarn; count++ {
		if _, failure := testHarness.database.Exec(
			"INSERT INTO sync_queue (design_id, user_id, status) VALUES (?, ?, 'pending')", designID, testHarness.userID,
		); failure != nil {
			t.Fatalf("fill the queue: %v", failure)
		}
	}

	check := healthCheck(t, testHarness, "sync_worker")

	if check["status"] != "warn" || check["message_key"] != "health_sync_backlog" {
		t.Fatalf("a backed-up queue reports %v", check)
	}
}

// The file check alone said nothing: a Chromium that does not start leaves its
// binary exactly where it was.
func TestAdminHealthStartsChromiumForTheBrowserCheck(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.server.Cfg.ChromiumBin = fakeBinary(t, `echo "Chromium 120.0.6099.109"`)

	check := healthCheck(t, testHarness, "browser")

	if check["status"] != "ok" || check["message_key"] != "health_browser_ok" {
		t.Fatalf("a working Chromium reports %v", check)
	}
	if version := check["vars"].(map[string]any)["version"]; version != "Chromium 120.0.6099.109" {
		t.Fatalf("the version reads %v", version)
	}
}

func TestAdminHealthReportsAChromiumThatDoesNotStart(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.server.Cfg.ChromiumBin = fakeBinary(t, `echo "error while loading shared libraries: libnss3.so" >&2; exit 127`)

	check := healthCheck(t, testHarness, "browser")

	if check["status"] != "error" || check["message_key"] != "health_browser_error" {
		t.Fatalf("a broken Chromium reports %v", check)
	}
	if detail, _ := check["vars"].(map[string]any)["detail"].(string); !strings.Contains(detail, "libnss3.so") {
		t.Fatalf("the reason is missing: %v", check["vars"])
	}
}

func TestAdminHealthReportsAMissingChromium(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.server.Cfg.ChromiumBin = filepath.Join(t.TempDir(), "no-chromium")

	check := healthCheck(t, testHarness, "browser")

	if check["status"] != "error" || check["message_key"] != "health_browser_missing" {
		t.Fatalf("a missing Chromium reports %v", check)
	}
}

// fakeBinary writes a stub - the real Chromium is not startable in a test.
func fakeBinary(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chromium")
	if failure := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); failure != nil {
		t.Fatalf("write stub: %v", failure)
	}
	return path
}

// Health tiles carry i18n keys instead of text, so a typo does not fail
// anywhere - it shows up on the admin page as the literal "health_loop_stale".
// The same pin as httpx.TestErrorKeysAreTranslated, for the other key family.
func TestHealthKeysAreTranslated(t *testing.T) {
	emitted := regexp.MustCompile(`"(health_[a-z0-9_]+)"`)
	defined := regexp.MustCompile(`(?m)^\s*(health_[a-z0-9_]+)\s*:`)

	source, failure := os.ReadFile("admin.go")
	if failure != nil {
		t.Fatalf("read admin.go: %v", failure)
	}
	var keys []string
	for _, match := range emitted.FindAllStringSubmatch(string(source), -1) {
		keys = append(keys, match[1])
	}
	if len(keys) < 10 {
		t.Fatalf("only %d health keys found - has the handler moved?", len(keys))
	}

	for _, language := range []string{"en", "de"} {
		path := filepath.Join("..", "..", "..", "frontend", "src", "i18n", language+".ts")
		dictionary, failure := os.ReadFile(path)
		if failure != nil {
			t.Skipf("dictionary %s unreadable (%v) - needs a full checkout", path, failure)
		}
		known := map[string]bool{}
		for _, match := range defined.FindAllStringSubmatch(string(dictionary), -1) {
			known[match[1]] = true
		}
		for _, key := range keys {
			if !known[key] {
				t.Errorf("%s.ts has no entry for %q", language, key)
			}
		}
	}
}

func TestFmtAge(t *testing.T) {
	for _, testCase := range []struct {
		age    time.Duration
		expect string
	}{
		{-time.Second, "0s"},
		{4 * time.Second, "4s"},
		{3*time.Minute + 12*time.Second, "3m 12s"},
		{62 * time.Minute, "1h 2m"},
	} {
		if formatted := fmtAge(testCase.age); formatted != testCase.expect {
			t.Fatalf("fmtAge(%s) = %q, expected %q", testCase.age, formatted, testCase.expect)
		}
	}
}

func TestSettingsRoundTripThroughTheWhitelist(t *testing.T) {
	testHarness := newHarness(t)

	saved := testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{
		"library_sync_hour":         5,
		"download_cooldown_default": 120,
		"unbekannt":                 "wird ignoriert",
	})

	if saved.status != http.StatusOK {
		t.Fatalf("saving answered %d: %s", saved.status, saved.rawBody)
	}
	written := saved.data(t)
	if _, present := written["unbekannt"]; present {
		t.Fatal("an unknown key was written")
	}
	if coerce.Int(written["library_sync_hour"]) != 5 {
		t.Fatalf("the hour is %v", written["library_sync_hour"])
	}
	if count := testHarness.count("SELECT COUNT(*) FROM app_settings WHERE key = 'unbekannt'"); count != 0 {
		t.Fatal("the unknown key reached the database")
	}

	loaded := testHarness.asAdmin(http.MethodGet, "/api/v1/admin/settings", nil).data(t)
	if coerce.Int(loaded["library_sync_hour"]) != 5 || coerce.Int(loaded["download_cooldown_default"]) != 120 {
		t.Fatalf("the settings came back as %v", loaded)
	}
	// A known key is typed as a number, not as the string the column holds.
	if _, isNumber := loaded["library_sync_hour"].(float64); !isNumber {
		t.Fatalf("the hour came back as %T", loaded["library_sync_hour"])
	}
}

func TestSettingsClampValuesToTheirBounds(t *testing.T) {
	testHarness := newHarness(t)

	saved := testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{
		"library_sync_hour":    99,
		"library_sync_enabled": -3,
		"translation_enabled":  7,
	}).data(t)

	if coerce.Int(saved["library_sync_hour"]) != 23 {
		t.Fatalf("the hour was not clamped: %v", saved["library_sync_hour"])
	}
	if coerce.Int(saved["library_sync_enabled"]) != 0 {
		t.Fatalf("the switch was not clamped: %v", saved["library_sync_enabled"])
	}
	if coerce.Int(saved["translation_enabled"]) != 1 {
		t.Fatalf("the switch was not clamped: %v", saved["translation_enabled"])
	}
}

// Cooldowns are rejected instead of clamped: a value the admin never chose
// would silently replace the one they typed, and the floor exists precisely
// because a shorter pause gets the account rate-limited.
func TestSettingsRejectCooldownsBelowTheMinimum(t *testing.T) {
	for _, testCase := range []struct {
		key      string
		value    int
		expected string
	}{
		{"download_cooldown_printables", 29, "error.cooldown_too_low"},
		{"download_cooldown_default", 0, "error.cooldown_too_low"},
		{"download_cooldown_makerworld", 199, "error.cooldown_makerworld_too_low"},
	} {
		testHarness := newHarness(t)
		before := testHarness.scalar("SELECT value FROM app_settings WHERE key = '" + testCase.key + "'")

		answer := testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{testCase.key: testCase.value})

		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d answered %d: %s", testCase.key, testCase.value, answer.status, answer.rawBody)
		}
		if !strings.Contains(answer.rawBody, testCase.expected) {
			t.Fatalf("%s = %d answered %q, expected %q", testCase.key, testCase.value, answer.rawBody, testCase.expected)
		}
		if after := testHarness.scalar("SELECT value FROM app_settings WHERE key = '" + testCase.key + "'"); after != before {
			t.Fatalf("%s was written anyway: %q -> %q", testCase.key, before, after)
		}
	}
}

// A rejected value must not let the rest of the form through: the admin would
// see an error and half-saved settings.
func TestSettingsRejectTheWholeBodyOnOneBadValue(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{
		"library_sync_hour":            7,
		"download_cooldown_printables": 5,
	})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("the save answered %d: %s", answer.status, answer.rawBody)
	}
	if hour := testHarness.scalar("SELECT value FROM app_settings WHERE key = 'library_sync_hour'"); hour == "7" {
		t.Fatal("the sync hour was written although the body was rejected")
	}
}

// The exact minimum is still allowed - the bound is inclusive.
func TestSettingsAcceptTheMinimumCooldown(t *testing.T) {
	testHarness := newHarness(t)

	saved := testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{
		"download_cooldown_printables": 30,
		"download_cooldown_makerworld": 200,
	})

	if saved.status != http.StatusOK {
		t.Fatalf("the minimum was rejected: %d %s", saved.status, saved.rawBody)
	}
}

// The public settings are readable for every logged-in user, but must not carry
// anything beyond the sync hour and the server switch.
func TestPublicSettingsAreOpenToOrdinaryUsers(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{
		"library_sync_hour": 4, "download_cooldown_default": 90,
	})

	answer := testHarness.asUser(http.MethodGet, "/api/v1/settings/public", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the public settings answered %d: %s", answer.status, answer.rawBody)
	}
	settings := answer.data(t)
	if coerce.Int(settings["library_sync_hour"]) != 4 {
		t.Fatalf("the hour is %v", settings["library_sync_hour"])
	}
	if coerce.Int(settings["library_sync_enabled"]) != 1 {
		t.Fatalf("the switch is %v", settings["library_sync_enabled"])
	}
	if coerce.Int(settings["design_update_enabled"]) != 1 {
		t.Fatalf("the update switch is %v", settings["design_update_enabled"])
	}
	if coerce.Int(settings["design_update_min_days"]) != scheduler.DesignUpdateMinDays {
		t.Fatalf("the update interval is %v", settings["design_update_min_days"])
	}
	// Whether notification mail can be sent - the account page greys its e-mail
	// column out when it cannot. Only the fact travels, never the configuration.
	if settings["mail_enabled"] != false {
		t.Fatalf("mail is reported as %v on a server that has none configured", settings["mail_enabled"])
	}
	// Counted on purpose: this endpoint is open to every logged-in user, so a
	// setting added to app_settings must not appear here unnoticed.
	if len(settings) != 5 {
		t.Fatalf("the public settings carry %v", settings)
	}
}

// The user side needs the switch to hide its sync controls, so it has to travel
// with the public settings.
func TestPublicSettingsReportTheDisabledSwitch(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{"library_sync_enabled": 0})

	settings := testHarness.asUser(http.MethodGet, "/api/v1/settings/public", nil).data(t)

	if coerce.Int(settings["library_sync_enabled"]) != 0 {
		t.Fatalf("the switch is %v", settings["library_sync_enabled"])
	}
}

func TestRunLibrarySyncSetsTheForceFlag(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodPost, "/api/v1/admin/library-sync/run", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the run answered %d: %s", answer.status, answer.rawBody)
	}
	if value := testHarness.scalar("SELECT value FROM app_settings WHERE key = 'library_sync_force'"); value != "1" {
		t.Fatalf("the flag is %q", value)
	}

	// A second call must not create a second row - the scheduler reads exactly
	// one. The cooldown of the first call is aged out so the upsert is reached.
	testHarness.database.Exec("UPDATE app_settings SET value = '2000-01-01 00:00:00' WHERE key = 'library_sync_last_run'")
	testHarness.asAdmin(http.MethodPost, "/api/v1/admin/library-sync/run", nil)
	if count := testHarness.count("SELECT COUNT(*) FROM app_settings WHERE key = 'library_sync_force'"); count != 1 {
		t.Fatalf("%d rows carry the flag", count)
	}
}

// Setting the flag while the sync is off would fire the moment someone switches
// it back on - long after the admin pressed the button.
func TestRunLibrarySyncIsRefusedWhileTheSyncIsDisabled(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{"library_sync_enabled": 0})

	answer := testHarness.asAdmin(http.MethodPost, "/api/v1/admin/library-sync/run", nil)

	if answer.status != http.StatusForbidden {
		t.Fatalf("the run answered %d: %s", answer.status, answer.rawBody)
	}
	if value := testHarness.scalar("SELECT value FROM app_settings WHERE key = 'library_sync_force'"); value == "1" {
		t.Fatal("the force flag was set anyway")
	}
}

// With translation switched off the backfill reports that it is unconfigured
// instead of running into the live endpoint.
func TestTranslationBackfillReportsWhenItIsDisabled(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertDesign(testHarness.userID, "Unübersetzt")

	answer := testHarness.asAdmin(http.MethodPost, "/api/v1/admin/translations/backfill", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the backfill answered %d: %s", answer.status, answer.rawBody)
	}
	result := answer.data(t)
	if result["configured"] != false {
		t.Fatalf("the backfill reports %v", result)
	}
	if coerce.Int(result["processed"]) != 0 || coerce.Int(result["remaining"]) != 0 {
		t.Fatalf("the counters are %v", result)
	}
}

func TestFmtBytesPicksTheUnit(t *testing.T) {
	cases := []struct {
		bytes  int64
		expect string
	}{
		{0, "0 KB"},
		{2048, "2 KB"},
		{5 << 20, "5.0 MB"},
		{3 << 30, "3.0 GB"},
	}

	for _, testCase := range cases {
		if formatted := fmtBytes(testCase.bytes); formatted != testCase.expect {
			t.Fatalf("fmtBytes(%d) = %q, expected %q", testCase.bytes, formatted, testCase.expect)
		}
	}
}

// The admin run syncs every member's accounts at once, so it carries the same
// cooldown as the per-account buttons.
func TestRunLibrarySyncIsRefusedDuringTheCooldown(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.asAdmin(http.MethodPost, "/api/v1/admin/library-sync/run", nil)
	testHarness.database.Exec("UPDATE app_settings SET value = '0' WHERE key = 'library_sync_force'")

	answer := testHarness.asAdmin(http.MethodPost, "/api/v1/admin/library-sync/run", nil)

	if answer.status != http.StatusTooManyRequests {
		t.Fatalf("the second run answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.sync_cooldown" {
		t.Fatalf("the second run produced the error key %q", key)
	}
	if value := testHarness.scalar("SELECT value FROM app_settings WHERE key = 'library_sync_force'"); value == "1" {
		t.Fatal("the refused run set the force flag anyway")
	}
}

// The settings page disables its button from this value instead of offering a
// call that only comes back as 429.
func TestSettingsReportTheRemainingRunCooldown(t *testing.T) {
	testHarness := newHarness(t)

	if seconds := coerce.Int(testHarness.asAdmin(http.MethodGet, "/api/v1/admin/settings", nil).data(t)["library_sync_cooldown_seconds"]); seconds != 0 {
		t.Fatalf("an untouched installation reports %d seconds", seconds)
	}
	testHarness.asAdmin(http.MethodPost, "/api/v1/admin/library-sync/run", nil)

	seconds := coerce.Int(testHarness.asAdmin(http.MethodGet, "/api/v1/admin/settings", nil).data(t)["library_sync_cooldown_seconds"])

	if seconds <= 0 {
		t.Fatalf("after a run the cooldown reports %d seconds", seconds)
	}
}

// The interval is the floor for every member, so it must not be settable below
// the built-in minimum.
func TestSettingsRejectADesignUpdateIntervalBelowTheMinimum(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{
		"design_update_min_days": scheduler.DesignUpdateMinDays - 1,
	})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("the save answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.design_update_interval_too_low" {
		t.Fatalf("the save produced the error key %q", key)
	}
	if value := testHarness.scalar("SELECT value FROM app_settings WHERE key = 'design_update_min_days'"); value != "7" {
		t.Fatalf("the stored interval is %q", value)
	}
}
