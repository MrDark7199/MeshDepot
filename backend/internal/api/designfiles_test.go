package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/coerce"
	"meshdepot/internal/storage"
)

// uploadVersion posts a new file version with the given files in the "file"
// field, plus the version number and the notes the form sends alongside them.
func (testHarness *harness) uploadVersion(designID int, version, notes string, files map[string][]byte) response {
	testHarness.t.Helper()
	return testHarness.postFiles(fmt.Sprintf("/api/v1/designs/%s/files", testHarness.designPID(designID)), version, notes, files)
}

// addEntries posts further files into an existing version.
func (testHarness *harness) addEntries(designID, fileID int, files map[string][]byte) response {
	testHarness.t.Helper()
	return testHarness.postFiles(fmt.Sprintf("/api/v1/designs/%s/files/%d/entries", testHarness.designPID(designID), fileID), "", "", files)
}

func (testHarness *harness) postFiles(path, version, notes string, files map[string][]byte) response {
	testHarness.t.Helper()
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	if version != "" {
		if failure := writer.WriteField("version", version); failure != nil {
			testHarness.t.Fatalf("write the version field: %v", failure)
		}
	}
	if notes != "" {
		if failure := writer.WriteField("notes", notes); failure != nil {
			testHarness.t.Fatalf("write the notes field: %v", failure)
		}
	}
	for filename, content := range files {
		part, failure := writer.CreateFormFile("file", filename)
		if failure != nil {
			testHarness.t.Fatalf("create form file: %v", failure)
		}
		if _, failure := part.Write(content); failure != nil {
			testHarness.t.Fatalf("write form file: %v", failure)
		}
	}
	if failure := writer.Close(); failure != nil {
		testHarness.t.Fatalf("close multipart writer: %v", failure)
	}
	return testHarness.do(request{
		method:  http.MethodPost,
		path:    path,
		rawBody: buffer,
		headers: map[string]string{"Content-Type": writer.FormDataContentType()},
		token:   testHarness.userToken,
	})
}

// zipArchive builds a ZIP in memory from the given paths.
func zipArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	for name, content := range files {
		entry, failure := writer.Create(name)
		if failure != nil {
			t.Fatalf("create zip entry: %v", failure)
		}
		if _, failure := entry.Write(content); failure != nil {
			t.Fatalf("write zip entry: %v", failure)
		}
	}
	if failure := writer.Close(); failure != nil {
		t.Fatalf("close zip writer: %v", failure)
	}
	return buffer.Bytes()
}

// stlBytes and stlBytesAlternate are the smallest things that pass for a model
// file. The content never matters, only that two uploads differ so the
// content-addressed blob store keeps them apart.
var (
	stlBytes          = []byte("solid wuerfel\nendsolid wuerfel\n")
	stlBytesAlternate = []byte("solid kugel\nendsolid kugel\n")
)

// relativePaths reads the relative paths of all entries of a version.
func (testHarness *harness) relativePaths(versionID int) map[string]bool {
	testHarness.t.Helper()
	rows, failure := testHarness.database.Query(
		"SELECT COALESCE(relative_path, '') FROM design_file_entries WHERE design_file_id = ?", versionID)
	if failure != nil {
		testHarness.t.Fatalf("read the relative paths: %v", failure)
	}
	defer rows.Close()
	paths := map[string]bool{}
	for rows.Next() {
		var path string
		if failure := rows.Scan(&path); failure != nil {
			testHarness.t.Fatalf("scan a relative path: %v", failure)
		}
		paths[path] = true
	}
	return paths
}

func TestFilesStoreCreatesAVersion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")

	answer := testHarness.uploadVersion(designID, "1.0", "erste Fassung", map[string][]byte{"wuerfel.stl": stlBytes})

	if answer.status != http.StatusCreated {
		t.Fatalf("the upload answered %d: %s", answer.status, answer.rawBody)
	}
	saved := answer.data(t)
	if saved["version"] != "1.0" || saved["notes"] != "erste Fassung" {
		t.Fatalf("the version carries %v", saved)
	}
	if coerce.Int(saved["file_count"]) != 1 || coerce.Int(saved["size_bytes"]) != len(stlBytes) {
		t.Fatalf("the counters are %v / %v", saved["file_count"], saved["size_bytes"])
	}
	if coerce.Int(saved["is_current"]) != 1 {
		t.Fatal("the new version is not the current one")
	}
	entryPath := testHarness.storedFile("SELECT path FROM design_file_entries WHERE design_file_id = ?", coerce.Int(saved["id"]))
	content, failure := os.ReadFile(entryPath)
	if failure != nil {
		t.Fatalf("the blob is missing: %v", failure)
	}
	if !bytes.Equal(content, stlBytes) {
		t.Fatal("the stored bytes differ from the uploaded ones")
	}
}

