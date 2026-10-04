package api

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"meshdepot/internal/coerce"
)

// folderPath is where a file sits inside its version, as the UI and the ZIP
// export read it.
func (testHarness *harness) folderPath(entryID int) string {
	testHarness.t.Helper()
	var path string
	if failure := testHarness.database.QueryRow(
		"SELECT COALESCE(relative_path, '') FROM design_file_entries WHERE id = ?", entryID).Scan(&path); failure != nil {
		testHarness.t.Fatalf("read the relative path: %v", failure)
	}
	return path
}

// TestFilesMoveIntoAFolder covers the whole point of folders: the file ends up
// somewhere else without its bytes being touched, and the old place is gone.
func TestFilesMoveIntoAFolder(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Ordnern")
	version := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes}).data(t)
	versionID := coerce.Int(version["id"])
	entryID := testHarness.scalarInt("SELECT id FROM design_file_entries WHERE design_file_id = ?", versionID)
	before := testHarness.storedFile("SELECT path FROM design_file_entries WHERE id = ?", entryID)

	answer := testHarness.asUser(http.MethodPut,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d/folder", testHarness.designPID(designID), versionID, entryID),
		map[string]any{"folder": "STL Files"})

	if answer.status != http.StatusOK {
		t.Fatalf("the move answered %d: %s", answer.status, answer.rawBody)
	}
	if path := testHarness.folderPath(entryID); path != "STL Files/wuerfel.stl" {
		t.Fatalf("the file sits at %q", path)
	}
	after := testHarness.storedFile("SELECT path FROM design_file_entries WHERE id = ?", entryID)
	content, failure := os.ReadFile(after)
	if failure != nil {
		t.Fatalf("the file is not where its row says: %v", failure)
	}
	if string(content) != string(stlBytes) {
		t.Fatal("the bytes changed on the way into the folder")
	}
	if _, failure := os.Stat(before); failure == nil {
		t.Fatal("the file is still in its old place as well")
	}
}

// A folder must not end up with two files of one name: on disk one would
// overwrite the other, and in the ZIP export most unpackers keep only one.
func TestFilesMoveRefusesATakenName(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Ordnern")
	version := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes}).data(t)
	versionID := coerce.Int(version["id"])
	testHarness.addEntries(designID, versionID, map[string][]byte{"kugel.stl": stlBytesAlternate})

	entries, failure := testHarness.database.Query("SELECT id, filename FROM design_file_entries WHERE design_file_id = ? ORDER BY id", versionID)
	if failure != nil {
		t.Fatalf("read the entries: %v", failure)
	}
	ids := map[string]int{}
	for entries.Next() {
		var id int
		var filename string
		if failure := entries.Scan(&id, &filename); failure != nil {
			t.Fatalf("scan an entry: %v", failure)
		}
		ids[filename] = id
	}
	entries.Close()

	move := func(entryID int, folder string) response {
		return testHarness.asUser(http.MethodPut,
			fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d/folder", testHarness.designPID(designID), versionID, entryID),
			map[string]any{"folder": folder})
	}
	if answer := move(ids["wuerfel.stl"], "Teile"); answer.status != http.StatusOK {
		t.Fatalf("the first move answered %d: %s", answer.status, answer.rawBody)
	}
	// Renaming is not what this endpoint does, so a second "wuerfel.stl" can only
	// come from somewhere else - here, a copy uploaded under the same name.
	testHarness.database.Exec(
		"UPDATE design_file_entries SET filename = 'wuerfel.stl', relative_path = 'wuerfel.stl' WHERE id = ?", ids["kugel.stl"])

	answer := move(ids["kugel.stl"], "Teile")

	if answer.status != http.StatusConflict {
		t.Fatalf("the clashing move answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.filename_exists" {
		t.Fatalf("the clash produced the error key %q", key)
	}
	if path := testHarness.folderPath(ids["kugel.stl"]); path != "wuerfel.stl" {
		t.Fatalf("the refused file moved anyway, to %q", path)
	}
}

// Deleting a folder is about the folder, not about what is in it: the files come
// back up, keeping whatever structure sat below the folder.
func TestFoldersDeleteKeepsTheFiles(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Ordnern")
	archive := zipArchive(t, map[string][]byte{
		"Teile/klein/wuerfel.stl": stlBytes,
		"Teile/kugel.stl":         stlBytesAlternate,
	})
	version := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"teile.zip": archive}).data(t)
	versionID := coerce.Int(version["id"])

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/folders", testHarness.designPID(designID), versionID),
		map[string]any{"path": "Teile"})

	if answer.status != http.StatusOK {
		t.Fatalf("the delete answered %d: %s", answer.status, answer.rawBody)
	}
	paths := testHarness.relativePaths(versionID)
	if !paths["kugel.stl"] || !paths["klein/wuerfel.stl"] {
		t.Fatalf("the files are at %v", paths)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_file_entries WHERE design_file_id = ?", versionID); count != 2 {
		t.Fatalf("%d files are left", count)
	}
}

