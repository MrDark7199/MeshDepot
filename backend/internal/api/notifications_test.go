package api

import (
	"fmt"
	"net/http"
	"testing"

	"meshdepot/internal/coerce"
	"meshdepot/internal/scheduler"
)

// notificationsPath builds the self-scoped notification path of a user, which
// addresses the account by its public id.
func notificationsPath(publicID, suffix string) string {
	return fmt.Sprintf("/api/v1/users/%s/notifications%s", publicID, suffix)
}

func TestNotificationsIndexReturnsTheOwnNotificationsWithTheUnreadCount(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertNotification(testHarness.userID, "sync_update", "Ungelesen", "")
	testHarness.insertNotification(testHarness.userID, "sync_update", "Gelesen", "datetime('now')")
	testHarness.insertNotification(testHarness.adminID, "sync_update", "Fremd", "")

	payload := testHarness.asUser(http.MethodGet, notificationsPath(testHarness.publicID(testHarness.userID), ""), nil).data(t)

	items := payload["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("%d notifications were listed", len(items))
	}
	for _, item := range items {
		if item.(map[string]any)["title"] == "Fremd" {
			t.Fatal("a foreign notification was listed")
		}
	}
	if unread := coerce.Int(payload["unread"]); unread != 1 {
		t.Fatalf("the unread count is %d", unread)
	}
}

func TestNotificationsIndexIsEmptyWithoutNotifications(t *testing.T) {
	testHarness := newHarness(t)

	payload := testHarness.asUser(http.MethodGet, notificationsPath(testHarness.publicID(testHarness.userID), ""), nil).data(t)

	if items := payload["items"].([]any); len(items) != 0 {
		t.Fatalf("%d notifications were listed", len(items))
	}
	if unread := coerce.Int(payload["unread"]); unread != 0 {
		t.Fatalf("the unread count is %d", unread)
	}
}

// Every notification route is self-scoped: the {id} in the path has to be the
// session user, otherwise one user could read another user's inbox.
func TestNotificationRoutesRefuseAForeignUserID(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertNotification(testHarness.adminID, "sync_update", "Fremd", "")

	calls := []struct {
		method string
		path   string
	}{
		{http.MethodGet, notificationsPath(testHarness.publicID(testHarness.adminID), "")},
		{http.MethodPost, notificationsPath(testHarness.publicID(testHarness.adminID), "/read-all")},
		{http.MethodDelete, notificationsPath(testHarness.publicID(testHarness.adminID), "")},
		{http.MethodDelete, notificationsPath(testHarness.publicID(testHarness.adminID), "/1")},
		{http.MethodGet, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.adminID))},
		{http.MethodPut, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.adminID))},
	}

	for _, call := range calls {
		answer := testHarness.asUser(call.method, call.path, map[string]any{})
		if answer.status != http.StatusForbidden {
			t.Fatalf("%s %s answered %d", call.method, call.path, answer.status)
		}
		if key := answer.errorKey(t); key != "error.forbidden" {
			t.Fatalf("%s %s produced the error key %q", call.method, call.path, key)
		}
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE user_id = ?", testHarness.adminID); count != 1 {
		t.Fatal("a foreign notification was deleted")
	}
}

func TestNotificationRoutesWithANonNumericUserIDAreNotFound(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodGet, "/api/v1/users/abc/notifications", nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a non-numeric user id answered %d", answer.status)
	}
}

