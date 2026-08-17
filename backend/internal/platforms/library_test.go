package platforms

import (
	"bytes"
	"database/sql"
	"log"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // CGO-free SQLite driver (driver name "sqlite").
)

func TestExtractLibSourceID(t *testing.T) {
	cases := []struct{ url, platform, want string }{
		{"https://www.printables.com/model/12345-foo", "printables", "12345"},
		{"https://www.thingiverse.com/thing:678", "thingiverse", "678"},
		{"https://makerworld.com/en/models/999", "makerworld", "999"},
		{"https://www.myminifactory.com/object/abc123", "myminifactory", "abc123"},
		{"https://thangs.com/model/55", "thangs", "55"},
		// cults3d: last path segment incl. hyphens (= source_id of the download).
		{"https://cults3d.com/en/3d-model/game/widget77", "cults3d", "widget77"},
		{"https://cults3d.com/en/3d-model/game/widget-77", "cults3d", "widget-77"},
	}
	for _, testCase := range cases {
		if got := extractLibSourceID(testCase.url, testCase.platform); got != testCase.want {
			t.Errorf("extractLibSourceID(%q,%q) = %q, want %q", testCase.url, testCase.platform, got, testCase.want)
		}
	}
}

func TestPlatformLabel(t *testing.T) {
	if Label("makerworld") != "MakerWorld" || Label("cults3d") != "Cults3D" {
		t.Error("platformLabel wrong")
	}
}

func TestPrParsePrintList(t *testing.T) {
	body := []byte(`{"data":{"moreLikedPrints2":{"cursor":"NEXT","items":[{"print":{"id":1}},{"print":{"id":2}}]}}}`)
	urls, cursor := printablesParsePrintList(body, "moreLikedPrints2")
	if cursor != "NEXT" || len(urls) != 2 || urls[0] != "https://www.printables.com/model/1" {
		t.Errorf("printablesParsePrintList = %v cursor=%q", urls, cursor)
	}
}

func TestMwHitID(t *testing.T) {
	if makerworldHitID(map[string]any{"id": float64(42)}) != "42" {
		t.Error("makerworldHitID direct wrong")
	}
	if makerworldHitID(map[string]any{"design": map[string]any{"id": float64(7)}}) != "7" {
		t.Error("makerworldHitID nested wrong")
	}
}

// newSettingsDatabase builds the smallest database LibrarySyncEnabled needs:
// the key/value table the admin settings live in.
func newSettingsDatabase(t *testing.T, value string) *sql.DB {
	t.Helper()
	database, failure := sql.Open("sqlite", filepath.Join(t.TempDir(), "settings.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	if _, failure = database.Exec("CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)"); failure != nil {
		t.Fatalf("create table: %v", failure)
	}
	if value != "" {
		if _, failure = database.Exec("INSERT INTO app_settings (key, value) VALUES ('library_sync_enabled', ?)", value); failure != nil {
			t.Fatalf("insert setting: %v", failure)
		}
	}
	return database
}

// The switch decides for the whole server, so its default matters: an
// installation that never saw the setting keeps syncing.
func TestLibrarySyncEnabledDefaultsToOn(t *testing.T) {
	if !LibrarySyncEnabled(newSettingsDatabase(t, "")) {
		t.Fatal("a missing row switched the sync off")
	}
	if !LibrarySyncEnabled(newSettingsDatabase(t, "1")) {
		t.Fatal("'1' switched the sync off")
	}
	if LibrarySyncEnabled(newSettingsDatabase(t, "0")) {
		t.Fatal("'0' did not switch the sync off")
	}
}

// captureLog collects what the sync writes while fn runs; the log line is the
// only observable the gate produces.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	defer log.SetOutput(previous)
	fn()
	return buffer.String()
}

// The scheduler, the admin force flag and the manual trigger all end up in
// these two functions, so the gate sits inside them: a forgotten check upstream
// must not be able to start a sync the admin switched off.
func TestRunLibrarySyncStopsWhileDisabled(t *testing.T) {
	deps := Deps{DB: newSettingsDatabase(t, "0")}

	for name, run := range map[string]func(){
		"all":     func() { RunLibrarySync(deps) },
		"oneUser": func() { RunLibrarySyncFor(deps, 1, "printables") },
	} {
		if output := captureLog(t, run); !strings.Contains(output, "disabled server-side") {
			t.Fatalf("%s did not stop at the switch: %q", name, output)
		}
	}
}

// The counter-test: with the switch on, the same call walks past the gate and
// only stops at the (here empty) account list.
func TestRunLibrarySyncPassesTheGateWhileEnabled(t *testing.T) {
	deps := Deps{DB: newSettingsDatabase(t, "1")}

	output := captureLog(t, func() { RunLibrarySync(deps) })

	if strings.Contains(output, "disabled server-side") {
		t.Fatalf("the enabled sync stopped at the switch: %q", output)
	}
	if !strings.Contains(output, "no active accounts") {
		t.Fatalf("the sync did not reach the account list: %q", output)
	}
}

