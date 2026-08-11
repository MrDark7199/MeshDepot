package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/coerce"
	"meshdepot/internal/storage"
)

// Path columns hold the location relative to the data root. An absolute value
// would pin the row to one data directory and stop resolving the moment the
// deployment moves it, so this covers all five writers at once.
func TestEveryStoredPathIsRelativeToTheDataRoot(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	testHarness.uploadImages(designID, pngBytes)
	testHarness.uploadAsUser(http.MethodPost,
		fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID)), "avatar", "bild.png", pngBytes)

	columns := []struct{ query, label string }{
		{"SELECT path FROM design_files", "design_files.path"},
		{"SELECT path FROM design_file_entries", "design_file_entries.path"},
		{"SELECT path FROM design_images", "design_images.path"},
		{"SELECT cover_path FROM designs WHERE cover_path IS NOT NULL", "designs.cover_path"},
		{"SELECT avatar_path FROM users WHERE avatar_path IS NOT NULL", "users.avatar_path"},
	}
	for _, column := range columns {
		rows, failure := testHarness.database.Query(column.query)
		if failure != nil {
			t.Fatalf("%s: %v", column.label, failure)
		}
		seen := 0
		for rows.Next() {
			var stored string
			if failure := rows.Scan(&stored); failure != nil {
				t.Fatalf("%s: %v", column.label, failure)
			}
			seen++
			if filepath.IsAbs(stored) || strings.HasPrefix(stored, "/") {
				t.Errorf("%s holds the absolute path %q", column.label, stored)
			}
		}
		rows.Close()
		if seen == 0 {
			t.Errorf("%s produced no row - the test no longer covers this writer", column.label)
		}
	}
}

// The regression this whole step exists for: platform downloads store a
// root-relative path, and every serving handler used to open that value raw,
// which resolves against the process's working directory rather than the data
// root. The rows below are written exactly the way platforms/save.go writes
// them.
func TestFilesFromARelativePathAreServable(t *testing.T) {
	testHarness := newHarness(t)
	layout := storage.New(testHarness.dataRoot)
	user := testHarness.userLayout(testHarness.userID)
	designID := testHarness.insertDesign(testHarness.userID, "Von der Plattform")

	versionDir := user.Version(designID, "1.0")
	filePath := filepath.Join(versionDir, "wuerfel.stl")
	if failure := storage.WriteFile(filePath, stlBytes); failure != nil {
		t.Fatalf("write the model file: %v", failure)
	}
	versionID := testHarness.insertRow(
		`INSERT INTO design_files (design_id, version, filename, path, size_bytes, file_count, is_current)
		 VALUES (?, '1.0', 'wuerfel.stl', ?, ?, 1, 1)`,
		designID, layout.Rel(versionDir), len(stlBytes))
	entryID := testHarness.insertRow(
		`INSERT INTO design_file_entries (design_file_id, filename, path, size_bytes, file_hash, relative_path, blob_hash)
		 VALUES (?, 'wuerfel.stl', ?, ?, 'aa', 'wuerfel.stl', 'aa')`,
		versionID, layout.Rel(filePath), len(stlBytes))

	paths := map[string]string{
		"single entry": fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d", testHarness.designPID(designID), versionID, entryID),
		"legacy stl":   fmt.Sprintf("/api/v1/designs/%s/files/%d/stl", testHarness.designPID(designID), versionID),
		"download":     fmt.Sprintf("/api/v1/designs/%s/files/%d/download", testHarness.designPID(designID), versionID),
	}
	for label, path := range paths {
		answer := testHarness.asUser(http.MethodGet, path, nil)
		if answer.status != http.StatusOK {
			t.Errorf("%s answered %d: %s", label, answer.status, answer.rawBody)
			continue
		}
		if !strings.Contains(answer.rawBody, "solid") {
			t.Errorf("%s did not return the file content", label)
		}
	}
}

// Deleting a version removes its directory. On a raw relative path the
// RemoveAll silently hit the working directory and left the files behind.
func TestFilesDestroyRemovesTheVersionDirectory(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Zum Loeschen")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	versionID := coerce.Int(created.data(t)["id"])
	versionDir := testHarness.userLayout(testHarness.userID).Version(designID, "1.0")
	if _, failure := os.Stat(versionDir); failure != nil {
		t.Fatalf("the version directory was not created: %v", failure)
	}

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/designs/%s/files/%d", testHarness.designPID(designID), versionID), nil)
	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if _, failure := os.Stat(versionDir); !os.IsNotExist(failure) {
		t.Fatalf("the version directory survived the deletion: %v", failure)
	}
}
