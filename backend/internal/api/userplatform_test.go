package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"meshdepot/internal/platforms"
)

// stubDownloader stands in for a real platform downloader. Only the validator
// methods are ever called in these tests; Download exists to satisfy the
// interface the registry stores.
type stubDownloader struct {
	valid  bool
	reason string
	seen   platforms.Credentials
}

func (stub *stubDownloader) Download(string, platforms.Owner, func(step, label string, current, total int)) (platforms.Result, error) {
	return platforms.Result{}, nil
}

type stubValidator struct{ stubDownloader }

func (stub *stubValidator) Validate(credentials platforms.Credentials) bool {
	stub.seen = credentials
	return stub.valid
}

type stubReasonValidator struct{ stubDownloader }

func (stub *stubReasonValidator) ValidateReason(credentials platforms.Credentials) string {
	stub.seen = credentials
	return stub.reason
}

func platformAccountsPath(publicID string) string {
	return fmt.Sprintf("/api/v1/users/%s/platform-accounts", publicID)
}

// quietFlags switch every sync off, so saving credentials does not start a
// background library sync the test would have to wait for.
var quietFlags = map[string]any{"sync_likes": 0, "sync_collections": 0, "auto_library_sync": 0}

// saveAccount stores credentials for the logged-in member.
func (testHarness *harness) saveAccount(fields map[string]any) response {
	testHarness.t.Helper()
	body := map[string]any{}
	for key, value := range quietFlags {
		body[key] = value
	}
	for key, value := range fields {
		body[key] = value
	}
	return testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID)), body)
}

func TestPlatformAccountsIndexIsEmptyWithoutAccounts(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodGet, platformAccountsPath(testHarness.publicID(testHarness.userID)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the list answered %d: %s", answer.status, answer.rawBody)
	}
	if accounts := answer.list(t); len(accounts) != 0 {
		t.Fatalf("%d accounts were listed", len(accounts))
	}
}

// Credentials are encrypted at rest and only decrypted for their owner.
func TestPlatformAccountsSaveEncryptsTheCredentials(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.saveAccount(map[string]any{
		"platform": "thingiverse", "token": "geheimes-token", "username": "sammler", "password": "geheim",
	})

	if answer.status != http.StatusOK {
		t.Fatalf("saving answered %d: %s", answer.status, answer.rawBody)
	}
	view := answer.data(t)
	if view["token"] != "geheimes-token" || view["username"] != "sammler" {
		t.Fatalf("the account carries %v", view)
	}
	if view["has_password"] != true {
		t.Fatal("the password was not stored")
	}
	// The password itself never leaves the server.
	if _, present := view["password"]; present {
		t.Fatal("the password is part of the response")
	}
	stored := testHarness.scalar("SELECT token FROM platform_accounts WHERE user_id = ?", testHarness.userID)
	if stored == "" || stored == "geheimes-token" {
		t.Fatalf("the token is stored as %q", stored)
	}
}

func TestPlatformAccountsIndexReturnsTheFlagsAsBooleans(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "geheimes-token", "sync_likes": 1})

	accounts := testHarness.asUser(http.MethodGet, platformAccountsPath(testHarness.publicID(testHarness.userID)), nil).list(t)

	if len(accounts) != 1 {
		t.Fatalf("%d accounts were listed", len(accounts))
	}
	account := accounts[0].(map[string]any)
	if account["sync_likes"] != true || account["sync_collections"] != false || account["auto_library_sync"] != false {
		t.Fatalf("the flags are %v", account)
	}
	if account["token"] != "geheimes-token" {
		t.Fatalf("the token came back as %v", account["token"])
	}
}

// A second save updates the existing account instead of adding a second active
// row for the same platform.
func TestPlatformAccountsSaveUpdatesInsteadOfDuplicating(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "erstes-token", "username": "sammler"})

	answer := testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "zweites-token"})

	if answer.data(t)["token"] != "zweites-token" {
		t.Fatalf("the token is %v", answer.data(t)["token"])
	}
	count := testHarness.count("SELECT COUNT(*) FROM platform_accounts WHERE user_id = ? AND platform = 'thingiverse'",
		testHarness.userID)
	if count != 1 {
		t.Fatalf("%d accounts exist", count)
	}
	// A field the form did not send keeps its stored value.
	if answer.data(t)["username"] != "sammler" {
		t.Fatalf("the username is %v", answer.data(t)["username"])
	}
}

