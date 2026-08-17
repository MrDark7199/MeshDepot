package platforms

import (
	"os"
	"path/filepath"
	"testing"

	"meshdepot/internal/db"
	"meshdepot/internal/storage"
)

func TestSaveSyncVersion(t *testing.T) {
	database, failure := db.Open(filepath.Join(t.TempDir(), "p.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	owner := Owner{ID: 1, Layout: storage.New(t.TempDir()).User("a3f1c8e94b2d7f0a5c6e1b8d4a9f2c37")}

	// Create v1.0.
	firstFile := filepath.Join(t.TempDir(), "a.stl")
	os.WriteFile(firstFile, []byte("solid cube v1"), 0o644)
	designID, failure := SaveDownload(database, owner, "thingiverse", "https://x/thing:1", Result{
		Name:  "Cube",
		Files: []DownloadedFile{{TempPath: firstFile, Name: "a.stl"}},
	})
	if failure != nil {
		t.Fatalf("SaveDownload: %v", failure)
	}

	// Sync with identical content → no change.
	sameFile := filepath.Join(t.TempDir(), "a.stl")
	os.WriteFile(sameFile, []byte("solid cube v1"), 0o644)
	changed, _, _, failure := SaveSyncVersion(database, owner, designID, Result{
		Files: []DownloadedFile{{TempPath: sameFile, Name: "a.stl"}},
	}, nil)
	if failure != nil {
		t.Fatalf("sync (unchanged): %v", failure)
	}
	if changed {
		t.Fatal("unchanged content should return changed=false")
	}

	// Sync with changed content → new version 2.0.
	newFile := filepath.Join(t.TempDir(), "a.stl")
	os.WriteFile(newFile, []byte("solid cube v2 - changed"), 0o644)
	changed, newVersion, fileCount, failure := SaveSyncVersion(database, owner, designID, Result{
		Files: []DownloadedFile{{TempPath: newFile, Name: "a.stl"}},
	}, nil)
	if failure != nil {
		t.Fatalf("sync (changed): %v", failure)
	}
	if !changed || newVersion != "2.0" || fileCount != 1 {
		t.Fatalf("expected changed=true v2.0 1 file, got changed=%v v%s %d", changed, newVersion, fileCount)
	}

	// Exactly one current version, and it is 2.0.
	var currentVersion string
	var currentCount int
	database.QueryRow("SELECT version FROM design_files WHERE design_id=? AND is_current=1", designID).Scan(&currentVersion)
	database.QueryRow("SELECT COUNT(*) FROM design_files WHERE design_id=? AND is_current=1", designID).Scan(&currentCount)
	if currentVersion != "2.0" || currentCount != 1 {
		t.Fatalf("expected exactly one current version 2.0, got version=%q count=%d", currentVersion, currentCount)
	}

	// Two versions in total.
	var total int
	database.QueryRow("SELECT COUNT(*) FROM design_files WHERE design_id=?", designID).Scan(&total)
	if total != 2 {
		t.Fatalf("expected 2 versions, got %d", total)
	}

	// The new blob lies in the CAS store.
	var blobHash string
	database.QueryRow(`SELECT dfe.blob_hash FROM design_file_entries dfe JOIN design_files df ON df.id=dfe.design_file_id
		WHERE df.design_id=? AND df.is_current=1 LIMIT 1`, designID).Scan(&blobHash)
	if blobHash == "" {
		t.Fatal("blob_hash of the new version is missing")
	}
	if _, failure := os.Stat(owner.Layout.Blob(blobHash)); failure != nil {
		t.Fatalf("blob file missing in the CAS store: %v", failure)
	}
}
