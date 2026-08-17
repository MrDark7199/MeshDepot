package platforms

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver (driver name "sqlite").
)

// TestPlatformForURL pins the host mapping. A platform's CDN and login hosts have
// to count against the platform itself, otherwise the busiest requests - file
// transfers and the login - stay invisible.
func TestPlatformForURL(t *testing.T) {
	cases := []struct{ rawURL, want string }{
		{"https://makerworld.com/api/v1/design-service/design/1", "makerworld"},
		{"https://makerworld.bblmw.com/makerworld/model/US4/instance/x.3mf", "makerworld"},
		{"https://api.bambulab.com/v1/user-service/user/login", "makerworld"},
		{"https://bambulab.com/api/sign-in/tfa", "makerworld"},
		{"https://www.printables.com/model/42", "printables"},
		{"https://account.prusa3d.com/o/authorize/", "printables"},
		{"https://api.thingiverse.com/things/1", "thingiverse"},
		{"https://cults3d.com/graphql", "cults3d"},
		{"https://www.myminifactory.com/object/1", "myminifactory"},
		{"https://thangs.com/m/1", "thangs"},
		// Not ours: must not be counted against anyone.
		{"https://example.com/thing", ""},
		{"https://notmakerworld.com.evil.test/x", ""},
		{"", ""},
		{"::not a url::", ""},
	}
	for _, testCase := range cases {
		if got := platformForURL(testCase.rawURL); got != testCase.want {
			t.Errorf("platformForURL(%q) = %q, want %q", testCase.rawURL, got, testCase.want)
		}
	}
}

// TestRecordRequestIsNoOpWithoutRecorder guards the default: without wiring, a
// record call must not panic and must not touch anything.
func TestRecordRequestIsNoOpWithoutRecorder(t *testing.T) {
	activeRecorder.Store(nil)
	recordRequest("https://makerworld.com/x", requestKindAPI, 200)
	RecordBrowserRequest("https://makerworld.com/y")
}

// TestRecorderCountsAndFlushes checks that counted requests reach the table and
// that foreign hosts are dropped.
func TestRecorderCountsAndFlushes(t *testing.T) {
	database, failure := sql.Open("sqlite", "file:"+t.TempDir()+"/counter.db")
	if failure != nil {
		t.Fatalf("open: %v", failure)
	}
	defer database.Close()
	if _, failure := database.Exec(`CREATE TABLE platform_requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT, platform TEXT NOT NULL,
		kind TEXT NOT NULL, status INTEGER NOT NULL DEFAULT 0, at TEXT NOT NULL)`); failure != nil {
		t.Fatalf("schema: %v", failure)
	}

	recorder := &requestRecorder{db: database, summary: map[string]int{}, lastSummary: time.Now(), lastPrune: time.Now()}
	activeRecorder.Store(recorder)
	t.Cleanup(func() { activeRecorder.Store(nil) })

	recordRequest("https://makerworld.com/api/v1/design-service/design/1", requestKindAPI, 200)
	recordRequest("https://makerworld.bblmw.com/file.3mf", requestKindDownload, 200)
	RecordBrowserRequest("https://makerworld.com/en/models/1")
	recordRequest("https://example.com/ignored", requestKindAPI, 200)
	recordRequest("https://www.printables.com/model/42", requestKindAPI, 403)
	recorder.flush()

	var total int
	if failure := database.QueryRow("SELECT COUNT(*) FROM platform_requests").Scan(&total); failure != nil {
		t.Fatalf("count: %v", failure)
	}
	if total != 4 {
		t.Errorf("stored %d rows, want 4 (the foreign host must be dropped)", total)
	}

	var makerworld int
	database.QueryRow("SELECT COUNT(*) FROM platform_requests WHERE platform='makerworld'").Scan(&makerworld)
	if makerworld != 3 {
		t.Errorf("makerworld rows = %d, want 3", makerworld)
	}

	var kind string
	var status int
	database.QueryRow("SELECT kind, status FROM platform_requests WHERE platform='printables'").Scan(&kind, &status)
	if kind != string(requestKindAPI) || status != 403 {
		t.Errorf("printables row = (%q, %d), want (%q, 403)", kind, status, requestKindAPI)
	}

	// A page load is recorded as such - that is the volume the job cooldown missed.
	var pages int
	database.QueryRow("SELECT COUNT(*) FROM platform_requests WHERE kind='page'").Scan(&pages)
	if pages != 1 {
		t.Errorf("page rows = %d, want 1", pages)
	}
}