// A new version takes over as the current one; the previous one keeps its files
// but loses the flag.
func TestFilesStoreDemotesThePreviousVersion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	first := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	firstID := coerce.Int(first.data(t)["id"])

	second := testHarness.uploadVersion(designID, "2.0", "", map[string][]byte{"wuerfel.stl": stlBytes})

	if second.status != http.StatusCreated {
		t.Fatalf("the second upload answered %d: %s", second.status, second.rawBody)
	}
	if flag := testHarness.scalarInt("SELECT is_current FROM design_files WHERE id = ?", firstID); flag != 0 {
		t.Fatal("the previous version is still the current one")
	}
	current := testHarness.count("SELECT COUNT(*) FROM design_files WHERE design_id = ? AND is_current = 1", designID)
	if current != 1 {
		t.Fatalf("%d versions are flagged as current", current)
	}
}

func TestFilesStoreRejectsAVersionThatIsNotANumber(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")

	for _, version := range []string{"", "zwei", "1.0-beta", "NaN", "Inf", "1e400", "-1", "1000000"} {
		answer := testHarness.uploadVersion(designID, version, "", map[string][]byte{"wuerfel.stl": stlBytes})
		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("the version %q answered %d", version, answer.status)
		}
		if key := answer.errorKey(t); key != "error.version_invalid" {
			t.Fatalf("the version %q produced the error key %q", version, key)
		}
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_files WHERE design_id = ?", designID); count != 0 {
		t.Fatalf("%d versions were created anyway", count)
	}
}

func TestFilesStoreRejectsADuplicateVersion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})

	answer := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"kugel.stl": stlBytes})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a duplicate version answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.version_exists" {
		t.Fatalf("unexpected error key %q", key)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_files WHERE design_id = ?", designID); count != 1 {
		t.Fatalf("%d versions exist", count)
	}
}

func TestFilesStoreNeedsAFile(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")

	answer := testHarness.uploadVersion(designID, "1.0", "", nil)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an upload without files answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.file_required" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// A shared user may read the design but not change its files.
func TestFilesStoreRefusesAShareRecipient(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "Geteiltes Design")
	testHarness.shareDesign(designID, testHarness.adminID, testHarness.userID)

	answer := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})

	if answer.status != http.StatusForbidden {
		t.Fatalf("the share recipient answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.unauthorized" {
		t.Fatalf("unexpected error key %q", key)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_files WHERE design_id = ?", designID); count != 0 {
		t.Fatal("a version was created anyway")
	}
}

// A design the user has no access to at all answers 404 - not the 403 a share
// recipient gets, which would confirm the design exists.
func TestFilesStoreRefusesAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.uploadVersion(foreign, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
}

// Identical bytes are stored once. Both entries point at the same blob, and the
// second upload gets a duplicate warning naming the other design.
func TestFilesStoreWarnsAboutDuplicateContentInAnotherDesign(t *testing.T) {
	testHarness := newHarness(t)
	firstDesign := testHarness.insertDesign(testHarness.userID, "Erstes Design")
	secondDesign := testHarness.insertDesign(testHarness.userID, "Zweites Design")
	testHarness.uploadVersion(firstDesign, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})

	answer := testHarness.uploadVersion(secondDesign, "1.0", "", map[string][]byte{"kopie.stl": stlBytes})

	warnings := answer.data(t)["duplicate_warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("%d warnings were returned", len(warnings))
	}
	warning := warnings[0].(map[string]any)
	if warning["design_name"] != "Erstes Design" || warning["filename"] != "kopie.stl" {
		t.Fatalf("the warning carries %v", warning)
	}
	// Each design publishes the content in its own version directory, but both
	// entries point at one blob - deduplication happens at the inode, not at the
	// path.
	blobs := testHarness.count("SELECT COUNT(DISTINCT blob_hash) FROM design_file_entries")
	if blobs != 1 {
		t.Fatalf("the identical content produced %d blobs", blobs)
	}
	var first, second string
	rows, failure := testHarness.database.Query("SELECT path FROM design_file_entries ORDER BY id")
	if failure != nil {
		t.Fatalf("read the entries: %v", failure)
	}
	defer rows.Close()
	rows.Next()
	rows.Scan(&first)
	rows.Next()
	rows.Scan(&second)
	if first == second {
		t.Fatal("both designs share one published path")
	}
	layout := storage.New(testHarness.dataRoot)
	firstInfo, failure := os.Stat(layout.Abs(first))
	if failure != nil {
		t.Fatalf("stat the first file: %v", failure)
	}
	secondInfo, failure := os.Stat(layout.Abs(second))
	if failure != nil {
		t.Fatalf("stat the second file: %v", failure)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("the identical content was stored twice instead of linked")
	}
}