// The other way, and only when asked for in so many words: the folder goes and
// its files with it.
func TestFoldersDeleteCanTakeTheFilesAlong(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Ordnern")
	archive := zipArchive(t, map[string][]byte{
		"Teile/wuerfel.stl": stlBytes,
		"kugel.stl":         stlBytesAlternate,
	})
	version := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"teile.zip": archive}).data(t)
	versionID := coerce.Int(version["id"])
	doomed := testHarness.storedFile("SELECT path FROM design_file_entries WHERE design_file_id = ? AND filename = 'wuerfel.stl'", versionID)

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/folders", testHarness.designPID(designID), versionID),
		map[string]any{"path": "Teile", "delete_files": true})

	if answer.status != http.StatusOK {
		t.Fatalf("the delete answered %d: %s", answer.status, answer.rawBody)
	}
	paths := testHarness.relativePaths(versionID)
	if len(paths) != 1 || !paths["kugel.stl"] {
		t.Fatalf("the version is left with %v", paths)
	}
	if _, failure := os.Stat(doomed); failure == nil {
		t.Fatal("the file is still on disk")
	}
	// The counters are nothing that recomputes itself: a wrong one stays wrong.
	if count := testHarness.scalarInt("SELECT file_count FROM design_files WHERE id = ?", versionID); count != 1 {
		t.Fatalf("the version counts %d files", count)
	}
	if size := testHarness.scalarInt("SELECT size_bytes FROM design_files WHERE id = ?", versionID); size != len(stlBytesAlternate) {
		t.Fatalf("the version reports %d bytes", size)
	}
}

// The order a folder was dragged into has to survive the next read, or the files
// jump back the moment the page reloads.
func TestEntriesKeepTheOrderTheyWereDraggedInto(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Ordnern")
	archive := zipArchive(t, map[string][]byte{"a.stl": stlBytes, "b.stl": stlBytesAlternate, "c.stl": []byte("solid c\nendsolid c\n")})
	version := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"teile.zip": archive}).data(t)
	versionID := coerce.Int(version["id"])

	named := map[string]int{}
	rows, failure := testHarness.database.Query("SELECT id, filename FROM design_file_entries WHERE design_file_id = ?", versionID)
	if failure != nil {
		t.Fatalf("read the entries: %v", failure)
	}
	for rows.Next() {
		var id int
		var filename string
		if failure := rows.Scan(&id, &filename); failure != nil {
			t.Fatalf("scan an entry: %v", failure)
		}
		named[filename] = id
	}
	rows.Close()

	answer := testHarness.asUser(http.MethodPut,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/order", testHarness.designPID(designID), versionID),
		map[string]any{"entry_ids": []int{named["c.stl"], named["a.stl"], named["b.stl"]}})
	if answer.status != http.StatusOK {
		t.Fatalf("the arrangement answered %d: %s", answer.status, answer.rawBody)
	}

	listed := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/files", testHarness.designPID(designID)), nil)
	first, _ := listed.list(t)[0].(map[string]any)
	entries, _ := first["entries"].([]any)
	var order []string
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		order = append(order, coerce.StringOr(entry["filename"], ""))
	}
	if len(order) != 3 || order[0] != "c.stl" || order[1] != "a.stl" || order[2] != "b.stl" {
		t.Fatalf("the files come back as %v", order)
	}

	// An id from somewhere else must not be able to renumber this version.
	other := testHarness.insertDesign(testHarness.userID, "Anderes")
	otherVersion := testHarness.uploadVersion(other, "1.0", "", map[string][]byte{"fremd.stl": stlBytes}).data(t)
	strayID := testHarness.scalarInt("SELECT id FROM design_file_entries WHERE design_file_id = ?", coerce.Int(otherVersion["id"]))
	refused := testHarness.asUser(http.MethodPut,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/order", testHarness.designPID(designID), versionID),
		map[string]any{"entry_ids": []int{strayID}})
	if refused.status != http.StatusUnprocessableEntity {
		t.Fatalf("a stray id answered %d", refused.status)
	}
}

// An empty folder has no file to describe it, so it is kept as a row - and has
// to come back with the version.
func TestFoldersCreateSurvivesWithoutFiles(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Ordnern")
	version := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes}).data(t)
	versionID := coerce.Int(version["id"])

	created := testHarness.asUser(http.MethodPost,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/folders", testHarness.designPID(designID), versionID),
		map[string]any{"path": "../../Supports/"})
	if created.status != http.StatusCreated {
		t.Fatalf("creating the folder answered %d: %s", created.status, created.rawBody)
	}
	if path := coerce.StringOr(created.data(t)["path"], ""); path != "Supports" {
		t.Fatalf("the folder was stored as %q - the path must not reach out of the version", path)
	}

	listed := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/files", testHarness.designPID(designID)), nil)
	versions := listed.list(t)
	if len(versions) != 1 {
		t.Fatalf("%d versions came back", len(versions))
	}
	first, _ := versions[0].(map[string]any)
	folders, _ := first["folders"].([]any)
	if len(folders) != 1 || coerce.StringOr(folders[0], "") != "Supports" {
		t.Fatalf("the version lists the folders %v", first["folders"])
	}
}
