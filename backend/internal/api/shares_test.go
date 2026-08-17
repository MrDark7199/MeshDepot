package api

import (
	"fmt"
	"net/http"
	"testing"

	"meshdepot/internal/coerce"
)

func TestSharesIndexListsTheRecipients(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Geteiltes Design")
	testHarness.shareDesign(designID, testHarness.userID, testHarness.adminID)

	shares := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)), nil).list(t)

	if len(shares) != 1 {
		t.Fatalf("%d shares were listed", len(shares))
	}
	entry := shares[0].(map[string]any)
	if entry["shared_with_email"] != "admin@example.org" || entry["shared_with_name"] != "administrator" {
		t.Fatalf("the recipient is wrong: %v", entry)
	}
}

func TestSharesIndexIsEmptyForAnUnsharedDesign(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Privat")

	shares := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)), nil).list(t)

	if len(shares) != 0 {
		t.Fatalf("%d shares were listed", len(shares))
	}
}

// The recipient of a share must not see who else the design was shared with -
// only the owner gets the list.
func TestSharesIndexRejectsEveryoneButTheOwner(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "Fremdes Design")
	testHarness.shareDesign(designID, testHarness.adminID, testHarness.userID)

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("the recipient answered %d", answer.status)
	}
}

func TestSharesStoreSharesWithAnActiveUser(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Neu geteilt")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)),
		map[string]any{"emails": []string{"  admin@example.org  "}})

	if answer.status != http.StatusOK {
		t.Fatalf("sharing answered %d: %s", answer.status, answer.rawBody)
	}
	if shared := coerce.Int(answer.data(t)["shared_count"]); shared != 1 {
		t.Fatalf("shared_count is %d", shared)
	}
	stored := testHarness.count(
		"SELECT COUNT(*) FROM design_shares WHERE design_id = ? AND owner_user_id = ? AND shared_with_user_id = ?",
		designID, testHarness.userID, testHarness.adminID)
	if stored != 1 {
		t.Fatal("the share row is missing")
	}
}

func TestSharesStoreNotifiesTheRecipient(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Benachrichtigung")

	testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)),
		map[string]any{"emails": []string{"admin@example.org"}})

	notifications := testHarness.count(
		"SELECT COUNT(*) FROM notifications WHERE user_id = ? AND type = 'design_shared' AND design_id = ?",
		testHarness.adminID, designID)
	if notifications != 1 {
		t.Fatalf("%d notifications were created", notifications)
	}
	title := testHarness.scalar("SELECT title FROM notifications WHERE user_id = ?", testHarness.adminID)
	if title != "member shared a design with you" {
		t.Fatalf("the notification title is %q", title)
	}
	body := testHarness.scalar("SELECT body FROM notifications WHERE user_id = ?", testHarness.adminID)
	if body != `"Mit Benachrichtigung"` {
		t.Fatalf("the notification body is %q", body)
	}
}

// Sharing twice must stay idempotent, and the second call must not notify
// again - the recipient already knows.
func TestSharesStoreDoesNotNotifyASecondTime(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Zweimal geteilt")
	body := map[string]any{"emails": []string{"admin@example.org"}}

	testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)), body)
	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)), body)

	if shared := coerce.Int(answer.data(t)["shared_count"]); shared != 1 {
		t.Fatalf("shared_count is %d on the second call", shared)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_shares WHERE design_id = ?", designID); count != 1 {
		t.Fatalf("%d share rows exist", count)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE user_id = ?", testHarness.adminID); count != 1 {
		t.Fatalf("%d notifications were created", count)
	}
}

func TestSharesStoreHonoursTheRecipientsNotificationPreference(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Ohne Benachrichtigung")
	if _, failure := testHarness.database.Exec(
		"INSERT INTO notification_prefs (user_id, design_shared) VALUES (?, 0)", testHarness.adminID); failure != nil {
		t.Fatalf("store the preference: %v", failure)
	}

	testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)),
		map[string]any{"emails": []string{"admin@example.org"}})

	if count := testHarness.count("SELECT COUNT(*) FROM design_shares WHERE design_id = ?", designID); count != 1 {
		t.Fatal("the share itself was not stored")
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE user_id = ?", testHarness.adminID); count != 0 {
		t.Fatalf("%d notifications were created despite the preference", count)
	}
}