// Content that only exists in another user's library is none of this user's
// business - no warning may leak it.
func TestFilesStoreDoesNotWarnAboutAForeignLibrary(t *testing.T) {
	testHarness := newHarness(t)
	ownDesign := testHarness.insertDesign(testHarness.userID, "Eigenes Design")
	created := testHarness.uploadVersion(ownDesign, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	hash := testHarness.scalar("SELECT file_hash FROM design_file_entries WHERE design_file_id = ?",
		coerce.Int(created.data(t)["id"]))

	// The admin holds the same content, which is none of this user's business.
	foreignDesign := testHarness.insertDesign(testHarness.adminID, "Fremdes Design")
	foreignVersion := testHarness.insertFileVersion(foreignDesign, len(stlBytes), "wuerfel.stl")
	if _, failure := testHarness.database.Exec(
		"UPDATE design_file_entries SET file_hash = ? WHERE design_file_id = ?", hash, foreignVersion); failure != nil {
		t.Fatalf("set the hash: %v", failure)
	}
	secondOwnDesign := testHarness.insertDesign(testHarness.userID, "Zweites eigenes Design")

	answer := testHarness.uploadVersion(secondOwnDesign, "1.0", "", map[string][]byte{"kopie.stl": stlBytes})

	warnings := answer.data(t)["duplicate_warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("%d warnings were returned: %v", len(warnings), warnings)
	}
	if name := warnings[0].(map[string]any)["design_name"]; name != "Eigenes Design" {
		t.Fatalf("the warning names %v", name)
	}
}

func TestFilesStoreExtractsAZip(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Archiv")
	archive := zipArchive(t, map[string][]byte{
		"modell/teile/links.stl":  stlBytes,
		"modell/teile/rechts.stl": stlBytesAlternate,
		"modell/liesmich.txt":     []byte("hinweise"),
	})

	answer := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"paket.zip": archive})

	if answer.status != http.StatusCreated {
		t.Fatalf("the upload answered %d: %s", answer.status, answer.rawBody)
	}
	versionID := coerce.Int(answer.data(t)["id"])
	if count := testHarness.count("SELECT COUNT(*) FROM design_file_entries WHERE design_file_id = ?", versionID); count != 3 {
		t.Fatalf("%d entries were created", count)
	}
	// The common top folder is stripped, the structure below it survives.
	paths := testHarness.relativePaths(versionID)
	for _, expected := range []string{"teile/links.stl", "teile/rechts.stl", "liesmich.txt"} {
		if !paths[expected] {
			t.Fatalf("the relative path %q is missing: %v", expected, paths)
		}
	}
}

// Without a single common top folder nothing is stripped.
func TestFilesStoreKeepsTheZipStructureWithoutACommonFolder(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Archiv")
	archive := zipArchive(t, map[string][]byte{
		"links/teil.stl":  stlBytes,
		"rechts/teil.stl": stlBytesAlternate,
	})

	answer := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"paket.zip": archive})

	paths := testHarness.relativePaths(coerce.Int(answer.data(t)["id"]))
	for _, expected := range []string{"links/teil.stl", "rechts/teil.stl"} {
		if !paths[expected] {
			t.Fatalf("the relative path %q is missing: %v", expected, paths)
		}
	}
}

// Dotfiles and directory entries never become files of their own.
func TestFilesStoreSkipsDotfilesInAZip(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Archiv")
	archive := zipArchive(t, map[string][]byte{
		"teil.stl":             stlBytes,
		".DS_Store":            []byte("müll"),
		"__MACOSX/.hidden":     []byte("müll"),
		"unterordner/.gitkeep": []byte(""),
	})

	answer := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"paket.zip": archive})

	versionID := coerce.Int(answer.data(t)["id"])
	if count := testHarness.count("SELECT COUNT(*) FROM design_file_entries WHERE design_file_id = ?", versionID); count != 1 {
		t.Fatalf("%d entries were created: %v", count, testHarness.relativePaths(versionID))
	}
}

