package api

import (
	"database/sql"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

// testDB builds an in-memory schema with the columns the typed loaders read.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	database, failure := sql.Open("sqlite", ":memory:")
	if failure != nil {
		t.Fatalf("open: %v", failure)
	}
	// Same single-connection setup as production, so ":memory:" stays one DB.
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	schema := []string{
		`CREATE TABLE designs (id INTEGER PRIMARY KEY, user_id INTEGER, name TEXT,
			cover_path TEXT, source_url TEXT, source_platform TEXT)`,
		`CREATE TABLE design_files (id INTEGER PRIMARY KEY, design_id INTEGER, version TEXT,
			filename TEXT, path TEXT, size_bytes INTEGER, file_count INTEGER, is_current INTEGER)`,
		`CREATE TABLE design_file_entries (id INTEGER PRIMARY KEY, design_file_id INTEGER, filename TEXT,
			path TEXT, relative_path TEXT, file_hash TEXT, blob_hash TEXT, size_bytes INTEGER)`,
		`CREATE TABLE design_images (id INTEGER PRIMARY KEY, design_id INTEGER, path TEXT, sort_order INTEGER)`,
		// Design 1 belongs to user 1 and has a current version with two files;
		// design 2 belongs to user 2 and exists to check the isolation.
		`INSERT INTO designs (id, user_id, name, source_url) VALUES (1, 1, 'Cup', 'https://example.test/cup'), (2, 2, NULL, NULL)`,
		`INSERT INTO design_files (id, design_id, version, filename, path, size_bytes, file_count, is_current)
		 VALUES (10, 1, '2.0', 'cup.stl', '1/stl/1/2.0', 300, 2, 1), (11, 2, '1.0', 'x.stl', '2/stl/2/1.0', 5, 1, 0)`,
		`INSERT INTO design_file_entries (id, design_file_id, filename, path, relative_path, file_hash, blob_hash, size_bytes)
		 VALUES (100, 10, 'body.stl', '1/stl/1/2.0/parts/body.stl', 'parts/body.stl', 'aa', 'aa', 200),
			(101, 10, 'lid.stl', '1/stl/1/2.0/lid.stl', NULL, 'bb', NULL, 100),
			(102, 11, 'other.stl', '2/stl/2/1.0/other.stl', 'other.stl', 'cc', 'cc', 5)`,
		`INSERT INTO design_images (id, design_id, path, sort_order) VALUES (200, 1, '1/img.jpg', 3)`,
	}
	for _, statement := range schema {
		if _, failure := database.Exec(statement); failure != nil {
			t.Fatalf("schema %q: %v", statement, failure)
		}
	}
	return database
}

func TestLoadDesignSeparatesMissingFromError(t *testing.T) {
	database := testDB(t)

	design, found, failure := loadDesign(database, 1)
	if failure != nil || !found {
		t.Fatalf("design 1: found=%v failure=%v", found, failure)
	}
	if design.UserID != 1 || design.Name != "Cup" || design.SourceURL != "https://example.test/cup" {
		t.Fatalf("unexpected design: %+v", design)
	}
	// NULL columns must arrive as "" and not blow up the scan.
	other, found, failure := loadDesign(database, 2)
	if failure != nil || !found {
		t.Fatalf("design 2: found=%v failure=%v", found, failure)
	}
	if other.Name != "" || other.SourceURL != "" || other.StoredCoverPath != "" {
		t.Fatalf("NULL columns not empty: %+v", other)
	}
	if _, found, failure := loadDesign(database, 999); found || failure != nil {
		t.Fatalf("missing design: found=%v failure=%v (want false, nil)", found, failure)
	}
	// A broken query must be an error, never "not found" - that difference is the
	// whole point of the (row, found, err) signature.
	database.Close()
	if _, found, failure := loadDesign(database, 1); failure == nil || found {
		t.Fatalf("closed DB: found=%v failure=%v (want an error)", found, failure)
	}
}