// The form sends "***" for a password it never showed. That must not overwrite
// the stored one with three asterisks.
func TestPlatformAccountsSaveKeepsAMaskedPassword(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "cults3d", "username": "sammler", "password": "geheim"})
	before := testHarness.scalar("SELECT password_encrypted FROM platform_accounts WHERE user_id = ?", testHarness.userID)

	answer := testHarness.saveAccount(map[string]any{"platform": "cults3d", "username": "sammler", "password": "***"})

	if answer.data(t)["has_password"] != true {
		t.Fatal("the password was dropped")
	}
	after := testHarness.scalar("SELECT password_encrypted FROM platform_accounts WHERE user_id = ?", testHarness.userID)
	if after != before {
		t.Fatal("the stored password was replaced by the mask")
	}
}

func TestPlatformAccountsSaveNeedsAPlatform(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.saveAccount(map[string]any{"platform": "  ", "token": "geheimes-token"})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a missing platform answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.platform_required" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// Switching a sync on starts the library sync in the background; saving the
// same flags again does not.
func TestPlatformAccountsSaveReportsTheTriggeredSync(t *testing.T) {
	testHarness := newHarness(t)

	first := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID)), map[string]any{
		"platform": "printables", "username": "sammler",
		"sync_likes": 0, "sync_collections": 1, "auto_library_sync": 1,
	})
	if first.data(t)["auto_synced"] != true {
		t.Fatalf("the first save reports %v", first.data(t)["auto_synced"])
	}

	second := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID)), map[string]any{
		"platform": "printables", "username": "sammler",
		"sync_likes": 0, "sync_collections": 1, "auto_library_sync": 1,
	})
	if second.data(t)["auto_synced"] != false {
		t.Fatalf("the second save reports %v", second.data(t)["auto_synced"])
	}
}

// disableLibrarySync flips the server-wide switch through the admin route, the
// same way the settings form does.
func (testHarness *harness) disableLibrarySync() {
	testHarness.t.Helper()
	answer := testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{"library_sync_enabled": 0})
	if answer.status != http.StatusOK {
		testHarness.t.Fatalf("switching the library sync off answered %d: %s", answer.status, answer.rawBody)
	}
}

// The server switch outranks the account flags: with the sync off, saving
// credentials must not start one either.
func TestPlatformAccountsSaveStartsNoSyncWhileDisabledServerSide(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.disableLibrarySync()

	answer := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID)), map[string]any{
		"platform": "printables", "username": "sammler",
		"sync_likes": 0, "sync_collections": 1, "auto_library_sync": 1,
	})

	if answer.status != http.StatusOK {
		t.Fatalf("the save answered %d: %s", answer.status, answer.rawBody)
	}
	if answer.data(t)["auto_synced"] != false {
		t.Fatalf("the save reports %v", answer.data(t)["auto_synced"])
	}
}

// The manual sync routes answer with a reason instead of a silent success, so
// the client can tell the member why nothing happened.
func TestPlatformAccountsSyncIsRefusedWhileDisabledServerSide(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "geheimes-token"})
	testHarness.disableLibrarySync()

	for _, path := range []string{
		platformAccountsPath(testHarness.publicID(testHarness.userID)) + "/sync-all",
		platformAccountsPath(testHarness.publicID(testHarness.userID)) + "/thingiverse/sync",
	} {
		answer := testHarness.asUser(http.MethodPost, path, nil)
		if answer.status != http.StatusForbidden {
			t.Fatalf("%s answered %d: %s", path, answer.status, answer.rawBody)
		}
		if key := answer.errorKey(t); key != "error.library_sync_disabled" {
			t.Fatalf("%s produced the error key %q", path, key)
		}
	}
}

func TestPlatformAccountsDeleteRemovesTheOwnAccount(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "geheimes-token"})
	accountID := testHarness.scalarInt("SELECT id FROM platform_accounts WHERE user_id = ?", testHarness.userID)

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("%s/%d", platformAccountsPath(testHarness.publicID(testHarness.userID)), accountID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM platform_accounts WHERE id = ?", accountID); count != 0 {
		t.Fatal("the account survived")
	}
}