// A ZIP entry that would escape its directory is flattened, so nothing is ever
// written outside the version.
func TestFilesStoreNeutralizesTraversalInAZip(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Archiv")
	archive := zipArchive(t, map[string][]byte{"../../entwischt.stl": stlBytes})

	answer := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"paket.zip": archive})

	paths := testHarness.relativePaths(coerce.Int(answer.data(t)["id"]))
	for path := range paths {
		if strings.Contains(path, "..") {
			t.Fatalf("the relative path %q still escapes", path)
		}
	}
	if !paths["entwischt.stl"] {
		t.Fatalf("the flattened path is missing: %v", paths)
	}
}

// Something that is named .zip but is not one is stored as it is instead of
// being dropped.
func TestFilesStoreKeepsABrokenZipAsAFile(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Archiv")

	answer := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"kaputt.zip": []byte("kein zip")})

	if answer.status != http.StatusCreated {
		t.Fatalf("the upload answered %d: %s", answer.status, answer.rawBody)
	}
	versionID := coerce.Int(answer.data(t)["id"])
	if count := testHarness.count("SELECT COUNT(*) FROM design_file_entries WHERE design_file_id = ?", versionID); count != 1 {
		t.Fatalf("%d entries were created", count)
	}
}

// G-code is parsed on upload so the frontend can show the print parameters
// without reading the file again.
func TestFilesStoreParsesGcodeMetadata(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit G-code")
	gcodeContent := []byte("; generated by PrusaSlicer\n; layer_height = 0.2\n; fill_density = 15%\nG1 X0 Y0\n")

	answer := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"druck.gcode": gcodeContent})

	if answer.status != http.StatusCreated {
		t.Fatalf("the upload answered %d: %s", answer.status, answer.rawBody)
	}
	meta := testHarness.scalar("SELECT gcode_meta FROM design_file_entries WHERE filename = 'druck.gcode'")
	if meta == "" {
		t.Fatal("no print parameters were stored")
	}
	if !strings.Contains(meta, "layer_height") {
		t.Fatalf("the parameters are %q", meta)
	}
}

func TestFilesIndexReturnsVersionsWithTheirEntries(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"alt.stl": stlBytes})
	testHarness.uploadVersion(designID, "2.0", "", map[string][]byte{"neu.stl": stlBytesAlternate})

	versions := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/files", testHarness.designPID(designID)), nil).list(t)

	if len(versions) != 2 {
		t.Fatalf("%d versions were listed", len(versions))
	}
	// The current version comes first.
	current := versions[0].(map[string]any)
	if current["version"] != "2.0" {
		t.Fatalf("the first entry is version %v", current["version"])
	}
	entries := current["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("%d entries were attached", len(entries))
	}
	if entries[0].(map[string]any)["filename"] != "neu.stl" {
		t.Fatalf("the entry carries %v", entries[0])
	}
}

// The parsed G-code parameters travel as an object, not as the JSON string the
// column holds.
func TestFilesIndexDecodesTheGcodeMetadata(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit G-code")
	testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{
		"druck.gcode": []byte("; layer_height = 0.2\nG1 X0 Y0\n"),
		"wuerfel.stl": stlBytes,
	})

	versions := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/files", testHarness.designPID(designID)), nil).list(t)

	for _, entry := range versions[0].(map[string]any)["entries"].([]any) {
		row := entry.(map[string]any)
		if row["filename"] == "druck.gcode" {
			if _, isObject := row["gcode_meta"].(map[string]any); !isObject {
				t.Fatalf("the metadata came back as %T", row["gcode_meta"])
			}
		}
		// An entry without metadata carries no empty field either.
		if row["filename"] == "wuerfel.stl" {
			if _, present := row["gcode_meta"]; present {
				t.Fatalf("the stl carries metadata: %v", row["gcode_meta"])
			}
		}
	}
}

// A share recipient may read the file list - that is the point of a share.
func TestFilesIndexIsOpenToAShareRecipient(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "Geteiltes Design")
	testHarness.shareDesign(designID, testHarness.adminID, testHarness.userID)
	testHarness.insertFileVersion(designID, 100, "wuerfel.stl")

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/files", testHarness.designPID(designID)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the share recipient answered %d: %s", answer.status, answer.rawBody)
	}
	if len(answer.list(t)) != 1 {
		t.Fatal("the version list is empty")
	}
}

func TestFilesIndexRefusesAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/files", testHarness.designPID(foreign)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
}