func TestLoadFileVersion(t *testing.T) {
	database := testDB(t)

	version, found, failure := loadFileVersion(database, 10, 1)
	if failure != nil || !found {
		t.Fatalf("version 10: found=%v failure=%v", found, failure)
	}
	if !version.IsCurrent || version.Version != "2.0" || version.FileCount != 2 || version.SizeBytes != 300 {
		t.Fatalf("unexpected version: %+v", version)
	}
	// A version of a foreign design must not be reachable through design 1.
	if _, found, failure := loadFileVersion(database, 11, 1); found || failure != nil {
		t.Fatalf("foreign version: found=%v failure=%v (want false, nil)", found, failure)
	}
	if version, _, _ := loadFileVersionByID(database, 11); version.IsCurrent {
		t.Fatalf("is_current=0 read as current")
	}
}

func TestVersionEntriesOrderAndNulls(t *testing.T) {
	entries, failure := versionEntries(testDB(t), 10)
	if failure != nil {
		t.Fatalf("versionEntries: %v", failure)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	// ORDER BY relative_path: the NULL one sorts first.
	if entries[0].Filename != "lid.stl" || entries[0].RelativePath != "" || entries[0].BlobHash != "" {
		t.Fatalf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].RelativePath != "parts/body.stl" || entries[1].SizeBytes != 200 {
		t.Fatalf("unexpected second entry: %+v", entries[1])
	}
}

func TestRequireEntryStaysInsideItsDesign(t *testing.T) {
	server := &Server{DB: testDB(t)}

	recorder := httptest.NewRecorder()
	entry, ok := server.requireEntry(recorder, 100, 10, 1)
	if !ok || entry.StoredPath != "1/stl/1/2.0/parts/body.stl" {
		t.Fatalf("own entry: ok=%v entry=%+v", ok, entry)
	}
	// Entry 102 belongs to version 11 of design 2 - neither the wrong version nor
	// the wrong design may produce it.
	for _, attempt := range []struct{ entryID, fileID, designID int }{
		{102, 10, 1}, {102, 11, 1}, {100, 11, 2},
	} {
		recorder := httptest.NewRecorder()
		if _, ok := server.requireEntry(recorder, attempt.entryID, attempt.fileID, attempt.designID); ok {
			t.Fatalf("entry %d reachable via file %d/design %d", attempt.entryID, attempt.fileID, attempt.designID)
		}
		if recorder.Code != 404 {
			t.Fatalf("status %d, want 404", recorder.Code)
		}
	}
}

func TestRequireModelEntryPicksModelFile(t *testing.T) {
	database := testDB(t)
	if _, failure := database.Exec(`INSERT INTO design_file_entries (id, design_file_id, filename, path)
		VALUES (103, 10, 'readme.txt', '1/stl/1/2.0/readme.txt')`); failure != nil {
		t.Fatalf("insert: %v", failure)
	}
	server := &Server{DB: database}
	entry, ok := server.requireModelEntry(httptest.NewRecorder(), 10, 1)
	if !ok || entry.Filename != "body.stl" {
		t.Fatalf("ok=%v entry=%+v (want body.stl)", ok, entry)
	}
}

func TestRequireDesignImage(t *testing.T) {
	server := &Server{DB: testDB(t)}
	image, ok := server.requireDesignImage(httptest.NewRecorder(), 200, 1)
	if !ok || image.StoredPath != "1/img.jpg" || image.SortOrder != 3 {
		t.Fatalf("ok=%v image=%+v", ok, image)
	}
	recorder := httptest.NewRecorder()
	if _, ok := server.requireDesignImage(recorder, 200, 2); ok || recorder.Code != 404 {
		t.Fatalf("foreign design: ok=%v status=%d", ok, recorder.Code)
	}
}

func TestZipEntryName(t *testing.T) {
	cases := []struct {
		entry fileEntryRow
		want  string
	}{
		{fileEntryRow{RelativePath: "parts/body.stl", Filename: "body.stl"}, "parts/body.stl"},
		{fileEntryRow{Filename: "body.stl"}, "body.stl"},
		{fileEntryRow{RelativePath: "/../../etc/passwd", Filename: "passwd"}, "etc/passwd"},
		{fileEntryRow{RelativePath: "..", Filename: ""}, "file"},
	}
	for _, testCase := range cases {
		if got := zipEntryName(testCase.entry); got != testCase.want {
			t.Errorf("zipEntryName(%+v) = %q, want %q", testCase.entry, got, testCase.want)
		}
	}
}