// The delete is scoped to the owner, so an id belonging to somebody else does
// nothing at all.
func TestPlatformAccountsDeleteLeavesAForeignAccountAlone(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertPlatformAccount(testHarness.adminID, "thingiverse", "fremdes-token", "fremd")
	foreignID := testHarness.scalarInt("SELECT id FROM platform_accounts WHERE user_id = ?", testHarness.adminID)

	testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("%s/%d", platformAccountsPath(testHarness.publicID(testHarness.userID)), foreignID), nil)

	if count := testHarness.count("SELECT COUNT(*) FROM platform_accounts WHERE id = ?", foreignID); count != 1 {
		t.Fatal("the foreign account was deleted")
	}
}

func TestPlatformAccountsDeleteWithANonNumericIDIsNotFound(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodDelete, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/abc", nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a non-numeric id answered %d", answer.status)
	}
}

// The platform accounts belong to their user; no route may be called with
// somebody else's id.
func TestPlatformAccountRoutesAreSelfScoped(t *testing.T) {
	testHarness := newHarness(t)
	foreignPath := platformAccountsPath(testHarness.publicID(testHarness.adminID))

	calls := []struct {
		method string
		path   string
	}{
		{http.MethodGet, foreignPath},
		{http.MethodPost, foreignPath},
		{http.MethodPost, foreignPath + "/validate"},
		{http.MethodDelete, foreignPath + "/1"},
		{http.MethodPost, foreignPath + "/sync-all"},
		{http.MethodPost, foreignPath + "/thingiverse/sync"},
	}

	for _, call := range calls {
		answer := testHarness.asUser(call.method, call.path, map[string]any{"platform": "thingiverse"})
		if answer.status != http.StatusForbidden {
			t.Fatalf("%s %s answered %d", call.method, call.path, answer.status)
		}
		if key := answer.errorKey(t); key != "error.forbidden" {
			t.Fatalf("%s %s produced the error key %q", call.method, call.path, key)
		}
	}
}

func TestPlatformAccountsSyncRoutesAnswerWithoutAccounts(t *testing.T) {
	// One harness per route: a manual run starts the account-wide cooldown, so
	// the second call in a shared harness would be refused for that reason
	// rather than answering the question this test asks.
	for _, suffix := range []string{"/sync-all", "/thingiverse/sync"} {
		testHarness := newHarness(t)
		path := platformAccountsPath(testHarness.publicID(testHarness.userID)) + suffix
		answer := testHarness.asUser(http.MethodPost, path, nil)
		if answer.status != http.StatusOK {
			t.Fatalf("%s answered %d: %s", path, answer.status, answer.rawBody)
		}
	}
}

// A platform whose downloader cannot check credentials says so instead of
// claiming they are wrong.
func TestPlatformAccountsValidateReportsAnUnsupportedPlatform(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/validate",
		map[string]any{"platform": "thingiverse", "token": "geheimes-token"})

	if answer.status != http.StatusOK {
		t.Fatalf("the check answered %d: %s", answer.status, answer.rawBody)
	}
	result := answer.data(t)
	if result["ok"] != true || result["unsupported"] != true {
		t.Fatalf("the check reports %v", result)
	}
}

func TestPlatformAccountsValidateUsesTheDownloader(t *testing.T) {
	testHarness := newHarness(t)
	validator := &stubValidator{}
	testHarness.server.Registry = platforms.Registry{"thingiverse": validator}

	validator.valid = false
	rejected := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/validate",
		map[string]any{"platform": "thingiverse", "token": "falsches-token"}).data(t)
	if rejected["ok"] != false || rejected["error"] != "error.platform_invalid_credentials" {
		t.Fatalf("the rejection reports %v", rejected)
	}
	if validator.seen.Token != "falsches-token" {
		t.Fatalf("the downloader saw %v", validator.seen)
	}

	validator.valid = true
	accepted := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/validate",
		map[string]any{"platform": "thingiverse", "token": "richtiges-token"}).data(t)
	if accepted["ok"] != true || accepted["error"] != nil {
		t.Fatalf("the acceptance reports %v", accepted)
	}
}