func TestFilesAddEntriesExtendsAnExistingVersion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"erstes.stl": stlBytes})
	versionID := coerce.Int(created.data(t)["id"])
	extra := stlBytesAlternate

	answer := testHarness.addEntries(designID, versionID, map[string][]byte{"zweites.stl": extra})

	if answer.status != http.StatusCreated {
		t.Fatalf("adding answered %d: %s", answer.status, answer.rawBody)
	}
	saved := answer.data(t)
	if coerce.Int(saved["file_count"]) != 2 {
		t.Fatalf("the file count is %v", saved["file_count"])
	}
	if coerce.Int(saved["size_bytes"]) != len(stlBytes)+len(extra) {
		t.Fatalf("the size is %v", saved["size_bytes"])
	}
	if entries := saved["entries"].([]any); len(entries) != 2 {
		t.Fatalf("%d entries were returned", len(entries))
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_files WHERE design_id = ?", designID); count != 1 {
		t.Fatalf("%d versions exist - a new one was created", count)
	}
}

// Two files of the same name inside one version would be indistinguishable in
// the UI, so the whole call is refused instead of half-applied.
func TestFilesAddEntriesRefusesADuplicateFilename(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"teil.stl": stlBytes})
	versionID := coerce.Int(created.data(t)["id"])

	answer := testHarness.addEntries(designID, versionID, map[string][]byte{"TEIL.STL": stlBytesAlternate})

	if answer.status != http.StatusConflict {
		t.Fatalf("a duplicate filename answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.filename_exists" {
		t.Fatalf("unexpected error key %q", key)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_file_entries WHERE design_file_id = ?", versionID); count != 1 {
		t.Fatalf("%d entries exist", count)
	}
	if size := testHarness.scalarInt("SELECT size_bytes FROM design_files WHERE id = ?", versionID); size != len(stlBytes) {
		t.Fatalf("the size counter was changed to %d", size)
	}
}

func TestFilesAddEntriesRefusesAVersionOfAnotherDesign(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Eigenes")
	otherDesign := testHarness.insertDesign(testHarness.userID, "Anderes")
	otherVersion := testHarness.insertFileVersion(otherDesign, 100, "fremd.stl")

	answer := testHarness.addEntries(designID, otherVersion, map[string][]byte{"neu.stl": stlBytes})

	if answer.status != http.StatusNotFound {
		t.Fatalf("a version of another design answered %d", answer.status)
	}
}

func TestFilesAddEntriesNeedsAFile(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"teil.stl": stlBytes})

	answer := testHarness.addEntries(designID, coerce.Int(created.data(t)["id"]), nil)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("adding without files answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.file_required" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestFilesDestroyRemovesTheVersionAndItsEntries(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"teil.stl": stlBytes})
	versionID := coerce.Int(created.data(t)["id"])

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/files/%d", testHarness.designPID(designID), versionID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_files WHERE id = ?", versionID); count != 0 {
		t.Fatal("the version survived")
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_file_entries WHERE design_file_id = ?", versionID); count != 0 {
		t.Fatal("the entries survived")
	}
}

// Deleting the current version has to promote the newest remaining one, or the
// design shows an empty file list although its files are still there.
func TestFilesDestroyPromotesTheNewestRemainingVersion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	first := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"alt.stl": stlBytes})
	second := testHarness.uploadVersion(designID, "2.0", "", map[string][]byte{"neu.stl": stlBytesAlternate})
	firstID := coerce.Int(first.data(t)["id"])
	secondID := coerce.Int(second.data(t)["id"])

	testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/files/%d", testHarness.designPID(designID), secondID), nil)

	if flag := testHarness.scalarInt("SELECT is_current FROM design_files WHERE id = ?", firstID); flag != 1 {
		t.Fatal("no version was promoted")
	}
}

// Deleting a version that was not current leaves the flag where it is.
func TestFilesDestroyLeavesTheCurrentVersionAlone(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	first := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"alt.stl": stlBytes})
	second := testHarness.uploadVersion(designID, "2.0", "", map[string][]byte{"neu.stl": stlBytesAlternate})
	firstID := coerce.Int(first.data(t)["id"])
	secondID := coerce.Int(second.data(t)["id"])

	testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/files/%d", testHarness.designPID(designID), firstID), nil)

	if flag := testHarness.scalarInt("SELECT is_current FROM design_files WHERE id = ?", secondID); flag != 1 {
		t.Fatal("the current version lost its flag")
	}
}

