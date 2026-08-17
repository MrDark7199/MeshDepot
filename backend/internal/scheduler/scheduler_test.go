package scheduler

import (
	"database/sql"
	"path/filepath"
	"testing"

	"meshdepot/internal/db"
	"meshdepot/internal/publicid"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	database, failure := db.Open(filepath.Join(t.TempDir(), "s.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	return database
}

func TestConsumeForceFlag(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	if scheduler.consumeForceFlag() {
		t.Fatal("flag is 0 -> should be false")
	}
	database.Exec("UPDATE app_settings SET value='1' WHERE key='library_sync_force'")
	if !scheduler.consumeForceFlag() {
		t.Fatal("flag=1 -> should be true")
	}
	if scheduler.consumeForceFlag() {
		t.Fatal("back to 0 after consumption -> should be false")
	}
}

func TestAutoSyncEnqueue(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	// one syncable design (with source_url, never checked)
	database.Exec("INSERT INTO designs (user_id, public_id, name, source_url, source_platform) VALUES (1,?,'D','https://x/thing:1','thingiverse')", publicid.New())
	// one local design without source_url -> not syncable
	database.Exec("INSERT INTO designs (user_id, public_id, name, source_platform) VALUES (1,?,'L','manual')", publicid.New())

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 1 {
		t.Fatalf("expected 1 enqueued, got %d", enqueued)
	}
	// second run: already pending -> nothing new
	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 0 {
		t.Fatalf("expected 0 on 2nd run, got %d", enqueued)
	}
	var count int
	database.QueryRow("SELECT COUNT(*) FROM sync_queue").Scan(&count)
	if count != 1 {
		t.Fatalf("sync_queue should have 1 row, has %d", count)
	}
}

// insertUser adds an active member; designs and prefs reference users, so a
// second member has to exist before rows can point at it.
func insertUser(t *testing.T, database *sql.DB, name string) {
	t.Helper()
	if _, failure := database.Exec("INSERT INTO users (email, hash, name, state, public_id) VALUES (NULL, 'x', ?, 'active', ?)", name, publicid.New()); failure != nil {
		t.Fatal(failure)
	}
}

// insertSyncable adds a design with a source URL and a last check that is
// daysAgo days old (daysAgo < 0 means "never checked").
func insertSyncable(t *testing.T, database *sql.DB, userID int, name string, daysAgo int) {
	t.Helper()
	lastSynced := any(nil)
	if daysAgo >= 0 {
		var stamp string
		database.QueryRow("SELECT datetime('now', ? || ' days')", -daysAgo).Scan(&stamp)
		lastSynced = stamp
	}
	if _, failure := database.Exec(
		"INSERT INTO designs (user_id, public_id, name, source_url, source_platform, last_synced_at) VALUES (?, ?, ?, 'https://x/thing:1', 'thingiverse', ?)",
		userID, publicid.New(), name, lastSynced,
	); failure != nil {
		t.Fatal(failure)
	}
}

// queuedDesignNames returns the names of all designs sitting in the sync queue.
func queuedDesignNames(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, failure := database.Query("SELECT d.name FROM sync_queue sq JOIN designs d ON d.id = sq.design_id ORDER BY d.name")
	if failure != nil {
		t.Fatal(failure)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		names = append(names, name)
	}
	return names
}

func TestDesignUpdateSettingsFallBackToTheBuiltInMinimum(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	database.Exec("DELETE FROM app_settings WHERE key IN ('design_update_enabled','design_update_min_days')")

	enabled, minDays := scheduler.designUpdateSettings()
	if !enabled || minDays != DesignUpdateMinDays {
		t.Fatalf("missing rows should mean enabled/%d, got %v/%d", DesignUpdateMinDays, enabled, minDays)
	}
}

func TestDesignUpdateSettingsNeverDropBelowTheMinimum(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	database.Exec("UPDATE app_settings SET value='2' WHERE key='design_update_min_days'")

	if _, minDays := scheduler.designUpdateSettings(); minDays != DesignUpdateMinDays {
		t.Fatalf("a value below the minimum must not lower the floor, got %d", minDays)
	}
	database.Exec("UPDATE app_settings SET value='30' WHERE key='design_update_min_days'")
	if _, minDays := scheduler.designUpdateSettings(); minDays != 30 {
		t.Fatalf("expected the configured 30 days, got %d", minDays)
	}
}

func TestAutoSyncEnqueueSkipsDesignsCheckedWithinTheInterval(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	insertSyncable(t, database, 1, "fresh", 3)
	insertSyncable(t, database, 1, "due", 9)

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 1 {
		t.Fatalf("only the design past the interval should be queued, got %d", enqueued)
	}
	if names := queuedDesignNames(t, database); len(names) != 1 || names[0] != "due" {
		t.Fatalf("expected only 'due' in the queue, got %v", names)
	}
}

func TestAutoSyncEnqueueHonoursThePerUserInterval(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	insertUser(t, database, "second")
	database.Exec("INSERT INTO notification_prefs (user_id, sync_min_age_days) VALUES (2, 30)")
	insertSyncable(t, database, 1, "default-user", 9)
	insertSyncable(t, database, 2, "patient-user", 9)

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 1 {
		t.Fatalf("the 30-day member is not due yet, got %d queued", enqueued)
	}
	if names := queuedDesignNames(t, database); len(names) != 1 || names[0] != "default-user" {
		t.Fatalf("expected only 'default-user', got %v", names)
	}
}

func TestAutoSyncEnqueueSkipsMembersWhoTurnedUpdatesOff(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	database.Exec("INSERT INTO notification_prefs (user_id, sync_min_age_days) VALUES (1, NULL)")
	insertSyncable(t, database, 1, "opted-out", 99)

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 0 {
		t.Fatalf("NULL means no updates, got %d queued", enqueued)
	}
}

func TestAutoSyncEnqueueRaisesShortIntervalsToTheServerFloor(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	database.Exec("UPDATE app_settings SET value='30' WHERE key='design_update_min_days'")
	database.Exec("INSERT INTO notification_prefs (user_id, sync_min_age_days) VALUES (1, 7)")
	insertSyncable(t, database, 1, "below-floor", 9)

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 0 {
		t.Fatalf("the server floor of 30 days outranks the member's 7, got %d queued", enqueued)
	}
}

func TestAutoSyncEnqueueStopsWhileUpdatesAreDisabled(t *testing.T) {
	database := newDB(t)
	scheduler := New(database, nil)
	database.Exec("UPDATE app_settings SET value='0' WHERE key='design_update_enabled'")
	insertSyncable(t, database, 1, "due", 99)

	if enqueued := scheduler.autoSyncEnqueue(); enqueued != 0 {
		t.Fatalf("the server switch is off, got %d queued", enqueued)
	}
}