// newCollectionsDatabase builds the part of the schema findOrCreateCollection
// touches, including the trigger, so a stray UPDATE shows up in updated_at.
func newCollectionsDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, failure := sql.Open("sqlite", filepath.Join(t.TempDir(), "collections.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	if _, failure = database.Exec(`CREATE TABLE collections (
		id                            INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id                       INTEGER NOT NULL,
		name                          TEXT    NOT NULL,
		source_platform               TEXT    DEFAULT NULL,
		source_platform_collection_id TEXT    DEFAULT NULL,
		source_name                   TEXT    DEFAULT NULL,
		updated_at                    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP)`); failure != nil {
		t.Fatalf("create table: %v", failure)
	}
	return database
}

// collectionRow reads back what the sync stored under an id.
func collectionRow(t *testing.T, database *sql.DB, id int) (name string, sourceName sql.NullString) {
	t.Helper()
	if failure := database.QueryRow("SELECT name, source_name FROM collections WHERE id=?", id).
		Scan(&name, &sourceName); failure != nil {
		t.Fatalf("read collection %d: %v", id, failure)
	}
	return name, sourceName
}

// The first sight of a collection stores the prefixed name for the UI and the
// bare platform name as the reference every later comparison runs against.
func TestFindOrCreateCollectionCreatesWithSourceName(t *testing.T) {
	database := newCollectionsDatabase(t)

	id := findOrCreateCollection(database, 1, "printables", "col-1", "Deko")

	if id == 0 {
		t.Fatal("no collection was created")
	}
	name, sourceName := collectionRow(t, database, id)
	if name != "[Printables] Deko" {
		t.Errorf("name = %q, want %q", name, "[Printables] Deko")
	}
	if !sourceName.Valid || sourceName.String != "Deko" {
		t.Errorf("source_name = %v, want %q", sourceName, "Deko")
	}
}

// The match runs over the platform id. A renamed collection must land on the
// existing row, otherwise the designs stay behind on an orphaned copy.
func TestFindOrCreateCollectionMatchesByIDNotName(t *testing.T) {
	database := newCollectionsDatabase(t)
	first := findOrCreateCollection(database, 1, "printables", "col-1", "Deko")

	second := findOrCreateCollection(database, 1, "printables", "col-1", "Dekoration")

	if second != first {
		t.Fatalf("a rename created a second collection: %d != %d", second, first)
	}
	var count int
	database.QueryRow("SELECT COUNT(*) FROM collections").Scan(&count)
	if count != 1 {
		t.Fatalf("collections in the table: %d, want 1", count)
	}
}

// Nobody touched the name locally, so the rename on the platform comes through.
func TestFindOrCreateCollectionFollowsPlatformRename(t *testing.T) {
	database := newCollectionsDatabase(t)
	id := findOrCreateCollection(database, 1, "printables", "col-1", "Deko")

	findOrCreateCollection(database, 1, "printables", "col-1", "Dekoration")

	name, sourceName := collectionRow(t, database, id)
	if name != "[Printables] Dekoration" {
		t.Errorf("name = %q, want %q", name, "[Printables] Dekoration")
	}
	if sourceName.String != "Dekoration" {
		t.Errorf("source_name = %q, want %q", sourceName.String, "Dekoration")
	}
}

// The user renamed it here. Their name wins - but source_name still follows the
// platform, so a later comparison comes out right instead of drifting.
func TestFindOrCreateCollectionKeepsLocalRename(t *testing.T) {
	database := newCollectionsDatabase(t)
	id := findOrCreateCollection(database, 1, "printables", "col-1", "Deko")
	if _, failure := database.Exec("UPDATE collections SET name='Meine Deko' WHERE id=?", id); failure != nil {
		t.Fatalf("local rename: %v", failure)
	}

	findOrCreateCollection(database, 1, "printables", "col-1", "Dekoration")

	name, sourceName := collectionRow(t, database, id)
	if name != "Meine Deko" {
		t.Errorf("the sync overwrote the local name: %q", name)
	}
	if sourceName.String != "Dekoration" {
		t.Errorf("source_name = %q, want %q", sourceName.String, "Dekoration")
	}
}

// A local rename that keeps the prefix used to be indistinguishable from an
// untouched name, which is exactly what source_name settles.
func TestFindOrCreateCollectionKeepsLocalRenameWithPrefix(t *testing.T) {
	database := newCollectionsDatabase(t)
	id := findOrCreateCollection(database, 1, "printables", "col-1", "Deko")
	if _, failure := database.Exec("UPDATE collections SET name='[Printables] Meine Deko' WHERE id=?", id); failure != nil {
		t.Fatalf("local rename: %v", failure)
	}

	findOrCreateCollection(database, 1, "printables", "col-1", "Dekoration")

	if name, _ := collectionRow(t, database, id); name != "[Printables] Meine Deko" {
		t.Errorf("the sync overwrote the local name: %q", name)
	}
}

// Rows from before the column carry no reference name. Guessing one could hand
// the next rename a name the platform never owned, so the local name is left
// alone and only the reference is filled in.
func TestFindOrCreateCollectionBackfillsLegacyRow(t *testing.T) {
	database := newCollectionsDatabase(t)
	result, failure := database.Exec(
		`INSERT INTO collections (user_id, name, source_platform, source_platform_collection_id)
		 VALUES (1,'[Printables] Deko','printables','col-1')`)
	if failure != nil {
		t.Fatalf("insert legacy row: %v", failure)
	}
	legacyID, _ := result.LastInsertId()

	if id := findOrCreateCollection(database, 1, "printables", "col-1", "Dekoration"); int64(id) != legacyID {
		t.Fatalf("the legacy row was not reused: %d != %d", id, legacyID)
	}

	name, sourceName := collectionRow(t, database, int(legacyID))
	if name != "[Printables] Deko" {
		t.Errorf("the local name moved on a row without a reference: %q", name)
	}
	if sourceName.String != "Dekoration" {
		t.Errorf("source_name = %q, want %q", sourceName.String, "Dekoration")
	}

	// With the reference in place the next rename comes through as usual.
	findOrCreateCollection(database, 1, "printables", "col-1", "Deko")
	if name, _ = collectionRow(t, database, int(legacyID)); name != "[Printables] Deko" {
		t.Errorf("name after the healed row was renamed = %q", name)
	}
}

// The rule lives in one shared function, but every platform reaches it through
// its own fetcher, and each one has to bring a stable collection id along. This
// walks the same rename twice per platform - once untouched, once renamed here -
// so a fetcher that ever starts handing over a name instead of an id fails here.
func TestFindOrCreateCollectionRuleHoldsForEveryPlatform(t *testing.T) {
	cases := []struct{ platform, label string }{
		{"thingiverse", "Thingiverse"},
		{"makerworld", "MakerWorld"},
		{"myminifactory", "MyMiniFactory"},
		{"printables", "Printables"},
		{"thangs", "Thangs"},
		{"cults3d", "Cults3D"},
	}
	for _, testCase := range cases {
		t.Run(testCase.platform, func(t *testing.T) {
			database := newCollectionsDatabase(t)

			untouched := findOrCreateCollection(database, 1, testCase.platform, "col-1", "Deko")
			if name, _ := collectionRow(t, database, untouched); name != "["+testCase.label+"] Deko" {
				t.Fatalf("created name = %q, want %q", name, "["+testCase.label+"] Deko")
			}
			if same := findOrCreateCollection(database, 1, testCase.platform, "col-1", "Dekoration"); same != untouched {
				t.Fatalf("the rename created a second collection: %d != %d", same, untouched)
			}
			if name, _ := collectionRow(t, database, untouched); name != "["+testCase.label+"] Dekoration" {
				t.Errorf("the rename did not come through: %q", name)
			}

			renamed := findOrCreateCollection(database, 1, testCase.platform, "col-2", "Technik")
			if _, failure := database.Exec("UPDATE collections SET name='Meins' WHERE id=?", renamed); failure != nil {
				t.Fatalf("local rename: %v", failure)
			}
			findOrCreateCollection(database, 1, testCase.platform, "col-2", "Technik neu")
			if name, _ := collectionRow(t, database, renamed); name != "Meins" {
				t.Errorf("the sync overwrote the local name: %q", name)
			}
		})
	}
}

// Nothing changed, so nothing may be written: the trigger on updated_at turns a
// pointless UPDATE into a visible change on every sync run.
func TestFindOrCreateCollectionIsIdempotent(t *testing.T) {
	database := newCollectionsDatabase(t)
	id := findOrCreateCollection(database, 1, "printables", "col-1", "Deko")
	if _, failure := database.Exec("UPDATE collections SET updated_at='2000-01-01 00:00:00' WHERE id=?", id); failure != nil {
		t.Fatalf("set marker: %v", failure)
	}

	findOrCreateCollection(database, 1, "printables", "col-1", "Deko")

	var updatedAt string
	database.QueryRow("SELECT updated_at FROM collections WHERE id=?", id).Scan(&updatedAt)
	if updatedAt != "2000-01-01 00:00:00" {
		t.Errorf("an unchanged collection was written again: updated_at = %q", updatedAt)
	}
}