// A downloader that names the reason passes its i18n key through, so the
// frontend can point at the wrong field.
func TestPlatformAccountsValidatePassesTheReasonThrough(t *testing.T) {
	testHarness := newHarness(t)
	validator := &stubReasonValidator{}
	validator.reason = "error.platform_invalid_token"
	testHarness.server.Registry = platforms.Registry{"thingiverse": validator}

	rejected := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/validate",
		map[string]any{"platform": "thingiverse", "token": "falsches-token"}).data(t)

	if rejected["ok"] != false || rejected["error"] != "error.platform_invalid_token" {
		t.Fatalf("the rejection reports %v", rejected)
	}

	validator.reason = ""
	accepted := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/validate",
		map[string]any{"platform": "thingiverse", "token": "richtiges-token"}).data(t)
	if accepted["ok"] != true || accepted["error"] != nil {
		t.Fatalf("the acceptance reports %v", accepted)
	}
}

// The form sends "***" for fields it never showed; the check has to fill them
// in from the stored credentials rather than validate with a mask.
func TestPlatformAccountsValidateResolvesMaskedFields(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{
		"platform": "thingiverse", "token": "geheimes-token", "username": "sammler",
		"password": "geheim", "totp_secret": "ABCDEF",
	})
	validator := &stubValidator{}
	validator.valid = true
	testHarness.server.Registry = platforms.Registry{"thingiverse": validator}

	testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/validate",
		map[string]any{"platform": "thingiverse", "token": "***", "password": "***"})

	if validator.seen.Token != "geheimes-token" || validator.seen.Password != "geheim" {
		t.Fatalf("the downloader saw %+v", validator.seen)
	}
	// Fields the form left out entirely come from the store as well.
	if validator.seen.Email != "sammler" || validator.seen.TOTP != "ABCDEF" {
		t.Fatalf("the downloader saw %+v", validator.seen)
	}
}

// Credentials of another user are never handed to a validator.
func TestPlatformAccountsValidateIgnoresForeignCredentials(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertPlatformAccount(testHarness.adminID, "thingiverse", "fremdes-token", "fremd")
	validator := &stubValidator{}
	validator.valid = true
	testHarness.server.Registry = platforms.Registry{"thingiverse": validator}

	testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/validate",
		map[string]any{"platform": "thingiverse", "token": "***"})

	if validator.seen.Token != "" {
		t.Fatalf("the downloader saw the foreign token %q", validator.seen.Token)
	}
}

// An account that was switched off stays out of the list.
func TestPlatformAccountsIndexSkipsInactiveAccounts(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "geheimes-token"})
	if _, failure := testHarness.database.Exec(
		"UPDATE platform_accounts SET state = 'inactive' WHERE user_id = ?", testHarness.userID); failure != nil {
		t.Fatalf("deactivate the account: %v", failure)
	}

	accounts := testHarness.asUser(http.MethodGet, platformAccountsPath(testHarness.publicID(testHarness.userID)), nil).list(t)

	if len(accounts) != 0 {
		t.Fatalf("%d inactive accounts were listed", len(accounts))
	}
}

func TestTrimOrNilDropsEmptyValues(t *testing.T) {
	for _, value := range []any{nil, "", "   ", "\t\n"} {
		if trimmed := trimOrNil(value); trimmed != nil {
			t.Fatalf("trimOrNil(%v) = %q", value, *trimmed)
		}
	}
	if trimmed := trimOrNil("  sammler  "); trimmed == nil || *trimmed != "sammler" {
		t.Fatalf("trimOrNil did not trim: %v", trimmed)
	}
}

// syncCooldownRemaining is what closes the "sync now" buttons; it has to read
// the naive UTC stamps SQLite writes and survive anything else.
func TestSyncCooldownRemainingReadsTheStoredTimestamp(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name     string
		lastSync string
		wantLeft bool
	}{
		{"never synced", "", false},
		{"unparseable", "gestern", false},
		{"expired", now.Add(-2 * manualSyncCooldown).Format(sqliteTimeLayout), false},
		{"just started", now.Format(sqliteTimeLayout), true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			left := syncCooldownRemaining(testCase.lastSync)
			if (left > 0) != testCase.wantLeft {
				t.Fatalf("%q left %d seconds on the clock", testCase.lastSync, left)
			}
			if left > int(manualSyncCooldown.Seconds())+1 {
				t.Fatalf("%q left more than the cooldown itself: %d", testCase.lastSync, left)
			}
		})
	}
}

