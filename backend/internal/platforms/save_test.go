package platforms

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"meshdepot/internal/db"
	"meshdepot/internal/storage"
)

func TestSaveDownload(t *testing.T) {
	database, failure := db.Open(filepath.Join(t.TempDir(), "p.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	owner := Owner{ID: 1, Layout: storage.New(t.TempDir()).User("a3f1c8e94b2d7f0a5c6e1b8d4a9f2c37")}

	// Two downloaded temp files (one in a subfolder).
	tempDir := t.TempDir()
	firstFile := filepath.Join(tempDir, "a.stl")
	secondFile := filepath.Join(tempDir, "b.obj")
	os.WriteFile(firstFile, []byte("solid a"), 0o644)
	os.WriteFile(secondFile, []byte("verts b"), 0o644)

	result := Result{
		Name:      "Cube",
		Author:    "me",
		SourceID:  "42",
		Files:     []DownloadedFile{{TempPath: firstFile, Name: "a.stl"}, {TempPath: secondFile, Name: "models/b.obj"}},
		Tags:      []string{"toy", "toy"}, // duplicate -> linked only once
		AllImages: []string{"1/cover.png", "1/extra.png"},
	}
	designID, failure := SaveDownload(database, owner, "thingiverse", "https://x/thing:42", result)
	if failure != nil {
		t.Fatalf("SaveDownload: %v", failure)
	}

	// Design
	var name, cover string
	database.QueryRow("SELECT name, cover_path FROM designs WHERE id=?", designID).Scan(&name, &cover)
	if name != "Cube" || cover != "1/cover.png" {
		t.Fatalf("design wrong: name=%q cover=%q", name, cover)
	}
	// Version + entries
	var fileCount, entryCount int
	database.QueryRow("SELECT file_count FROM design_files WHERE design_id=?", designID).Scan(&fileCount)
	database.QueryRow(`SELECT COUNT(*) FROM design_file_entries dfe JOIN design_files df ON df.id=dfe.design_file_id WHERE df.design_id=?`, designID).Scan(&entryCount)
	if fileCount != 2 || entryCount != 2 {
		t.Fatalf("expected 2 files/entries, got %d/%d", fileCount, entryCount)
	}
	// Temp files removed, target files exist
	if _, failure := os.Stat(firstFile); !os.IsNotExist(failure) {
		t.Fatal("temp file firstFile should be removed")
	}
	if _, failure := os.Stat(filepath.Join(owner.Layout.Version(designID, "1.0"), "models", "b.obj")); failure != nil {
		t.Fatalf("target file b.obj missing: %v", failure)
	}
	// Tags (duplicate deduplicated)
	var tagLinks int
	database.QueryRow("SELECT COUNT(*) FROM design_tags WHERE design_id=?", designID).Scan(&tagLinks)
	if tagLinks != 1 {
		t.Fatalf("expected 1 tag link, got %d", tagLinks)
	}
	var imageCount int
	database.QueryRow("SELECT COUNT(*) FROM design_images WHERE design_id=?", designID).Scan(&imageCount)
	if imageCount != 2 {
		t.Fatalf("expected 2 images, got %d", imageCount)
	}
	_ = sql.ErrNoRows
}

// The shape every real downloader produces: the images are staged as absolute
// paths in a temp directory, and the cover is the first of them - not a second,
// separate file. Publishing the gallery moves those files, so publishing the
// cover afterwards has to recognise the one it already moved. It did not, and
// left every downloaded design with images but no cover_path: the detail view
// found its gallery, while the overview card - which reads cover_path alone -
// kept the grey placeholder.
func TestSaveDownloadSetsTheCoverWhenItIsAlsoAGalleryImage(t *testing.T) {
	database, failure := db.Open(filepath.Join(t.TempDir(), "p.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	owner := Owner{ID: 1, Layout: storage.New(t.TempDir()).User("a3f1c8e94b2d7f0a5c6e1b8d4a9f2c37")}

	tempDir := t.TempDir()
	modelFile := filepath.Join(tempDir, "a.stl")
	os.WriteFile(modelFile, []byte("solid a"), 0o644)
	firstImage := filepath.Join(tempDir, "cover_1.png")
	secondImage := filepath.Join(tempDir, "cover_2.png")
	os.WriteFile(firstImage, []byte("png-one"), 0o644)
	os.WriteFile(secondImage, []byte("png-two"), 0o644)

	designID, failure := SaveDownload(database, owner, "thingiverse", "https://x/thing:43", Result{
		Name:      "Cube",
		Files:     []DownloadedFile{{TempPath: modelFile, Name: "a.stl"}},
		AllImages: []string{firstImage, secondImage},
		CoverPath: firstImage,
	})
	if failure != nil {
		t.Fatalf("SaveDownload: %v", failure)
	}

	var cover sql.NullString
	database.QueryRow("SELECT cover_path FROM designs WHERE id=?", designID).Scan(&cover)
	if !cover.Valid || cover.String == "" {
		t.Fatal("the design was stored without a cover")
	}
	if _, failure := os.Stat(owner.Layout.Abs(cover.String)); failure != nil {
		t.Fatalf("the cover path points at no file: %v", failure)
	}
	var imageCount int
	database.QueryRow("SELECT COUNT(*) FROM design_images WHERE design_id=?", designID).Scan(&imageCount)
	if imageCount != 2 {
		t.Fatalf("expected 2 gallery images, got %d", imageCount)
	}
}

// The green marker in the gallery comes from design_images.is_cover, a second
// record of the same fact as designs.cover_path. The downloader wrote only the
// latter, so a freshly downloaded design showed an image on its card while its
// own gallery marked none as the cover.
func TestSaveDownloadMarksTheCoverInTheGallery(t *testing.T) {
	database, failure := db.Open(filepath.Join(t.TempDir(), "p.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	owner := Owner{ID: 1, Layout: storage.New(t.TempDir()).User("a3f1c8e94b2d7f0a5c6e1b8d4a9f2c37")}

	tempDir := t.TempDir()
	modelFile := filepath.Join(tempDir, "a.stl")
	os.WriteFile(modelFile, []byte("solid a"), 0o644)
	firstImage := filepath.Join(tempDir, "cover_1.png")
	secondImage := filepath.Join(tempDir, "cover_2.png")
	os.WriteFile(firstImage, []byte("png-one"), 0o644)
	os.WriteFile(secondImage, []byte("png-two"), 0o644)

	designID, failure := SaveDownload(database, owner, "thingiverse", "https://x/thing:44", Result{
		Name:      "Cube",
		Files:     []DownloadedFile{{TempPath: modelFile, Name: "a.stl"}},
		AllImages: []string{firstImage, secondImage},
		CoverPath: firstImage,
	})
	if failure != nil {
		t.Fatalf("SaveDownload: %v", failure)
	}

	var marked int
	database.QueryRow("SELECT COUNT(*) FROM design_images WHERE design_id=? AND is_cover=1", designID).Scan(&marked)
	if marked != 1 {
		t.Fatalf("%d gallery images are marked as the cover, expected exactly one", marked)
	}
	var markedPath string
	database.QueryRow("SELECT path FROM design_images WHERE design_id=? AND is_cover=1", designID).Scan(&markedPath)
	if filepath.Base(markedPath) != "cover_1.png" {
		t.Fatalf("the marked image is %q, expected the one handed in as the cover", markedPath)
	}
}

// A sync repairs a design left without a cover. The condition used to be "no
// images yet", which such a design can never satisfy again - it has images, and
// only the cover is missing.
func TestAddImagesGivesACoverlessDesignItsCover(t *testing.T) {
	database, failure := db.Open(filepath.Join(t.TempDir(), "p.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	owner := Owner{ID: 1, Layout: storage.New(t.TempDir()).User("a3f1c8e94b2d7f0a5c6e1b8d4a9f2c37")}

	result, failure := database.Exec("INSERT INTO designs (user_id, name, source_platform) VALUES (1, 'Ohne Titelbild', 'thingiverse')")
	if failure != nil {
		t.Fatal(failure)
	}
	insertedID, _ := result.LastInsertId()
	designID := int(insertedID)
	database.Exec("INSERT INTO design_images (design_id, path, sort_order) VALUES (?, '1/first.png', 0)", designID)

	AddImages(database, owner, designID, []string{"1/second.png"})

	var cover sql.NullString
	database.QueryRow("SELECT cover_path FROM designs WHERE id=?", designID).Scan(&cover)
	if cover.String != "1/first.png" {
		t.Fatalf("the cover is %q, want the design's own first image", cover.String)
	}
}

// A cover the owner picked is theirs; a sync that brings new images must not
// move it.
func TestAddImagesKeepsAnExistingCover(t *testing.T) {
	database, failure := db.Open(filepath.Join(t.TempDir(), "p.db"))
	if failure != nil {
		t.Fatal(failure)
	}
	if failure := db.InitSchema(database); failure != nil {
		t.Fatal(failure)
	}
	owner := Owner{ID: 1, Layout: storage.New(t.TempDir()).User("a3f1c8e94b2d7f0a5c6e1b8d4a9f2c37")}

	result, failure := database.Exec("INSERT INTO designs (user_id, name, source_platform, cover_path) VALUES (1, 'Mit Titelbild', 'thingiverse', '1/chosen.png')")
	if failure != nil {
		t.Fatal(failure)
	}
	insertedID, _ := result.LastInsertId()
	designID := int(insertedID)
	database.Exec("INSERT INTO design_images (design_id, path, sort_order) VALUES (?, '1/chosen.png', 0)", designID)

	AddImages(database, owner, designID, []string{"1/new.png"})

	var cover sql.NullString
	database.QueryRow("SELECT cover_path FROM designs WHERE id=?", designID).Scan(&cover)
	if cover.String != "1/chosen.png" {
		t.Fatalf("the chosen cover was replaced by %q", cover.String)
	}
}