func TestFilesDestroyRefusesAShareRecipient(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "Geteiltes Design")
	testHarness.shareDesign(designID, testHarness.adminID, testHarness.userID)
	versionID := testHarness.insertFileVersion(designID, 100, "teil.stl")

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/files/%d", testHarness.designPID(designID), versionID), nil)

	if answer.status != http.StatusForbidden {
		t.Fatalf("the share recipient answered %d", answer.status)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_files WHERE id = ?", versionID); count != 1 {
		t.Fatal("the version was deleted anyway")
	}
}

func TestFilesDeleteEntryRecountsTheVersion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	extra := stlBytesAlternate
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"erstes.stl": stlBytes, "zweites.stl": extra})
	versionID := coerce.Int(created.data(t)["id"])
	entryID := testHarness.scalarInt(
		"SELECT id FROM design_file_entries WHERE design_file_id = ? AND filename = 'zweites.stl'", versionID)

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d", testHarness.designPID(designID), versionID, entryID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.scalarInt("SELECT file_count FROM design_files WHERE id = ?", versionID); count != 1 {
		t.Fatalf("the file count is %d", count)
	}
	if size := testHarness.scalarInt("SELECT size_bytes FROM design_files WHERE id = ?", versionID); size != len(stlBytes) {
		t.Fatalf("the size is %d", size)
	}
}

// The last entry takes its version with it - an empty version is nothing the UI
// could show.
func TestFilesDeleteEntryDropsTheEmptiedVersion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	first := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"alt.stl": stlBytes})
	second := testHarness.uploadVersion(designID, "2.0", "", map[string][]byte{"neu.stl": stlBytesAlternate})
	firstID := coerce.Int(first.data(t)["id"])
	secondID := coerce.Int(second.data(t)["id"])
	entryID := testHarness.scalarInt("SELECT id FROM design_file_entries WHERE design_file_id = ?", secondID)

	testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d", testHarness.designPID(designID), secondID, entryID), nil)

	if count := testHarness.count("SELECT COUNT(*) FROM design_files WHERE id = ?", secondID); count != 0 {
		t.Fatal("the emptied version survived")
	}
	if flag := testHarness.scalarInt("SELECT is_current FROM design_files WHERE id = ?", firstID); flag != 1 {
		t.Fatal("the remaining version was not promoted")
	}
}

func TestFilesDeleteEntryRefusesAnEntryOfAnotherVersion(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")
	first := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"alt.stl": stlBytes})
	second := testHarness.uploadVersion(designID, "2.0", "", map[string][]byte{"neu.stl": stlBytesAlternate})
	firstID := coerce.Int(first.data(t)["id"])
	foreignEntry := testHarness.scalarInt("SELECT id FROM design_file_entries WHERE design_file_id = ?",
		coerce.Int(second.data(t)["id"]))

	answer := testHarness.asUser(http.MethodDelete,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d", testHarness.designPID(designID), firstID, foreignEntry), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("an entry of another version answered %d", answer.status)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_file_entries WHERE id = ?", foreignEntry); count != 1 {
		t.Fatal("the entry was deleted anyway")
	}
}

// A single file is handed back as it is; only several files become a ZIP.
func TestFilesDownloadServesASingleFileDirectly(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Datei")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	versionID := coerce.Int(created.data(t)["id"])

	answer := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/download", testHarness.designPID(designID), versionID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the download answered %d", answer.status)
	}
	disposition := answer.recorder.Header().Get("Content-Disposition")
	if disposition != `attachment; filename="wuerfel.stl"` {
		t.Fatalf("the disposition is %q", disposition)
	}
	if answer.rawBody != string(stlBytes) {
		t.Fatal("the delivered bytes differ from the stored ones")
	}
}

func TestFilesDownloadPacksSeveralFilesIntoAZip(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Archiv")
	archive := zipArchive(t, map[string][]byte{
		"modell/teile/links.stl":  stlBytes,
		"modell/teile/rechts.stl": stlBytesAlternate,
	})
	created := testHarness.uploadVersion(designID, "2.0", "", map[string][]byte{"paket.zip": archive})
	versionID := coerce.Int(created.data(t)["id"])

	answer := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/download", testHarness.designPID(designID), versionID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the download answered %d", answer.status)
	}
	if disposition := answer.recorder.Header().Get("Content-Disposition"); disposition != `attachment; filename="design_v2.0.zip"` {
		t.Fatalf("the disposition is %q", disposition)
	}
	body := []byte(answer.rawBody)
	reader, failure := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if failure != nil {
		t.Fatalf("the answer is not a zip: %v", failure)
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	// The directory structure has to survive the round trip, otherwise two files
	// of the same name in different folders would overwrite each other.
	for _, expected := range []string{"teile/links.stl", "teile/rechts.stl"} {
		if !names[expected] {
			t.Fatalf("the zip entry %q is missing: %v", expected, names)
		}
	}
}