func TestNotificationsReadAllMarksOnlyTheOwnUnreadOnes(t *testing.T) {
	testHarness := newHarness(t)
	own := testHarness.insertNotification(testHarness.userID, "sync_update", "Ungelesen", "")
	foreign := testHarness.insertNotification(testHarness.adminID, "sync_update", "Fremd", "")

	answer := testHarness.asUser(http.MethodPost, notificationsPath(testHarness.publicID(testHarness.userID), "/read-all"), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("read-all answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE id = ? AND read_at IS NOT NULL", own); count != 1 {
		t.Fatal("the own notification is still unread")
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE id = ? AND read_at IS NULL", foreign); count != 1 {
		t.Fatal("a foreign notification was marked as read")
	}
}

func TestNotificationsDeleteAllRemovesOnlyTheOwnOnes(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.insertNotification(testHarness.userID, "sync_update", "Eigen", "")
	testHarness.insertNotification(testHarness.userID, "sync_update", "Auch eigen", "datetime('now')")
	testHarness.insertNotification(testHarness.adminID, "sync_update", "Fremd", "")

	answer := testHarness.asUser(http.MethodDelete, notificationsPath(testHarness.publicID(testHarness.userID), ""), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("delete-all answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE user_id = ?", testHarness.userID); count != 0 {
		t.Fatalf("%d own notifications survived", count)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE user_id = ?", testHarness.adminID); count != 1 {
		t.Fatal("a foreign notification was deleted")
	}
}

func TestNotificationsDeleteRemovesASingleNotification(t *testing.T) {
	testHarness := newHarness(t)
	first := testHarness.insertNotification(testHarness.userID, "sync_update", "Weg damit", "")
	second := testHarness.insertNotification(testHarness.userID, "sync_update", "Bleibt", "")

	answer := testHarness.asUser(http.MethodDelete, notificationsPath(testHarness.publicID(testHarness.userID), fmt.Sprintf("/%d", first)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("deleting answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE id = ?", first); count != 0 {
		t.Fatal("the notification survived")
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE id = ?", second); count != 1 {
		t.Fatal("the wrong notification was deleted")
	}
}

// Deleting a foreign notification answers 404 rather than 403: a 403 would
// confirm that the id exists.
func TestNotificationsDeleteRejectsAForeignNotification(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertNotification(testHarness.adminID, "sync_update", "Fremd", "")

	answer := testHarness.asUser(http.MethodDelete, notificationsPath(testHarness.publicID(testHarness.userID), fmt.Sprintf("/%d", foreign)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign notification answered %d", answer.status)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notifications WHERE id = ?", foreign); count != 1 {
		t.Fatal("the foreign notification was deleted")
	}
}

func TestNotificationsDeleteAnswersNotFoundForAnUnknownID(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodDelete, notificationsPath(testHarness.publicID(testHarness.userID), "/98765"), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("an unknown notification answered %d", answer.status)
	}
}

func TestNotificationsDeleteWithANonNumericIDIsNotFound(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodDelete, notificationsPath(testHarness.publicID(testHarness.userID), "/abc"), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a non-numeric notification id answered %d", answer.status)
	}
}

// A user who never touched the settings has no row; the defaults stand in for
// it so the frontend can render the form either way.
func TestNotificationPrefsFallBackToTheDefaults(t *testing.T) {
	testHarness := newHarness(t)

	prefs := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID)), nil).data(t)

	for _, key := range []string{"sync_update", "download_failed", "download_done", "design_shared", "storage_80"} {
		if coerce.Int(prefs[key]) != 1 {
			t.Fatalf("the default of %s is %v", key, prefs[key])
		}
	}
	if days := coerce.Int(prefs["sync_min_age_days"]); days != 7 {
		t.Fatalf("the default of sync_min_age_days is %d", days)
	}
	// The numeric owner is deliberately absent: the response envelope strips
	// internal columns on the way out.
	if _, present := prefs["user_id"]; present {
		t.Fatalf("the response carries the numeric user id: %v", prefs["user_id"])
	}
}

func TestNotificationPrefsReturnTheStoredRow(t *testing.T) {
	testHarness := newHarness(t)
	if _, failure := testHarness.database.Exec(
		"INSERT INTO notification_prefs (user_id, sync_update, storage_80, sync_min_age_days) VALUES (?, 0, 0, 30)",
		testHarness.userID); failure != nil {
		t.Fatalf("store the preferences: %v", failure)
	}

	prefs := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID)), nil).data(t)

	if coerce.Int(prefs["sync_update"]) != 0 || coerce.Int(prefs["storage_80"]) != 0 {
		t.Fatalf("the disabled classes are enabled: %v", prefs)
	}
	if coerce.Int(prefs["download_failed"]) != 1 {
		t.Fatalf("an untouched class is disabled: %v", prefs)
	}
	if days := coerce.Int(prefs["sync_min_age_days"]); days != 30 {
		t.Fatalf("sync_min_age_days is %d", days)
	}
}

func TestNotificationPrefsSaveCreatesTheRow(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID)),
		map[string]any{"sync_update": false, "download_failed": true, "sync_min_age_days": 14})

	if answer.status != http.StatusOK {
		t.Fatalf("saving answered %d: %s", answer.status, answer.rawBody)
	}
	stored := testHarness.count(
		"SELECT COUNT(*) FROM notification_prefs WHERE user_id = ? AND sync_update = 0 AND download_failed = 1 AND sync_min_age_days = 14",
		testHarness.userID)
	if stored != 1 {
		t.Fatal("the preferences were not stored")
	}
}

// The second save has to update the existing row instead of failing on the
// primary key.
func TestNotificationPrefsSaveUpdatesAnExistingRow(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID))

	testHarness.asUser(http.MethodPut, path, map[string]any{"sync_update": false, "storage_80": false})
	answer := testHarness.asUser(http.MethodPut, path, map[string]any{"sync_update": true, "storage_80": false})

	if answer.status != http.StatusOK {
		t.Fatalf("the second save answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM notification_prefs WHERE user_id = ?", testHarness.userID); count != 1 {
		t.Fatalf("%d preference rows exist", count)
	}
	if value := testHarness.scalarInt("SELECT sync_update FROM notification_prefs WHERE user_id = ?", testHarness.userID); value != 1 {
		t.Fatal("sync_update was not switched back on")
	}
	if value := testHarness.scalarInt("SELECT storage_80 FROM notification_prefs WHERE user_id = ?", testHarness.userID); value != 0 {
		t.Fatal("storage_80 was not kept off")
	}
}

