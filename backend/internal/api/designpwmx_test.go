package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/pwmx"
)

// insertEntryWithBlob writes a file version with one entry, puts the content on
// disk and gives the entry a blob hash - which is what the mesh cache is keyed
// on.
func (testHarness *harness) insertEntryWithBlob(designID int, filename, blobHash string, content []byte) (int, int) {
	testHarness.t.Helper()
	path := filepath.Join(testHarness.dataRoot, filename)
	if content != nil {
		if failure := os.MkdirAll(filepath.Dir(path), 0o775); failure != nil {
			testHarness.t.Fatalf("create the file directory: %v", failure)
		}
		if failure := os.WriteFile(path, content, 0o644); failure != nil {
			testHarness.t.Fatalf("write the file: %v", failure)
		}
	}
	result, failure := testHarness.database.Exec(
		"INSERT INTO design_files (design_id, filename, path, size_bytes) VALUES (?, ?, ?, ?)",
		designID, filename, path, len(content))
	if failure != nil {
		testHarness.t.Fatalf("insert the file version: %v", failure)
	}
	versionID, _ := result.LastInsertId()
	entryResult, failure := testHarness.database.Exec(
		"INSERT INTO design_file_entries (design_file_id, filename, path, size_bytes, blob_hash) VALUES (?, ?, ?, ?, ?)",
		versionID, filename, path, len(content), blobHash)
	if failure != nil {
		testHarness.t.Fatalf("insert the file entry: %v", failure)
	}
	entryID, _ := entryResult.LastInsertId()
	return int(versionID), int(entryID)
}

func pwmxMeshPath(designID, versionID, entryID any) string {
	return fmt.Sprintf("/api/v1/designs/%v/files/%v/entry/%v/pwmx/mesh", designID, versionID, entryID)
}

// meshCachePath rebuilds the name the handler caches a reconstructed mesh
// under, so a test can plant a file there.
func meshCachePath(dataRoot, blobHash string) string {
	options := pwmx.DefaultMeshOptions()
	return filepath.Join(dataRoot, "pwmx_mesh", fmt.Sprintf("%s-mc-xy%dz%dt%dm%d.stl",
		blobHash, options.XYStep, options.ZStep, options.Threshold, int(options.MinFill*100)))
}

// Reconstructing a mesh takes seconds, so a mesh that was built once is
// streamed from the cache instead of being parsed again.
func TestPwmxMeshComesFromTheCacheWhenItExists(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "resin-modell")
	// The blob itself is not a valid pwmx file: if the handler parsed it instead
	// of using the cache, the request would fail.
	versionID, entryID := testHarness.insertEntryWithBlob(designID, "modell.pwmx", "abc123", []byte("kein echtes pwmx"))
	cachePath := meshCachePath(testHarness.dataRoot, "abc123")
	if failure := os.MkdirAll(filepath.Dir(cachePath), 0o775); failure != nil {
		t.Fatalf("create the cache directory: %v", failure)
	}
	if failure := os.WriteFile(cachePath, []byte("solid cached\nendsolid cached\n"), 0o644); failure != nil {
		t.Fatalf("write the cached mesh: %v", failure)
	}

	answer := testHarness.asUser(http.MethodGet, pwmxMeshPath(testHarness.designPID(designID), versionID, entryID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the cached mesh answered %d: %s", answer.status, answer.rawBody)
	}
	if answer.rawBody != "solid cached\nendsolid cached\n" {
		t.Fatalf("the answer carries %q", answer.rawBody)
	}
}

// A file that only looks like a resin file is rejected instead of ending as a
// server error deep inside the parser.
func TestPwmxMeshRejectsAFileItCannotParse(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "resin-modell")
	versionID, entryID := testHarness.insertEntryWithBlob(designID, "modell.pwmx", "abc123", []byte("kein echtes pwmx"))

	answer := testHarness.asUser(http.MethodGet, pwmxMeshPath(testHarness.designPID(designID), versionID, entryID), nil)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an unparseable file answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.unsupported" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestPwmxMeshRejectsAnotherFileType(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "modell")
	versionID, entryID := testHarness.insertEntryWithBlob(designID, "wuerfel.stl", "abc123", []byte("solid wuerfel\n"))

	answer := testHarness.asUser(http.MethodGet, pwmxMeshPath(testHarness.designPID(designID), versionID, entryID), nil)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an stl file answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.unsupported" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// A row whose blob was lost is not a parse problem, but there is still nothing