// A second trigger within the cooldown is refused: a burst of manual syncs is
// what gets a platform account blocked.
func TestPlatformAccountsSyncIsRefusedDuringTheCooldown(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "geheimes-token"})
	testHarness.database.Exec(
		"UPDATE platform_accounts SET library_last_synced_at = CURRENT_TIMESTAMP WHERE user_id = ?", testHarness.userID)

	for _, path := range []string{
		platformAccountsPath(testHarness.publicID(testHarness.userID)) + "/sync-all",
		platformAccountsPath(testHarness.publicID(testHarness.userID)) + "/thingiverse/sync",
	} {
		answer := testHarness.asUser(http.MethodPost, path, nil)
		if answer.status != http.StatusTooManyRequests {
			t.Fatalf("%s answered %d: %s", path, answer.status, answer.rawBody)
		}
		if key := answer.errorKey(t); key != "error.sync_cooldown" {
			t.Fatalf("%s produced the error key %q", path, key)
		}
	}
}

// The cooldown is per account: a sync of one platform must not close the button
// of another one.
func TestPlatformAccountsSyncCooldownIsPerPlatform(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "geheimes-token"})
	testHarness.saveAccount(map[string]any{"platform": "printables", "username": "sammler", "password": "geheim"})
	testHarness.database.Exec(
		"UPDATE platform_accounts SET library_last_synced_at = CURRENT_TIMESTAMP WHERE user_id = ? AND platform = 'thingiverse'",
		testHarness.userID)

	answer := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/printables/sync", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the untouched platform answered %d: %s", answer.status, answer.rawBody)
	}
}

// Issue #4: triggering one platform's sync must not put another platform's
// "sync now" on cooldown. Unlike the test above (which stamps a timestamp
// directly), this goes through the real trigger, which used to also stamp the
// account-wide users.last_manual_sync_at and thereby block every other platform.
func TestPlatformAccountsSyncOneDoesNotBlockAnotherPlatform(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "geheimes-token"})
	testHarness.saveAccount(map[string]any{"platform": "printables", "username": "sammler", "password": "geheim"})

	first := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/thingiverse/sync", nil)
	if first.status != http.StatusOK {
		t.Fatalf("the thingiverse trigger answered %d: %s", first.status, first.rawBody)
	}
	second := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/printables/sync", nil)
	if second.status != http.StatusOK {
		t.Fatalf("the printables trigger answered %d (want 200) - one platform's sync blocked another: %s", second.status, second.rawBody)
	}
}

// The list carries the remaining cooldown so the client can disable the button
// instead of offering a call the server only rejects.
func TestPlatformAccountsIndexReportsTheRemainingCooldown(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "geheimes-token"})
	testHarness.database.Exec(
		"UPDATE platform_accounts SET library_last_synced_at = CURRENT_TIMESTAMP WHERE user_id = ?", testHarness.userID)

	answer := testHarness.asUser(http.MethodGet, platformAccountsPath(testHarness.publicID(testHarness.userID)), nil)

	accounts := answer.list(t)
	if len(accounts) != 1 {
		t.Fatalf("%d accounts were listed", len(accounts))
	}
	account, isObject := accounts[0].(map[string]any)
	if !isObject {
		t.Fatalf("the listed account is %T", accounts[0])
	}
	seconds, isNumber := account["sync_cooldown_seconds"].(float64)
	if !isNumber || seconds <= 0 {
		t.Fatalf("the account reports the cooldown as %v", account["sync_cooldown_seconds"])
	}
}

// A triggered sync stamps the account right away, so the button closes even
// while the sync itself is still running.
func TestPlatformAccountsSyncStampsTheAccount(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{"platform": "thingiverse", "token": "geheimes-token"})

	answer := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/thingiverse/sync", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the trigger answered %d: %s", answer.status, answer.rawBody)
	}
	if stamp := testHarness.scalar("SELECT library_last_synced_at FROM platform_accounts WHERE user_id = ?", testHarness.userID); stamp == "" {
		t.Fatal("the account was not stamped")
	}
}

// The cooldown used to hang off the platform accounts alone, so a member
// without one could trigger the run as fast as they could click - which is
// exactly the state a fresh installation is in.
func TestSyncAllHoldsItsCooldownWithoutAnyPlatformAccount(t *testing.T) {
	testHarness := newHarness(t)
	path := platformAccountsPath(testHarness.publicID(testHarness.userID)) + "/sync-all"
	if count := testHarness.count("SELECT COUNT(*) FROM platform_accounts WHERE user_id = ?", testHarness.userID); count != 0 {
		t.Fatalf("the test needs an account without platforms, found %d", count)
	}

	if answer := testHarness.asUser(http.MethodPost, path, nil); answer.status != http.StatusOK {
		t.Fatalf("the first run answered %d: %s", answer.status, answer.rawBody)
	}
	second := testHarness.asUser(http.MethodPost, path, nil)
	if second.status != http.StatusTooManyRequests {
		t.Fatalf("the second run answered %d, want 429", second.status)
	}
	if key := second.errorKey(t); key != "error.sync_cooldown" {
		t.Fatalf("reported %q", key)
	}
}

