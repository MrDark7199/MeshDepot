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