// to reconstruct.
func TestPwmxMeshRejectsAMissingBlob(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "resin-modell")
	versionID, entryID := testHarness.insertEntryWithBlob(designID, "modell.pwmx", "abc123", nil)

	answer := testHarness.asUser(http.MethodGet, pwmxMeshPath(testHarness.designPID(designID), versionID, entryID), nil)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a missing blob answered %d: %s", answer.status, answer.rawBody)
	}
}

func TestPwmxMeshOfAForeignDesignIsNotFound(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "fremdes-modell")
	versionID, entryID := testHarness.insertEntryWithBlob(designID, "modell.pwmx", "abc123", []byte("kein echtes pwmx"))

	answer := testHarness.asUser(http.MethodGet, pwmxMeshPath(testHarness.designPID(designID), versionID, entryID), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d: %s", answer.status, answer.rawBody)
	}
}

// The entry has to belong to the version in the path, otherwise anybody could
// read a foreign entry through one of their own designs.
func TestPwmxMeshOfAnotherVersionIsNotFound(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "resin-modell")
	versionID, _ := testHarness.insertEntryWithBlob(designID, "modell.pwmx", "abc123", []byte("kein echtes pwmx"))
	otherDesignID := testHarness.insertDesign(testHarness.userID, "zweites-modell")
	_, foreignEntryID := testHarness.insertEntryWithBlob(otherDesignID, "zweites.pwmx", "def456", []byte("kein echtes pwmx"))

	answer := testHarness.asUser(http.MethodGet, pwmxMeshPath(testHarness.designPID(designID), versionID, foreignEntryID), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign entry answered %d: %s", answer.status, answer.rawBody)
	}
}

func TestPwmxMeshWithANonNumericEntryIsNotFound(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "resin-modell")
	versionID, _ := testHarness.insertEntryWithBlob(designID, "modell.pwmx", "abc123", []byte("kein echtes pwmx"))

	answer := testHarness.asUser(http.MethodGet, pwmxMeshPath(testHarness.designPID(designID), versionID, "abc"), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a non-numeric entry answered %d", answer.status)
	}
}

func TestPwmxMeshNeedsASession(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "resin-modell")
	versionID, entryID := testHarness.insertEntryWithBlob(designID, "modell.pwmx", "abc123", []byte("kein echtes pwmx"))

	answer := testHarness.anonymous(http.MethodGet, pwmxMeshPath(testHarness.designPID(designID), versionID, entryID), nil)

	if answer.status != http.StatusUnauthorized {
		t.Fatalf("an anonymous request answered %d", answer.status)
	}
}

// The mesh is only published once it is complete, so a second request can never
// pick up a half-written file.
func TestWriteCacheFilePublishesTheCompleteFile(t *testing.T) {
	directory := t.TempDir()
	cacheDir := filepath.Join(directory, "pwmx_mesh")
	cachePath := filepath.Join(cacheDir, "abc123.stl")

	writeCacheFile(cacheDir, cachePath, []byte("solid erste\n"))
	writeCacheFile(cacheDir, cachePath, []byte("solid zweite fassung\n"))

	content, failure := os.ReadFile(cachePath)
	if failure != nil {
		t.Fatalf("read the cached mesh: %v", failure)
	}
	if string(content) != "solid zweite fassung\n" {
		t.Fatalf("the cache carries %q", content)
	}
	entries, failure := os.ReadDir(cacheDir)
	if failure != nil {
		t.Fatalf("read the cache directory: %v", failure)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Fatalf("the temporary file %q was left behind", entry.Name())
		}
	}
}