// A credential that no longer decrypts (APP_KEY changed after it was saved)
// must be reported as such. Without the flag the account looks like one that
// was never set up, and the user has nothing to act on.
func TestPlatformAccountsIndexFlagsUnreadableCredentials(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{
		"platform": "thingiverse", "token": "geheimes-token", "username": "sammler", "password": "geheim",
	})
	if _, failure := testHarness.database.Exec(
		// Valid base64, but not something this key can open.
		"UPDATE platform_accounts SET password_encrypted = ? WHERE user_id = ?",
		"QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", testHarness.userID); failure != nil {
		t.Fatalf("break the stored password: %v", failure)
	}

	accounts := testHarness.asUser(http.MethodGet, platformAccountsPath(testHarness.publicID(testHarness.userID)), nil).list(t)

	if len(accounts) != 1 {
		t.Fatalf("%d accounts were listed", len(accounts))
	}
	if account := accounts[0].(map[string]any); account["credentials_unreadable"] != true {
		t.Fatalf("the account came back as %v", account)
	}
}

func TestPlatformAccountsIndexReportsReadableCredentialsAsReadable(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{
		"platform": "thingiverse", "token": "geheimes-token", "username": "sammler", "password": "geheim",
	})

	accounts := testHarness.asUser(http.MethodGet, platformAccountsPath(testHarness.publicID(testHarness.userID)), nil).list(t)

	if account := accounts[0].(map[string]any); account["credentials_unreadable"] != false {
		t.Fatalf("the account came back as %v", account)
	}
}

// Validating with a masked password whose stored value cannot be decrypted used
// to check the platform with an empty password, so the user was told their
// credentials were wrong. It must name the real problem instead.
func TestPlatformAccountsValidateReportsUnreadableCredentials(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{
		"platform": "thingiverse", "token": "geheimes-token", "username": "sammler", "password": "geheim",
	})
	if _, failure := testHarness.database.Exec(
		"UPDATE platform_accounts SET password_encrypted = ? WHERE user_id = ?",
		"QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", testHarness.userID); failure != nil {
		t.Fatalf("break the stored password: %v", failure)
	}
	validator := &stubValidator{}
	validator.valid = true
	testHarness.server.Registry = platforms.Registry{"thingiverse": validator}

	answer := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/validate",
		map[string]any{"platform": "thingiverse", "username": "sammler", "password": "***"})

	data := answer.data(t)
	if data["ok"] != false || data["error"] != "error.platform_credentials_unreadable" {
		t.Fatalf("the answer is %v", data)
	}
	// The platform must not have been contacted with an empty password.
	if validator.seen.Password != "" || validator.seen.Email != "" {
		t.Fatalf("the downloader was called with %+v", validator.seen)
	}
}

// A password the user typed replaces the unreadable one, so validation runs
// normally instead of reporting the stored value.
func TestPlatformAccountsValidateAcceptsATypedPasswordOverAnUnreadableOne(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.saveAccount(map[string]any{
		"platform": "thingiverse", "token": "geheimes-token", "username": "sammler", "password": "geheim",
	})
	if _, failure := testHarness.database.Exec(
		"UPDATE platform_accounts SET password_encrypted = ? WHERE user_id = ?",
		"QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", testHarness.userID); failure != nil {
		t.Fatalf("break the stored password: %v", failure)
	}
	validator := &stubValidator{}
	validator.valid = true
	testHarness.server.Registry = platforms.Registry{"thingiverse": validator}

	answer := testHarness.asUser(http.MethodPost, platformAccountsPath(testHarness.publicID(testHarness.userID))+"/validate",
		map[string]any{"platform": "thingiverse", "username": "sammler", "password": "neues-passwort"})

	if answer.data(t)["ok"] != true {
		t.Fatalf("the answer is %v", answer.data(t))
	}
	if validator.seen.Password != "neues-passwort" {
		t.Fatalf("the downloader saw %+v", validator.seen)
	}
}