// Unknown addresses, inactive accounts and the owner's own address are skipped
// silently: the answer must not tell the caller which addresses have an account
// here.
func TestSharesStoreSkipsAddressesWithoutAnActiveAccount(t *testing.T) {
	testHarness := newHarness(t)
	inactiveID := testHarness.createUser("gesperrt@example.org", "gesperrt", false)
	if _, failure := testHarness.database.Exec("UPDATE users SET state = 'inactive' WHERE id = ?", inactiveID); failure != nil {
		t.Fatalf("deactivate the user: %v", failure)
	}
	designID := testHarness.insertDesign(testHarness.userID, "Teilbar")

	// Unknown, deactivated, and the owner themselves: none of the three is a
	// recipient, so the request is refused instead of reporting a share that
	// never happened.
	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)),
		map[string]any{"emails": []string{"niemand@example.org", "gesperrt@example.org", "user@example.org"}})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("answered %d, want 422: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.no_users_matched" {
		t.Fatalf("reported %q", key)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_shares WHERE design_id = ?", designID); count != 0 {
		t.Fatalf("%d shares were created", count)
	}
}

// One usable recipient among unusable ones is still a share - only a request
// that reaches nobody is an error.
func TestSharesStoreSharesWithTheRecipientsItFinds(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Teilbar")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)),
		map[string]any{"emails": []string{"niemand@example.org", "admin@example.org"}})

	if answer.status != http.StatusOK {
		t.Fatalf("answered %d: %s", answer.status, answer.rawBody)
	}
	if shared := coerce.Int(answer.data(t)["shared_count"]); shared != 1 {
		t.Fatalf("shared_count is %d, want 1", shared)
	}
}

// The account list shows names, so a name has to work as well as an address.
func TestSharesStoreAcceptsAUserName(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Teilbar")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)),
		map[string]any{"emails": []string{"administrator"}})

	if answer.status != http.StatusOK {
		t.Fatalf("sharing by name answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count(
		"SELECT COUNT(*) FROM design_shares WHERE design_id = ? AND shared_with_user_id = ?", designID, testHarness.adminID); count != 1 {
		t.Fatalf("%d shares reached the admin", count)
	}
}

func TestSharesStoreNeedsAtLeastOneEmail(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Teilbar")

	for _, emails := range []any{[]string{}, []string{"   ", ""}} {
		answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)),
			map[string]any{"emails": emails})
		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("%v answered %d", emails, answer.status)
		}
		if key := answer.errorKey(t); key != "error.emails_required" {
			t.Fatalf("unexpected error key %q", key)
		}
	}
}

func TestSharesStoreRejectsAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(foreign)),
		map[string]any{"emails": []string{"user@example.org"}})

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_shares WHERE design_id = ?", foreign); count != 0 {
		t.Fatal("a share on a foreign design was created")
	}
}

func TestSharesDestroyRemovesTheShare(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Geteilt")
	testHarness.shareDesign(designID, testHarness.userID, testHarness.adminID)
	shareID := testHarness.scalarInt("SELECT id FROM design_shares WHERE design_id = ?", designID)

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/shares/%d", testHarness.designPID(designID), shareID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("removing answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_shares WHERE id = ?", shareID); count != 0 {
		t.Fatal("the share survived")
	}
	if count := testHarness.count("SELECT COUNT(*) FROM designs WHERE id = ?", designID); count != 1 {
		t.Fatal("the design was deleted along with the share")
	}
}

// The recipient may not revoke their own share; only the owner may.
func TestSharesDestroyRejectsTheRecipient(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "Fremd geteilt")
	testHarness.shareDesign(designID, testHarness.adminID, testHarness.userID)
	shareID := testHarness.scalarInt("SELECT id FROM design_shares WHERE design_id = ?", designID)

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/shares/%d", testHarness.designPID(designID), shareID), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("the recipient answered %d", answer.status)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_shares WHERE id = ?", shareID); count != 1 {
		t.Fatal("the recipient removed the share")
	}
}

func TestShareEndpointsWithNonNumericIDsAreNotFound(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Teilbar")

	calls := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/designs/abc/shares"},
		{http.MethodPost, "/api/v1/designs/abc/shares"},
		{http.MethodDelete, "/api/v1/designs/abc/shares/1"},
		{http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/shares/abc", testHarness.designPID(designID))},
	}

	for _, call := range calls {
		answer := testHarness.asUser(call.method, call.path, map[string]any{"emails": []string{"admin@example.org"}})
		if answer.status != http.StatusNotFound {
			t.Fatalf("%s %s answered %d", call.method, call.path, answer.status)
		}
	}
}