func TestFilesDownloadIsOpenToAShareRecipient(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.adminID, "Geteiltes Design")
	testHarness.shareDesign(designID, testHarness.adminID, testHarness.userID)
	versionID := testHarness.insertFileVersion(designID, len(stlBytes), "wuerfel.stl")
	if failure := os.MkdirAll(testHarness.dataRoot, 0o775); failure != nil {
		t.Fatalf("create the model directory: %v", failure)
	}
	if failure := os.WriteFile(filepath.Join(testHarness.dataRoot, "wuerfel.stl"), stlBytes, 0o644); failure != nil {
		t.Fatalf("write the blob: %v", failure)
	}

	answer := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/download", testHarness.designPID(designID), versionID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the share recipient answered %d: %s", answer.status, answer.rawBody)
	}
	if answer.rawBody != string(stlBytes) {
		t.Fatal("the delivered bytes differ from the stored ones")
	}
}

func TestFilesServeEntryStreamsTheFileInline(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Datei")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	versionID := coerce.Int(created.data(t)["id"])
	entryID := testHarness.scalarInt("SELECT id FROM design_file_entries WHERE design_file_id = ?", versionID)

	answer := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d", testHarness.designPID(designID), versionID, entryID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("serving answered %d", answer.status)
	}
	if answer.recorder.Header().Get("Content-Disposition") != "" {
		t.Fatal("an inline file must not be offered as a download")
	}
	if answer.rawBody != string(stlBytes) {
		t.Fatal("the delivered bytes differ from the stored ones")
	}
}

// The blob may be gone even though the row is still there - that is a 404, not
// an empty file.
func TestFilesServeEntryIsNotFoundWithoutTheBlob(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Datei")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	versionID := coerce.Int(created.data(t)["id"])
	entryID := testHarness.scalarInt("SELECT id FROM design_file_entries WHERE design_file_id = ?", versionID)
	blobPath := testHarness.storedFile("SELECT path FROM design_file_entries WHERE id = ?", entryID)
	if failure := os.Remove(blobPath); failure != nil {
		t.Fatalf("remove the blob: %v", failure)
	}

	answer := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d", testHarness.designPID(designID), versionID, entryID), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a missing blob answered %d", answer.status)
	}
}

// The legacy STL route picks the first model file of a version and ignores
// everything else.
func TestFilesServeStlPicksTheFirstModelFile(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Datei")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{
		"liesmich.txt": []byte("hinweise"),
		"wuerfel.stl":  stlBytes,
	})
	versionID := coerce.Int(created.data(t)["id"])

	answer := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/stl", testHarness.designPID(designID), versionID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("serving answered %d", answer.status)
	}
	if answer.rawBody != string(stlBytes) {
		t.Fatal("a file other than the model was delivered")
	}
}

func TestFilesServeStlIsNotFoundWithoutAModelFile(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Ohne Modell")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"liesmich.txt": []byte("hinweise")})

	answer := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/stl", testHarness.designPID(designID), coerce.Int(created.data(t)["id"])), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a version without a model answered %d", answer.status)
	}
}

