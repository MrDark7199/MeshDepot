package dbutil

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestDatabase opens a real SQLite file with one table that covers every
// value kind the helpers have to normalize.
func newTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, failure := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "dbutil.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1)
	if _, failure := database.Exec(`
		CREATE TABLE designs (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			size INTEGER,
			rating REAL,
			cover BLOB,
			note TEXT
		)`); failure != nil {
		t.Fatalf("create table: %v", failure)
	}
	return database
}

func TestQueryMapsReturnsEveryRow(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("INSERT INTO designs (id, name, size) VALUES (1, 'first', 10), (2, 'second', 20)")

	rows, failure := QueryMaps(database, "SELECT id, name, size FROM designs ORDER BY id")
	if failure != nil {
		t.Fatalf("query: %v", failure)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0]["name"] != "first" || rows[1]["name"] != "second" {
		t.Fatalf("unexpected rows %v", rows)
	}
	if rows[0]["id"] != int64(1) || rows[0]["size"] != int64(10) {
		t.Fatalf("integers were not read as int64: %#v", rows[0])
	}
}

// A caller that ranges over the result must not have to nil-check it.
func TestQueryMapsReturnsEmptySliceNotNil(t *testing.T) {
	database := newTestDatabase(t)
	rows, failure := QueryMaps(database, "SELECT id FROM designs")
	if failure != nil {
		t.Fatalf("query: %v", failure)
	}
	if rows == nil {
		t.Fatal("an empty result returned nil instead of an empty slice")
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

// BLOB columns arrive as []byte and would end up base64-encoded in the JSON.
func TestQueryMapsNormalizesBlobsToString(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("INSERT INTO designs (id, name, cover) VALUES (1, 'first', ?)", []byte("raw bytes"))

	rows, _ := QueryMaps(database, "SELECT cover FROM designs")
	if rows[0]["cover"] != "raw bytes" {
		t.Fatalf("the blob was not normalized: %#v", rows[0]["cover"])
	}
}

func TestQueryMapsKeepsNullAsNil(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("INSERT INTO designs (id, name) VALUES (1, 'first')")

	rows, _ := QueryMaps(database, "SELECT size, note FROM designs")
	if rows[0]["size"] != nil || rows[0]["note"] != nil {
		t.Fatalf("NULL was not mapped to nil: %#v", rows[0])
	}
}

func TestQueryMapsPassesArguments(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("INSERT INTO designs (id, name) VALUES (1, 'first'), (2, 'second')")

	rows, failure := QueryMaps(database, "SELECT name FROM designs WHERE id = ?", 2)
	if failure != nil {
		t.Fatalf("query: %v", failure)
	}
	if len(rows) != 1 || rows[0]["name"] != "second" {
		t.Fatalf("the argument was not applied: %v", rows)
	}
}

func TestQueryMapsReportsBrokenStatement(t *testing.T) {
	database := newTestDatabase(t)
	if _, failure := QueryMaps(database, "SELECT * FROM table_that_does_not_exist"); failure == nil {
		t.Fatal("a broken statement did not report an error")
	}
}

// found=false and a failure must stay distinguishable - a caller that treats a
// locked database as "not found" deletes live data.
func TestQueryMapSeparatesNotFoundFromFailure(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("INSERT INTO designs (id, name) VALUES (1, 'first')")

	row, found, failure := QueryMap(database, "SELECT name FROM designs WHERE id = ?", 1)
	if failure != nil || !found || row["name"] != "first" {
		t.Fatalf("the existing row was not returned: %v/%v/%v", row, found, failure)
	}

	row, found, failure = QueryMap(database, "SELECT name FROM designs WHERE id = ?", 99)
	if failure != nil {
		t.Fatalf("a matchless query reported an error: %v", failure)
	}
	if found || row != nil {
		t.Fatalf("a matchless query reported found: %v/%v", row, found)
	}

	row, found, failure = QueryMap(database, "SELECT * FROM table_that_does_not_exist")
	if failure == nil {
		t.Fatal("a broken statement did not report an error")
	}
	if found || row != nil {
		t.Fatalf("a broken statement reported found: %v/%v", row, found)
	}
}

func TestQueryMapReturnsTheFirstRow(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("INSERT INTO designs (id, name) VALUES (1, 'first'), (2, 'second')")

	row, found, _ := QueryMap(database, "SELECT name FROM designs ORDER BY id")
	if !found || row["name"] != "first" {
		t.Fatalf("expected the first row, got %v", row)
	}
}

func TestExecLoggedWritesAndSurvivesFailure(t *testing.T) {
	database := newTestDatabase(t)

	ExecLogged(database, "INSERT INTO designs (id, name) VALUES (?, ?)", 1, "first")
	var count int
	database.QueryRow("SELECT COUNT(*) FROM designs").Scan(&count)
	if count != 1 {
		t.Fatalf("the row was not written: %d", count)
	}

	ExecLogged(database, "INSERT INTO table_that_does_not_exist (id) VALUES (1)")
}

func TestExecLoggedWorksInsideTransaction(t *testing.T) {
	database := newTestDatabase(t)
	transaction, failure := database.Begin()
	if failure != nil {
		t.Fatalf("begin: %v", failure)
	}

	ExecLogged(transaction, "INSERT INTO designs (id, name) VALUES (1, 'first')")
	if failure := transaction.Commit(); failure != nil {
		t.Fatalf("commit: %v", failure)
	}

	var count int
	database.QueryRow("SELECT COUNT(*) FROM designs").Scan(&count)
	if count != 1 {
		t.Fatalf("the transaction wrote %d rows", count)
	}
}

func TestQueryHeadShortensLongStatements(t *testing.T) {
	short := "SELECT id FROM designs"
	if head := queryHead("  SELECT   id\n\tFROM designs "); head != short {
		t.Fatalf("whitespace was not collapsed: %q", head)
	}

	long := "SELECT id, name, size, rating, cover, note FROM designs WHERE id = ?"
	head := queryHead(long)
	if !strings.HasSuffix(head, "…") {
		t.Fatalf("a long statement was not shortened: %q", head)
	}
	if len(strings.Fields(head)) != 9 {
		t.Fatalf("expected 8 words plus the ellipsis, got %q", head)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("INSERT INTO designs (id, name) VALUES (1, 'first')")

	_, failure := database.Exec("INSERT INTO designs (id, name) VALUES (2, 'first')")
	if !IsUniqueViolation(failure) {
		t.Fatalf("a duplicate name was not recognised: %v", failure)
	}
	if IsUniqueViolation(nil) {
		t.Fatal("nil was reported as a unique violation")
	}
	if IsUniqueViolation(errors.New("database is locked")) {
		t.Fatal("an unrelated error was reported as a unique violation")
	}
}