// A missing class falls back to "enabled" - an empty body must not silently
// switch every notification off.
func TestNotificationPrefsSaveFallsBackToEnabledForMissingClasses(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID)),
		map[string]any{})

	saved := answer.data(t)
	for _, key := range []string{"sync_update", "download_failed", "download_done", "design_shared", "storage_80"} {
		if coerce.Int(saved[key]) != 1 {
			t.Fatalf("%s came back as %v", key, saved[key])
		}
	}
	if days := coerce.Int(saved["sync_min_age_days"]); days != 7 {
		t.Fatalf("sync_min_age_days came back as %d", days)
	}
}

// Every update check re-downloads the design, so an interval below the server
// floor is refused instead of quietly stored.
func TestNotificationPrefsSaveRejectsAnIntervalBelowTheFloor(t *testing.T) {
	testHarness := newHarness(t)

	for _, submitted := range []any{0, -5, scheduler.DesignUpdateMinDays - 1} {
		answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID)),
			map[string]any{"sync_min_age_days": submitted})
		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("%v answered %d: %s", submitted, answer.status, answer.rawBody)
		}
		if key := answer.errorKey(t); key != "error.design_update_interval_too_low" {
			t.Fatalf("%v produced the error key %q", submitted, key)
		}
	}
	if rows := testHarness.count("SELECT COUNT(*) FROM notification_prefs WHERE user_id = ?", testHarness.userID); rows != 0 {
		t.Fatal("a rejected interval was stored anyway")
	}
}

// The admin can raise the floor above the built-in minimum; the member's own
// value has to follow.
func TestNotificationPrefsSaveFollowsTheAdminFloor(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.asAdmin(http.MethodPut, "/api/v1/admin/settings", map[string]any{"design_update_min_days": 30})
	path := fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID))

	if answer := testHarness.asUser(http.MethodPut, path, map[string]any{"sync_min_age_days": 14}); answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("14 days below the raised floor answered %d: %s", answer.status, answer.rawBody)
	}
	answer := testHarness.asUser(http.MethodPut, path, map[string]any{"sync_min_age_days": 30})
	if answer.status != http.StatusOK {
		t.Fatalf("the raised floor itself answered %d: %s", answer.status, answer.rawBody)
	}
	if days := coerce.Int(answer.data(t)["sync_min_age_days"]); days != 30 {
		t.Fatalf("sync_min_age_days came back as %d", days)
	}
}

// A body without the field keeps the update running at the floor rather than
// switching it off.
func TestNotificationPrefsSaveDefaultsToTheFloor(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID)),
		map[string]any{"sync_update": true})

	if days := coerce.Int(answer.data(t)["sync_min_age_days"]); days != scheduler.DesignUpdateMinDays {
		t.Fatalf("sync_min_age_days came back as %d", days)
	}
}

// null means "no minimum age", which the column stores as NULL.
func TestNotificationPrefsSaveAcceptsAnEmptyMinimumSyncAge(t *testing.T) {
	testHarness := newHarness(t)

	for _, submitted := range []any{nil, "null"} {
		answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID)),
			map[string]any{"sync_min_age_days": submitted})
		if value := answer.data(t)["sync_min_age_days"]; value != nil {
			t.Fatalf("%v came back as %v", submitted, value)
		}
		stored := testHarness.count("SELECT COUNT(*) FROM notification_prefs WHERE user_id = ? AND sync_min_age_days IS NULL",
			testHarness.userID)
		if stored != 1 {
			t.Fatalf("%v was not stored as NULL", submitted)
		}
	}
}

// The form sends flags as strings when it comes from a plain HTML control, so
// "0", "false" and "" have to switch the class off just like false does.
func TestNotificationPrefsSaveUnderstandsStringFlags(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID))

	offCases := []string{"0", "false", ""}
	for _, submitted := range offCases {
		answer := testHarness.asUser(http.MethodPut, path, map[string]any{"design_shared": submitted})
		if value := coerce.Int(answer.data(t)["design_shared"]); value != 0 {
			t.Fatalf("%q came back as %d", submitted, value)
		}
	}
	answer := testHarness.asUser(http.MethodPut, path, map[string]any{"design_shared": "1"})
	if value := coerce.Int(answer.data(t)["design_shared"]); value != 1 {
		t.Fatalf("\"1\" came back as %d", value)
	}
}

// An unusable value keeps the default instead of coercing to zero, which would
// switch the class off behind the user's back.
func TestNotificationPrefsSaveKeepsTheDefaultForAnUnusableFlag(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/users/%s/notification-prefs", testHarness.publicID(testHarness.userID)),
		map[string]any{"download_done": map[string]any{"enabled": true}})

	if value := coerce.Int(answer.data(t)["download_done"]); value != 1 {
		t.Fatalf("download_done came back as %d", value)
	}
}
