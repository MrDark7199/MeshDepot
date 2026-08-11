package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/publicid"

	"golang.org/x/crypto/bcrypt"
)

// newTestDatabase opens an initialised database in a fresh directory.
func newTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "meshdepot.db"))
	if failure := InitSchema(database); failure != nil {
		t.Fatalf("init schema: %v", failure)
	}
	return database
}

func openTestDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, failure := Open(path)
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestOpenEnablesForeignKeysAndWAL(t *testing.T) {
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "meshdepot.db"))

	var foreignKeys int
	if failure := database.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); failure != nil {
		t.Fatalf("read pragma: %v", failure)
	}
	if foreignKeys != 1 {
		t.Fatal("foreign keys are not enabled")
	}

	var journalMode string
	database.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("unexpected journal mode %q", journalMode)
	}
}

// A single pool slot is what keeps web requests and worker goroutines from
// producing "database is locked".
func TestOpenLimitsToASingleConnection(t *testing.T) {
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "meshdepot.db"))
	if maximum := database.Stats().MaxOpenConnections; maximum != 1 {
		t.Fatalf("expected 1 connection, got %d", maximum)
	}
}

// A path with spaces or a '?' must not be swallowed by the DSN parser.
func TestOpenEscapesThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh depot?db.sqlite")
	database := openTestDatabase(t, path)

	if _, failure := database.Exec("CREATE TABLE probe (id INTEGER)"); failure != nil {
		t.Fatalf("write to the escaped path: %v", failure)
	}
}

func TestOpenReportsUnusablePath(t *testing.T) {
	if _, failure := Open(filepath.Join(t.TempDir(), "missing-directory", "meshdepot.db")); failure == nil {
		t.Fatal("a path in a missing directory did not report an error")
	}
}

func TestInitSchemaCreatesEveryTable(t *testing.T) {
	database := newTestDatabase(t)

	for _, table := range []string{
		"users", "sessions", "tags", "designs", "design_tags", "design_files",
		"design_file_entries", "design_images", "collections", "design_collections",
		"design_shares", "design_sync_log", "platform_accounts", "download_queue",
		"notification_prefs", "notifications", "sync_queue", "file_blobs",
		"pending_collection_assignments", "design_translations", "app_settings",
	} {
		var name string
		failure := database.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&name)
		if failure != nil {
			t.Fatalf("table %s is missing: %v", table, failure)
		}
	}
}

func TestInitSchemaSeedsTheDefaultAdmin(t *testing.T) {
	database := newTestDatabase(t)

	var name, state, hash string
	var email sql.NullString
	var admin, mustChange int
	failure := database.QueryRow(
		"SELECT name, email, state, hash, admin, must_change_password FROM users",
	).Scan(&name, &email, &state, &hash, &admin, &mustChange)
	if failure != nil {
		t.Fatalf("read the seeded user: %v", failure)
	}

	if name != "admin" {
		t.Fatalf("unexpected name %q", name)
	}
	if email.Valid {
		t.Fatalf("the seed account carries an e-mail address: %v", email)
	}
	if state != "active" || admin != 1 {
		t.Fatalf("the seed account is not an active admin: %s/%d", state, admin)
	}
	// The credentials are public knowledge, so the account has to be replaced
	// on first use.
	if mustChange != 1 {
		t.Fatal("must_change_password is not set on the seed account")
	}
	if failure := bcrypt.CompareHashAndPassword([]byte(hash), []byte("admin")); failure != nil {
		t.Fatalf("the seed password is not 'admin': %v", failure)
	}
}

func TestInitSchemaIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meshdepot.db")
	database := openTestDatabase(t, path)

	for round := 0; round < 3; round++ {
		if failure := InitSchema(database); failure != nil {
			t.Fatalf("run %d: %v", round+1, failure)
		}
	}

	var userCount int
	database.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	if userCount != 1 {
		t.Fatalf("the seed admin was created %d times", userCount)
	}
}