// The token exists so a slicer without a session can fetch one file. It is
// valid exactly once.
func TestFilesTokenServesTheFileExactlyOnce(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Datei")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	versionID := coerce.Int(created.data(t)["id"])
	entryID := testHarness.scalarInt("SELECT id FROM design_file_entries WHERE design_file_id = ?", versionID)

	issued := testHarness.asUser(http.MethodPost,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/entry/%d/token", testHarness.designPID(designID), versionID, entryID), nil)
	if issued.status != http.StatusOK {
		t.Fatalf("issuing the token answered %d: %s", issued.status, issued.rawBody)
	}
	token := coerce.StringOr(issued.decoded["token"], "")
	if token == "" {
		t.Fatalf("no token was issued: %s", issued.rawBody)
	}

	first := testHarness.anonymous(http.MethodGet, "/api/v1/files/token/"+token, nil)
	if first.status != http.StatusOK {
		t.Fatalf("the first call answered %d", first.status)
	}
	if first.rawBody != string(stlBytes) {
		t.Fatal("the delivered bytes differ from the stored ones")
	}

	second := testHarness.anonymous(http.MethodGet, "/api/v1/files/token/"+token, nil)
	if second.status != http.StatusNotFound {
		t.Fatalf("the second call answered %d", second.status)
	}
}

func TestFilesTokenIsNotFoundForAnUnknownToken(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.anonymous(http.MethodGet, "/api/v1/files/token/gibtsnicht", nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("an unknown token answered %d", answer.status)
	}
}

func TestFileRoutesWithNonNumericIDsAreNotFound(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Dateien")

	calls := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/designs/abc/files"},
		{http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/files/abc", testHarness.designPID(designID))},
		{http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/files/abc/download", testHarness.designPID(designID))},
		{http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/files/1/entry/abc", testHarness.designPID(designID))},
		{http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/files/1/entry/abc", testHarness.designPID(designID))},
		{http.MethodPost, fmt.Sprintf("/api/v1/designs/%s/files/1/entry/abc/token", testHarness.designPID(designID))},
		{http.MethodGet, fmt.Sprintf("/api/v1/designs/%s/files/abc/stl", testHarness.designPID(designID))},
	}

	for _, call := range calls {
		answer := testHarness.asUser(call.method, call.path, nil)
		if answer.status != http.StatusNotFound {
			t.Fatalf("%s %s answered %d", call.method, call.path, answer.status)
		}
	}
}

func TestZipEntryNameKeepsTheStructureAndDropsTraversal(t *testing.T) {
	cases := []struct {
		entry  fileEntryRow
		expect string
	}{
		{fileEntryRow{RelativePath: "teile/links.stl", Filename: "links.stl"}, "teile/links.stl"},
		{fileEntryRow{RelativePath: "", Filename: "links.stl"}, "links.stl"},
		{fileEntryRow{RelativePath: "../../entwischt.stl", Filename: "entwischt.stl"}, "entwischt.stl"},
		{fileEntryRow{RelativePath: "/absolut/teil.stl", Filename: "teil.stl"}, "absolut/teil.stl"},
		{fileEntryRow{RelativePath: "./teil.stl", Filename: "teil.stl"}, "teil.stl"},
		{fileEntryRow{RelativePath: "..", Filename: ""}, "file"},
	}

	for _, testCase := range cases {
		if name := zipEntryName(testCase.entry); name != testCase.expect {
			t.Fatalf("zipEntryName(%q) = %q, expected %q", testCase.entry.RelativePath, name, testCase.expect)
		}
	}
}

func TestEntryFilenameNeverComesBackEmpty(t *testing.T) {
	if name := entryFilename(fileEntryRow{Filename: ""}); name != "file" {
		t.Fatalf("an empty filename became %q", name)
	}
	if name := entryFilename(fileEntryRow{Filename: "wuerfel.stl"}); name != "wuerfel.stl" {
		t.Fatalf("the filename became %q", name)
	}
}

// A filename with a line break in it would let an upload inject its own header
// into the response.
func TestServeAttachmentSanitizesTheFilename(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Datei")
	created := testHarness.uploadVersion(designID, "1.0", "", map[string][]byte{"wuerfel.stl": stlBytes})
	versionID := coerce.Int(created.data(t)["id"])
	if _, failure := testHarness.database.Exec(
		"UPDATE design_file_entries SET filename = ? WHERE design_file_id = ?",
		"boes\r\nX-Injected: 1\".stl", versionID); failure != nil {
		t.Fatalf("set the filename: %v", failure)
	}

	answer := testHarness.asUser(http.MethodGet,
		fmt.Sprintf("/api/v1/designs/%s/files/%d/download", testHarness.designPID(designID), versionID), nil)

	disposition := answer.recorder.Header().Get("Content-Disposition")
	if strings.ContainsAny(disposition, "\r\n") {
		t.Fatalf("the header carries a line break: %q", disposition)
	}
}

func TestNumericVersionAcceptsOnlyPlainNumbers(t *testing.T) {
	valid := []string{"0", "1", "1.0", "2.5", "100000"}
	invalid := []string{"", " ", "zwei", "1.0-beta", "NaN", "Inf", "+Inf", "1e400", "-1", "100001", "0x10"}

	for _, version := range valid {
		if !numericVersion(version) {
			t.Fatalf("%q was rejected", version)
		}
	}
	for _, version := range invalid {
		if numericVersion(version) {
			t.Fatalf("%q was accepted", version)
		}
	}
}
