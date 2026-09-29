package platforms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/db"
	"meshdepot/internal/storage"
)

// A model already in the library takes another file into its newest version,
// and refuses a name that version already holds.
func TestAddFilesToCurrentVersion(t *testing.T) {
	database, failure := db.Open(filepath.Join(t.TempDir(), "p.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	defer database.Close()
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	owner := Owner{ID: 1, Layout: storage.New(t.TempDir()).User("a3f1c8e94b2d7f0a5c6e1b8d4a9f2c37")}

	tempDir := t.TempDir()
	first := filepath.Join(tempDir, "Captain_America_V5.3mf")
	os.WriteFile(first, []byte("profile one"), 0o644)

	designID, failure := SaveDownload(database, owner, "makerworld", "https://makerworld.com/models/7",
		Result{Name: "Helmet", Files: []DownloadedFile{{TempPath: first, Name: "Captain_America_V5.3mf"}}})
	if failure != nil {
		t.Fatalf("SaveDownload: %v", failure)
	}

	// Another print profile, another filename: it joins the same version.
	second := filepath.Join(tempDir, "second.3mf")
	os.WriteFile(second, []byte("profile two"), 0o644)
	added, failure := AddFilesToCurrentVersion(database, owner, designID,
		[]DownloadedFile{{TempPath: second, Name: "Captain America V3(2) test(1).3mf"}})
	if failure != nil {
		t.Fatalf("second profile: %v", failure)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}

	var versions, entries, fileCount int
	var sizeBytes int64
	database.QueryRow("SELECT COUNT(*) FROM design_files WHERE design_id=?", designID).Scan(&versions)
	database.QueryRow(`SELECT COUNT(*) FROM design_file_entries e JOIN design_files f ON f.id=e.design_file_id
		WHERE f.design_id=?`, designID).Scan(&entries)
	database.QueryRow("SELECT file_count, size_bytes FROM design_files WHERE design_id=?", designID).Scan(&fileCount, &sizeBytes)
	if versions != 1 {
		t.Fatalf("versions = %d, want 1 - another profile must not open a version of its own", versions)
	}
	if entries != 2 || fileCount != 2 {
		t.Fatalf("entries = %d, file_count = %d, want 2 and 2", entries, fileCount)
	}
	if sizeBytes != int64(len("profile one")+len("profile two")) {
		t.Fatalf("size_bytes = %d, want %d", sizeBytes, len("profile one")+len("profile two"))
	}

	// The same name again is refused, and nothing of the batch is stored.
	again := filepath.Join(tempDir, "again.3mf")
	os.WriteFile(again, []byte("different bytes entirely"), 0o644)
	other := filepath.Join(tempDir, "other.3mf")
	os.WriteFile(other, []byte("would have been new"), 0o644)
	_, failure = AddFilesToCurrentVersion(database, owner, designID, []DownloadedFile{
		{TempPath: again, Name: "captain_america_v5.3mf"},
		{TempPath: other, Name: "brand new.3mf"},
	})
	if failure == nil || !strings.Contains(failure.Error(), "filename_exists") {
		t.Fatalf("a name already in the version was accepted: %v", failure)
	}
	database.QueryRow(`SELECT COUNT(*) FROM design_file_entries e JOIN design_files f ON f.id=e.design_file_id
		WHERE f.design_id=?`, designID).Scan(&entries)
	if entries != 2 {
		t.Fatalf("entries = %d after the refused batch, want 2", entries)
	}
}
