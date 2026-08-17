package notify

import (
	"database/sql"
	"path/filepath"
	"testing"

	"meshdepot/internal/db"
	"meshdepot/internal/publicid"
)

// newTestDatabase opens a schema database with one user to attach notifications
// to.
func newTestDatabase(t *testing.T) (*sql.DB, int) {
	t.Helper()
	database, failure := db.Open(filepath.Join(t.TempDir(), "meshdepot.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	if failure := db.InitSchema(database); failure != nil {
		t.Fatalf("init schema: %v", failure)
	}
	result, failure := database.Exec(
		"INSERT INTO users (email, hash, name, state, public_id) VALUES ('user@example.org', 'x', 'ada', 'active', ?)", publicid.New(),
	)
	if failure != nil {
		t.Fatalf("insert user: %v", failure)
	}
	userID, _ := result.LastInsertId()
	return database, int(userID)
}

func countNotifications(t *testing.T, database *sql.DB, userID int) int {
	t.Helper()
	var count int
	if failure := database.QueryRow("SELECT COUNT(*) FROM notifications WHERE user_id = ?", userID).Scan(&count); failure != nil {
		t.Fatalf("count notifications: %v", failure)
	}
	return count
}

func TestUserCreatesNotification(t *testing.T) {
	database, userID := newTestDatabase(t)

	User(database, userID, "download_done", "Fertig", "Der Download ist fertig.", nil)

	var notificationType, title, body string
	var designID sql.NullInt64
	failure := database.QueryRow(
		"SELECT type, title, body, design_id FROM notifications WHERE user_id = ?", userID,
	).Scan(&notificationType, &title, &body, &designID)
	if failure != nil {
		t.Fatalf("read notification: %v", failure)
	}
	if notificationType != "download_done" || title != "Fertig" || body != "Der Download ist fertig." {
		t.Fatalf("unexpected row %q/%q/%q", notificationType, title, body)
	}
	if designID.Valid {
		t.Fatalf("design_id was not stored as NULL: %v", designID)
	}
}

func TestUserStoresDesignReference(t *testing.T) {
	database, userID := newTestDatabase(t)
	result, failure := database.Exec(
		"INSERT INTO designs (user_id, name) VALUES (?, 'Cube')", userID,
	)
	if failure != nil {
		t.Fatalf("insert design: %v", failure)
	}
	insertedID, _ := result.LastInsertId()
	designID := int(insertedID)

	User(database, userID, "sync_update", "Update", "Eine neue Version.", &designID)

	var stored sql.NullInt64
	database.QueryRow("SELECT design_id FROM notifications WHERE user_id = ?", userID).Scan(&stored)
	if !stored.Valid || int(stored.Int64) != designID {
		t.Fatalf("unexpected design_id %v", stored)
	}
}

// A type outside the whitelist would be interpolated into the SELECT, so it must
// be rejected before it reaches the database.
func TestUserIgnoresUnknownType(t *testing.T) {
	database, userID := newTestDatabase(t)

	User(database, userID, "not_a_type", "Titel", "Text", nil)
	User(database, userID, "sync_update; DROP TABLE notifications", "Titel", "Text", nil)
	User(database, userID, "", "Titel", "Text", nil)

	if count := countNotifications(t, database, userID); count != 0 {
		t.Fatalf("%d notifications were created for unknown types", count)
	}
}

func TestUserAcceptsEveryWhitelistedType(t *testing.T) {
	database, userID := newTestDatabase(t)

	for notificationType := range allowed {
		User(database, userID, notificationType, "Titel", "Text", nil)
	}

	if count := countNotifications(t, database, userID); count != len(allowed) {
		t.Fatalf("expected %d notifications, got %d", len(allowed), count)
	}
}

// No preference row means the user never touched the settings - the default is
// enabled.
func TestUserDefaultsToEnabledWithoutPreferences(t *testing.T) {
	database, userID := newTestDatabase(t)

	User(database, userID, "storage_80", "Speicher", "80 % belegt.", nil)

	if count := countNotifications(t, database, userID); count != 1 {
		t.Fatalf("expected 1 notification, got %d", count)
	}
}

func TestUserRespectsDisabledPreference(t *testing.T) {
	database, userID := newTestDatabase(t)
	if _, failure := database.Exec(
		"INSERT INTO notification_prefs (user_id, download_failed, sync_update) VALUES (?, 0, 1)", userID,
	); failure != nil {
		t.Fatalf("insert preferences: %v", failure)
	}

	User(database, userID, "download_failed", "Fehler", "Der Download ist gescheitert.", nil)
	if count := countNotifications(t, database, userID); count != 0 {
		t.Fatalf("a disabled type produced %d notifications", count)
	}

	User(database, userID, "sync_update", "Update", "Eine neue Version.", nil)
	if count := countNotifications(t, database, userID); count != 1 {
		t.Fatalf("an enabled type produced %d notifications", count)
	}
}

// The helper never fails hard: a missing user violates the foreign key, and the
// caller must not be affected by that.
func TestUserSurvivesUnknownUser(t *testing.T) {
	database, _ := newTestDatabase(t)

	User(database, 9999, "download_done", "Fertig", "Text", nil)

	var count int
	database.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count)
	if count != 0 {
		t.Fatalf("a notification was created for an unknown user: %d", count)
	}
}