// The seed only runs on an empty user table, so an existing installation never
// gets a second admin back.
func TestInitSchemaDoesNotSeedWhenUsersExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meshdepot.db")
	database := openTestDatabase(t, path)
	if failure := InitSchema(database); failure != nil {
		t.Fatalf("init schema: %v", failure)
	}
	database.Exec("DELETE FROM users")
	database.Exec("INSERT INTO users (email, hash, name, state, public_id) VALUES ('user@example.org', 'x', 'ada', 'active', ?)", publicid.New())

	if failure := InitSchema(database); failure != nil {
		t.Fatalf("second init: %v", failure)
	}

	var names []string
	rows, _ := database.Query("SELECT name FROM users")
	defer rows.Close()
	for rows.Next() {
		var name string
		rows.Scan(&name)
		names = append(names, name)
	}
	if len(names) != 1 || names[0] != "ada" {
		t.Fatalf("the seed ran on a populated table: %v", names)
	}
}

func TestInitSchemaReportsFailureOnClosedDatabase(t *testing.T) {
	database, failure := Open(filepath.Join(t.TempDir(), "meshdepot.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	database.Close()

	if failure := InitSchema(database); failure == nil {
		t.Fatal("a closed database did not report an error")
	}
}

func TestMigrateAddsTheLaterColumns(t *testing.T) {
	database := newTestDatabase(t)

	for table, column := range map[string]string{
		"sync_queue":          "current_step",
		"design_file_entries": "gcode_meta",
		"tags":                "source",
	} {
		if !columnExists(t, database, table, column) {
			t.Fatalf("%s.%s is missing", table, column)
		}
	}
	for _, column := range []string{"step_current", "step_total", "started_at"} {
		if !columnExists(t, database, "sync_queue", column) {
			t.Fatalf("sync_queue.%s is missing", column)
		}
	}
}

// The dead table must not come back on a database that still has it.
func TestMigrateDropsUserSessions(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("CREATE TABLE user_sessions (id INTEGER PRIMARY KEY)")

	migrate(database)

	var name string
	failure := database.QueryRow("SELECT name FROM sqlite_master WHERE name = 'user_sessions'").Scan(&name)
	if failure == nil {
		t.Fatal("user_sessions survived the migration")
	}
}

// Sessions written by the older code used RFC3339; those rows have to be
// normalized to the layout every other time column uses.
func TestMigrateNormalizesSessionExpiry(t *testing.T) {
	database := newTestDatabase(t)
	var userID int
	database.QueryRow("SELECT id FROM users").Scan(&userID)
	database.Exec(
		"INSERT INTO sessions (id, user_id, persistent, expires_at) VALUES ('old', ?, 0, '2026-07-27T12:34:56Z')",
		userID,
	)

	migrate(database)

	var expiry string
	database.QueryRow("SELECT expires_at FROM sessions WHERE id = 'old'").Scan(&expiry)
	if expiry != "2026-07-27 12:34:56" {
		t.Fatalf("unexpected expiry %q", expiry)
	}
}

// On a database from before the column existed the ALTER succeeds, and only then
// does the one-time backfill run: imported tags all carry #457b9d, so a tag with
// any other colour was a deliberate choice.
func TestMigrateBackfillsTagSourceOnFirstAdd(t *testing.T) {
	database := newTestDatabase(t)
	var userID int
	database.QueryRow("SELECT id FROM users").Scan(&userID)
	database.Exec("INSERT INTO tags (user_id, name, color) VALUES (?, 'imported', '#457b9d')", userID)
	database.Exec("INSERT INTO tags (user_id, name, color) VALUES (?, 'own', '#ff0000')", userID)
	if _, failure := database.Exec("ALTER TABLE tags DROP COLUMN source"); failure != nil {
		t.Fatalf("simulate the old schema: %v", failure)
	}

	migrate(database)

	var importedSource, ownSource string
	database.QueryRow("SELECT source FROM tags WHERE name = 'imported'").Scan(&importedSource)
	database.QueryRow("SELECT source FROM tags WHERE name = 'own'").Scan(&ownSource)
	if importedSource != "import" {
		t.Fatalf("the imported tag was classified as %q", importedSource)
	}
	if ownSource != "manual" {
		t.Fatalf("the self-made tag was classified as %q", ownSource)
	}
}

// The backfill only runs on the boot that first adds tags.source, so on a later
// boot a manual tag must not be reclassified.
func TestMigrateBackfillsTagSourceOnlyOnce(t *testing.T) {
	database := newTestDatabase(t)
	var userID int
	database.QueryRow("SELECT id FROM users").Scan(&userID)
	database.Exec("INSERT INTO tags (user_id, name, color) VALUES (?, 'imported', '#457b9d')", userID)
	database.Exec("INSERT INTO tags (user_id, name, color) VALUES (?, 'own', '#ff0000')", userID)
	database.Exec("UPDATE tags SET source = 'manual' WHERE name = 'imported'")

	migrate(database)

	var source string
	database.QueryRow("SELECT source FROM tags WHERE name = 'imported'").Scan(&source)
	if source != "manual" {
		t.Fatalf("the backfill ran again and reset the tag to %q", source)
	}
}

func columnExists(t *testing.T, database *sql.DB, table, column string) bool {
	t.Helper()
	rows, failure := database.Query("PRAGMA table_info(" + table + ")")
	if failure != nil {
		t.Fatalf("read table info for %s: %v", table, failure)
	}
	defer rows.Close()
	for rows.Next() {
		var index int
		var name, columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if failure := rows.Scan(&index, &name, &columnType, &notNull, &defaultValue, &primaryKey); failure != nil {
			t.Fatalf("scan table info: %v", failure)
		}
		if name == column {
			return true
		}
	}
	return false
}

// The old seeds allowed 15 s (30 s for makerworld) - short enough to get an
// account rate-limited, and no longer accepted by the settings form. An
// existing installation must be lifted instead of keeping a value nobody can
// reproduce.
func TestMigrateLiftsCooldownsBelowTheMinimum(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("UPDATE app_settings SET value = '15' WHERE key = 'download_cooldown_printables'")
	database.Exec("UPDATE app_settings SET value = '45' WHERE key = 'download_cooldown_cults3d'")
	database.Exec("UPDATE app_settings SET value = '30' WHERE key = 'download_cooldown_makerworld'")

	migrate(database)

	for key, want := range map[string]string{
		"download_cooldown_printables": "30",  // lifted to the floor
		"download_cooldown_cults3d":    "45",  // above the floor, left alone
		"download_cooldown_makerworld": "300", // its own, much higher floor
	} {
		var value string
		database.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&value)
		if value != want {
			t.Fatalf("%s is %q, expected %q", key, value, want)
		}
	}
}

// A value the admin chose above the floor survives every restart - migrate runs
// on each boot.
func TestMigrateKeepsCooldownsAboveTheMinimum(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("UPDATE app_settings SET value = '120' WHERE key = 'download_cooldown_default'")

	migrate(database)
	migrate(database)

	var value string
	database.QueryRow("SELECT value FROM app_settings WHERE key = 'download_cooldown_default'").Scan(&value)
	if value != "120" {
		t.Fatalf("the chosen cooldown is %q", value)
	}
}

// The interval used to be stored but never read, so a member could have saved
// any number down to a single day. Now that the scheduler honours it, such a
// value would re-download a whole library every day.
func TestMigrateLiftsDesignUpdateIntervalsBelowTheFloor(t *testing.T) {
	database := newTestDatabase(t)
	seedMember(t, database, "kurz")
	seedMember(t, database, "lang")
	seedMember(t, database, "aus")
	database.Exec("INSERT INTO notification_prefs (user_id, sync_min_age_days) VALUES (2, 1), (3, 30), (4, NULL)")

	migrate(database)

	for userID, want := range map[int]any{2: int64(7), 3: int64(30), 4: nil} {
		var value any
		database.QueryRow("SELECT sync_min_age_days FROM notification_prefs WHERE user_id = ?", userID).Scan(&value)
		if value != want {
			t.Fatalf("user %d ended with %v, expected %v", userID, value, want)
		}
	}
}

// The floor comes from the setting, so raising it as an admin lifts everyone
// below it on the next boot.
func TestMigrateFollowsARaisedDesignUpdateFloor(t *testing.T) {
	database := newTestDatabase(t)
	seedMember(t, database, "kurz")
	database.Exec("INSERT INTO notification_prefs (user_id, sync_min_age_days) VALUES (2, 7)")
	database.Exec("UPDATE app_settings SET value = '30' WHERE key = 'design_update_min_days'")

	migrate(database)

	var days int
	database.QueryRow("SELECT sync_min_age_days FROM notification_prefs WHERE user_id = 2").Scan(&days)
	if days != 30 {
		t.Fatalf("the member kept %d days", days)
	}
}

// seedMember adds an active member; notification_prefs reference users.
func seedMember(t *testing.T, database *sql.DB, name string) {
	t.Helper()
	if _, failure := database.Exec("INSERT INTO users (email, hash, name, state, public_id) VALUES (NULL, 'x', ?, 'active', ?)", name, publicid.New()); failure != nil {
		t.Fatalf("seed member %s: %v", name, failure)
	}
}

// Every account needs an outward identifier: it addresses the account in every
// URL and every storage path, so a row without one is unreachable.
func TestSeededAdminHasAPublicID(t *testing.T) {
	database := newTestDatabase(t)

	var value string
	if failure := database.QueryRow("SELECT public_id FROM users WHERE name = 'admin'").Scan(&value); failure != nil {
		t.Fatalf("read the seeded admin: %v", failure)
	}
	if !publicid.Valid(value) {
		t.Fatalf("the seeded admin carries %q", value)
	}
}

// An installation from before the column gets its ids on the next boot. The
// backfill runs on every start, so an insert path that ever forgets the column
// heals itself instead of leaving an unaddressable account behind.
func TestMigrateBackfillsPublicIDForExistingRows(t *testing.T) {
	database := newTestDatabase(t)
	if _, failure := database.Exec(
		"INSERT INTO users (email, hash, name, state, public_id) VALUES ('old@example.org', 'x', 'old', 'active', '')"); failure != nil {
		t.Fatalf("insert a row without an id: %v", failure)
	}

	migrate(database)

	var value string
	if failure := database.QueryRow("SELECT public_id FROM users WHERE name = 'old'").Scan(&value); failure != nil {
		t.Fatalf("read the backfilled row: %v", failure)
	}
	if !publicid.Valid(value) {
		t.Fatalf("the row was not backfilled: %q", value)
	}
	var empty int
	database.QueryRow("SELECT COUNT(*) FROM users WHERE public_id = ''").Scan(&empty)
	if empty != 0 {
		t.Fatalf("%d rows are still without an id", empty)
	}
}

// Designs the publishing bug left without a cover: they kept their gallery, so
// the detail view showed the images while the overview card - which reads
// cover_path alone - stayed on the grey placeholder. The first gallery image is
// what the cover would have been.
func TestMigrateBackfillsMissingDesignCovers(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("INSERT INTO users (email, hash, name, state, public_id) VALUES ('o@example.org', 'x', 'o', 'active', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')")
	// Each row needs its own public id: the unique index makes a second one that
	// forgets the column fail outright, which is what keeps an unaddressable
	// design from ever being stored.
	database.Exec("INSERT INTO designs (id, public_id, user_id, name, source_platform) VALUES (1, ?, 1, 'Ohne Titelbild', 'thingiverse')", publicid.New())
	database.Exec("INSERT INTO design_images (design_id, path, sort_order) VALUES (1, '1/second.png', 1), (1, '1/first.png', 0)")
	// A design with no images at all has nothing to fall back on, and one with a
	// chosen cover must keep it.
	database.Exec("INSERT INTO designs (id, public_id, user_id, name, source_platform) VALUES (2, ?, 1, 'Ganz ohne Bild', 'manual')", publicid.New())
	database.Exec("INSERT INTO designs (id, public_id, user_id, name, source_platform, cover_path) VALUES (3, ?, 1, 'Mit Titelbild', 'thingiverse', '3/chosen.png')", publicid.New())
	database.Exec("INSERT INTO design_images (design_id, path, sort_order) VALUES (3, '3/other.png', 0)")

	migrate(database)

	var backfilled, untouched sql.NullString
	database.QueryRow("SELECT cover_path FROM designs WHERE id = 1").Scan(&backfilled)
	if backfilled.String != "1/first.png" {
		t.Fatalf("the cover was backfilled as %q, want the first gallery image", backfilled.String)
	}
	database.QueryRow("SELECT cover_path FROM designs WHERE id = 3").Scan(&untouched)
	if untouched.String != "3/chosen.png" {
		t.Fatalf("a chosen cover was overwritten with %q", untouched.String)
	}
	var imageless sql.NullString
	database.QueryRow("SELECT cover_path FROM designs WHERE id = 2").Scan(&imageless)
	if imageless.Valid && imageless.String != "" {
		t.Fatalf("a design without images was given the cover %q", imageless.String)
	}
}

// Designs whose card shows a cover while their gallery marks none: the flag the
// detail view reads was only ever written by the API paths, never by the
// downloader.
func TestMigrateMarksTheCoverImageInTheGallery(t *testing.T) {
	database := newTestDatabase(t)
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, failure := database.Exec(query, args...); failure != nil {
			t.Fatalf("fixture failed (%s): %v", query, failure)
		}
	}
	mustExec("INSERT INTO users (email, hash, name, state, public_id) VALUES ('o@example.org', 'x', 'o', 'active', ?)", publicid.New())
	// The cover is a hard link beside the pictures directory, so its path is not
	// one of the gallery paths: the first image is what the marker falls back to.
	mustExec("INSERT INTO designs (id, public_id, user_id, name, source_platform, cover_path) VALUES (1, ?, 1, 'Geladen', 'thingiverse', '1/cover.png')", publicid.New())
	mustExec("INSERT INTO design_images (design_id, path, sort_order) VALUES (1, '1/second.png', 1), (1, '1/first.png', 0)")
	// A design whose cover_path names one of its images marks that one.
	mustExec("INSERT INTO designs (id, public_id, user_id, name, source_platform, cover_path) VALUES (2, ?, 1, 'Mit Treffer', 'thingiverse', '2/second.png')", publicid.New())
	mustExec("INSERT INTO design_images (design_id, path, sort_order) VALUES (2, '2/first.png', 0), (2, '2/second.png', 1)")
	// A marker the owner set stays where it is.
	mustExec("INSERT INTO designs (id, public_id, user_id, name, source_platform, cover_path) VALUES (3, ?, 1, 'Selbst gewaehlt', 'manual', '3/chosen.png')", publicid.New())
	mustExec("INSERT INTO design_images (design_id, path, sort_order, is_cover) VALUES (3, '3/first.png', 0, 0), (3, '3/chosen.png', 1, 1)")

	migrate(database)

	for designID, want := range map[int]string{1: "1/first.png", 2: "2/second.png", 3: "3/chosen.png"} {
		var marked string
		var count int
		database.QueryRow("SELECT COUNT(*) FROM design_images WHERE design_id = ? AND is_cover = 1", designID).Scan(&count)
		database.QueryRow("SELECT COALESCE(path, '') FROM design_images WHERE design_id = ? AND is_cover = 1", designID).Scan(&marked)
		if count != 1 || marked != want {
			t.Errorf("design %d has %d marked images (%q), expected exactly %q", designID, count, marked, want)
		}
	}
}

// Two accounts sharing an id would collide in the storage tree, where the id is
// the directory name.
func TestPublicIDIsUnique(t *testing.T) {
	database := newTestDatabase(t)
	value := publicid.New()
	if _, failure := database.Exec(
		"INSERT INTO users (email, hash, name, state, public_id) VALUES ('a@example.org', 'x', 'a', 'active', ?)", value); failure != nil {
		t.Fatalf("first insert: %v", failure)
	}
	if _, failure := database.Exec(
		"INSERT INTO users (email, hash, name, state, public_id) VALUES ('b@example.org', 'x', 'b', 'active', ?)", value); failure == nil {
		t.Fatal("a duplicate public id was accepted")
	}
}
