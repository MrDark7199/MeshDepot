package platforms

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"meshdepot/internal/blobstore"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/printmeta"
	"meshdepot/internal/publicid"
	"meshdepot/internal/storage"
)

var unsafeChars = regexp.MustCompile(`[^\w\-./() ]`)

// nullIfEmpty stores an empty string as SQL NULL, so a file without print
// settings is distinguishable from one whose settings are empty.
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

type storedEntry struct {
	filename, path, relativePath string
	// blobHash addresses the stored bytes; contentHash identifies what the file
	// holds and is what a later sync compares against. They differ for an archive
	// that gets rebuilt on every download - see ContentHash.
	blobHash, contentHash string
	size                  int64
}

// SaveDownload writes a download result into the library and returns the
// design_id: the design row, the files under {root}/{uid}/stl/{designId}/1.0,
// the design_files entries, tags and images.
func SaveDownload(db *sql.DB, owner Owner, platform, sourceURL string, result Result) (int, error) {
	// The cover is only published once the design id exists, since it decides the
	// directory; publishCover fills the column in afterwards.
	var coverPath any

	// One transaction. Without it a failure after the designs INSERT left the row
	// behind, and since deduplication runs on designs.source_url that URL was
	// permanently unqueueable: "design already exists" for a design with no files.
	//
	// db.Open sets SetMaxOpenConns(1), so while tx is open no call may go through
	// db or it waits for the only connection and deadlocks.
	transaction, failure := db.Begin()
	if failure != nil {
		return 0, failure
	}

	// Idempotent against source_url: a download that hit procTimeout is re-queued,
	// but the timed-out goroutine keeps running and would insert a second design.
	// First writer wins; the late one returns the existing id.
	if sourceURL != "" {
		var existingID int
		failure := transaction.QueryRow("SELECT id FROM designs WHERE user_id = ? AND source_url = ? LIMIT 1",
			owner.ID, sourceURL).Scan(&existingID)
		if failure == nil && existingID > 0 {
			_ = transaction.Rollback()
			return existingID, nil
		}
		// A failed probe is not an answer. Treated as "no such design" it would
		// insert a second one for the same URL - exactly what this guards
		// against - so the download fails instead and the queue retries it.
		if failure != nil && !errors.Is(failure, sql.ErrNoRows) {
			_ = transaction.Rollback()
			return 0, fmt.Errorf("checking for an existing design failed: %w", failure)
		}
	}

	committed := false
	var versionDir string
	defer func() {
		if committed {
			return
		}
		_ = transaction.Rollback()
		// The files were written under a directory named after the rolled-back design
		// id. SQLite reuses rowids, so a later design could inherit it.
		if versionDir != "" {
			_ = os.RemoveAll(versionDir)
		}
	}()

	// Set right away, or the auto-sync immediately re-syncs a freshly downloaded
	// design and keeps creating versions.
	insertResult, failure := transaction.Exec(
		`INSERT INTO designs (user_id, public_id, name, description, source_url, source_platform, source_id, author, cover_path, last_synced_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		owner.ID, publicid.New(), result.Name, nullStr(result.Description), sourceURL, platform, nullStr(result.SourceID), nullStr(result.Author), coverPath)
	if failure != nil {
		return 0, failure
	}
	insertedID, _ := insertResult.LastInsertId()
	designID := int(insertedID)

	versionDir = owner.Layout.Version(designID, "1.0")
	_ = storage.MkdirAll(versionDir)

	var entries []storedEntry
	var totalBytes int64
	for _, file := range result.Files {
		fileInfo, failure := os.Stat(file.TempPath)
		if failure != nil || fileInfo.Size() == 0 {
			continue
		}
		relativePath := sanitizeRel(file.Name)
		destination := filepath.Join(versionDir, relativePath)
		if relativePath == "" || !withinBase(versionDir, destination) {
			continue
		}
		if _, failure := os.Stat(destination); failure == nil {
			extension := filepath.Ext(relativePath)
			relativePath = strings.TrimSuffix(relativePath, extension) + "_" + strconv.FormatInt(time.Now().Unix(), 10) + extension
			destination = filepath.Join(versionDir, relativePath)
		}
		blob, failure := storeAndLink(owner.Layout, file.TempPath, destination)
		if failure != nil || blob.SizeBytes == 0 {
			continue
		}
		// Read before the temp file goes: the first version has to record the same kind
		// of hash the sync later compares against, or the next update check publishes a
		// version holding nothing new.
		stableHash := ContentHash(file.TempPath, blob.Hash)
		_ = os.Remove(file.TempPath)
		totalBytes += blob.SizeBytes
		entries = append(entries, storedEntry{
			filename: filepath.Base(destination), path: destination,
			blobHash: blob.Hash, contentHash: stableHash, relativePath: relativePath, size: blob.SizeBytes,
		})
	}
	if len(entries) == 0 {
		return 0, errors.New("error.no_files")
	}

	versionResult, failure := transaction.Exec(
		`INSERT INTO design_files (design_id, version, filename, path, size_bytes, file_count, is_current, notes)
		 VALUES (?, '1.0', ?, ?, ?, ?, 1, 'Downloaded')`,
		designID, entries[0].filename, owner.Layout.Rel(versionDir), totalBytes, len(entries))
	if failure != nil {
		return 0, failure
	}
	versionID, _ := versionResult.LastInsertId()
	for _, entry := range entries {
		if _, failure := transaction.Exec(`INSERT INTO design_file_entries (design_file_id, filename, path, size_bytes, file_hash, relative_path, blob_hash, gcode_meta)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, versionID, entry.filename, owner.Layout.Rel(entry.path), entry.size, entry.contentHash, entry.relativePath, entry.blobHash,
			nullIfEmpty(printmeta.ExtractFileJSON(entry.filename, entry.path))); failure != nil {
			return 0, failure
		}
	}

	AddTags(transaction, owner.ID, designID, result.Tags)

	gallery := publishImages(owner.Layout, designID, result.AllImages)
	for index, image := range gallery {
		dbutil.ExecLogged(transaction, "INSERT INTO design_images (design_id, path, sort_order) VALUES (?, ?, ?)", designID, image, index)
	}
	cover, fromGallery := publishCover(owner.Layout, designID, result.CoverPath, gallery)
	if cover != "" {
		dbutil.ExecLogged(transaction, "UPDATE designs SET cover_path=? WHERE id=?", cover, designID)
		// The gallery row gets the flag too. Which image is the cover is stored twice,
		// and setting only designs.cover_path left a design showing a cover on its card
		// while no image in its own gallery was marked as the one.
		if fromGallery != "" {
			dbutil.ExecLogged(transaction, "UPDATE design_images SET is_cover = (path = ?) WHERE design_id = ?", fromGallery, designID)
		}
	}
	if failure := transaction.Commit(); failure != nil {
		return 0, failure
	}
	committed = true
	return designID, nil
}

// AddTags creates the tags (unique per user) and links them to the design.
// Additive: a sync removes no manual tags, but a tag removed on the platform
// side stays as well.
func AddTags(db dbutil.Querier, userID, designID int, tags []string) {
	for _, rawTag := range tags {
		// Some platforms hand their text over HTML-escaped: "'decor" arrived as
		// "&#39;decor" and was stored and searched under that name. Decoded here rather
		// than per scraper, so a new platform cannot forget it.
		name := strings.TrimSpace(html.UnescapeString(rawTag))
		// Cut on a character boundary: slicing bytes would leave half a multi-byte
		// character behind, which is not valid UTF-8.
		if runes := []rune(name); len(runes) > 80 {
			name = strings.TrimSpace(string(runes[:80]))
		}
		if name == "" {
			continue
		}
		// Matched without regard to case, so "Decor" and "decor" from two platforms end
		// up on one tag. The UNIQUE index is case-sensitive, so both would otherwise
		// exist side by side and a filter on one would miss the other's designs.
		var tagID int
		found := db.QueryRow(
			"SELECT id FROM tags WHERE user_id=? AND name=? COLLATE NOCASE LIMIT 1", userID, name).Scan(&tagID) == nil && tagID > 0
		if !found {
			// The spelling of whoever gets there first is the one that is kept.
			dbutil.ExecLogged(db, "INSERT OR IGNORE INTO tags (user_id, name, color, source) VALUES (?, ?, '#457b9d', 'import')", userID, name)
			_ = db.QueryRow("SELECT id FROM tags WHERE user_id=? AND name=? COLLATE NOCASE LIMIT 1", userID, name).Scan(&tagID)
		}
		if tagID > 0 {
			dbutil.ExecLogged(db, "INSERT OR IGNORE INTO design_tags (design_id, tag_id) VALUES (?, ?)", designID, tagID)
		}
	}
}

// AddImages adds missing platform images by path, without touching existing or
// manually uploaded ones, or the chosen cover. Only a design with no image at
// all gets a cover set. A no-op on an empty list.
func AddImages(db *sql.DB, owner Owner, designID int, images []string) {
	images = publishImages(owner.Layout, designID, images)
	if len(images) == 0 {
		return
	}
	var existingCount, maxSort int
	db.QueryRow("SELECT COUNT(*), COALESCE(MAX(sort_order),0) FROM design_images WHERE design_id=?", designID).Scan(&existingCount, &maxSort)
	for _, image := range images {
		var count int
		db.QueryRow("SELECT COUNT(*) FROM design_images WHERE design_id=? AND path=?", designID, image).Scan(&count)
		if count > 0 {
			continue
		}
		maxSort++
		dbutil.ExecLogged(db, "INSERT INTO design_images (design_id, path, sort_order) VALUES (?, ?, ?)", designID, image, maxSort)
	}
	// Keyed on the missing cover rather than the missing images: a design that has
	// images but no cover shows the placeholder, and the old condition could never
	// become true again for it. A cover already chosen is left alone.
	var cover sql.NullString
	db.QueryRow("SELECT cover_path FROM designs WHERE id=?", designID).Scan(&cover)
	if cover.String == "" {
		first := images[0]
		if existingCount > 0 {
			// Its own gallery comes first; these images were only appended to it.
			db.QueryRow("SELECT path FROM design_images WHERE design_id=? ORDER BY sort_order ASC, created_at ASC LIMIT 1", designID).Scan(&first)
		}
		dbutil.ExecLogged(db, "UPDATE designs SET cover_path=? WHERE id=?", first, designID)
		dbutil.ExecLogged(db, "UPDATE design_images SET is_cover = (path = ?) WHERE design_id = ?", first, designID)
	}
}

// SaveSyncVersion stores a re-download as a new version. Files move into the
// content-addressed blob store, and a new design_files version is created only
// if at least one blob is new. changed=false means "already up to date".
func SaveSyncVersion(db *sql.DB, owner Owner, designID int, result Result, progress func(step, label string, current, total int)) (bool, string, int, error) {
	if progress == nil {
		progress = func(string, string, int, int) {}
	}

	knownHashes := map[string]bool{}
	if rows, failure := db.Query(
		`SELECT DISTINCT dfe.file_hash FROM design_file_entries dfe
		 JOIN design_files df ON df.id = dfe.design_file_id
		 WHERE df.design_id = ? AND dfe.file_hash IS NOT NULL`, designID); failure == nil {
		for rows.Next() {
			var hashValue sql.NullString
			if rows.Scan(&hashValue) == nil && hashValue.Valid {
				knownHashes[hashValue.String] = true
			}
		}
		rows.Close()
	}

	type syncEntry struct {
		filename, blobPath, relativePath string
		// blobHash addresses the stored bytes, contentHash decides whether this is new.
		// They differ for a repacked archive - see ContentHash in archivehash.go.
		blobHash, contentHash string
		size                  int64
	}
	var entries []syncEntry
	newBlobCount := 0
	total := len(result.Files)
	for index, file := range result.Files {
		fileInfo, failure := os.Stat(file.TempPath)
		if failure != nil || fileInfo.Size() == 0 {
			continue
		}
		relativePath := sanitizeRel(file.Name)
		base := filepath.Base(relativePath)

		sourceFile, failure := os.Open(file.TempPath)
		if failure != nil {
			continue
		}
		blob, failure := blobstore.StoreReader(owner.Layout, sourceFile)
		sourceFile.Close()
		if failure != nil {
			continue
		}
		// Read before the temp file goes: a rebuilt archive has different bytes every
		// time, so comparing those would publish an empty version per sync.
		stableHash := ContentHash(file.TempPath, blob.Hash)
		_ = os.Remove(file.TempPath)

		if !knownHashes[stableHash] {
			newBlobCount++
			progress("store", fmt.Sprintf("New file %d/%d: %s", index+1, total, base), index+1, total)
		} else {
			progress("skip", "Unchanged: "+base, index+1, total)
		}
		entries = append(entries, syncEntry{
			filename: base, blobPath: blob.Path, relativePath: relativePath,
			blobHash: blob.Hash, contentHash: stableHash, size: blob.SizeBytes,
		})
	}

	if len(entries) == 0 {
		return false, "", 0, errors.New("error.no_files")
	}
	if newBlobCount == 0 {
		return false, "", 0, nil // unchanged
	}

	// One transaction. Written piecemeal, a failure between the statements left the
	// design with no current version at all, or with a version listing no files.
	// The blob store stays outside: its files are content-addressed and shared, so
	// an orphan is harmless while a rollback must not delete what another version
	// references.
	//
	// db.Open sets SetMaxOpenConns(1) - no call may go through db while the
	// transaction is open, or it deadlocks.
	transaction, failure := db.Begin()
	if failure != nil {
		return false, "", 0, failure
	}
	defer transaction.Rollback()

	// Read inside the transaction, so a parallel sync of the same design cannot
	// hand out the same number twice.
	var maxVersion sql.NullFloat64
	_ = transaction.QueryRow("SELECT MAX(CAST(version AS REAL)) FROM design_files WHERE design_id=?", designID).Scan(&maxVersion)
	baseVersion := 1.0
	if maxVersion.Valid && maxVersion.Float64 > 0 {
		baseVersion = maxVersion.Float64
	}
	newVersion := fmt.Sprintf("%.1f", baseVersion+1.0)
	versionDir := owner.Layout.Version(designID, newVersion)

	// Every file is a hard link to the blob, so an unchanged file costs no disk in
	// the new version while the version still lists as a normal directory.
	var totalBytes int64
	published := make([]string, len(entries))
	for index, entry := range entries {
		destination := filepath.Join(versionDir, entry.relativePath)
		if entry.relativePath == "" || !withinBase(versionDir, destination) {
			continue
		}
		if failure := blobstore.Link(entry.blobPath, destination); failure != nil {
			return false, "", 0, failure
		}
		published[index] = destination
		totalBytes += entry.size
	}

	if _, failure := transaction.Exec("UPDATE design_files SET is_current=0 WHERE design_id=?", designID); failure != nil {
		return false, "", 0, failure
	}
	versionResult, failure := transaction.Exec(
		`INSERT INTO design_files (design_id, version, filename, path, size_bytes, file_count, is_current, notes)
		 VALUES (?, ?, ?, ?, ?, ?, 1, 'Synced')`,
		designID, newVersion, entries[0].filename, owner.Layout.Rel(versionDir), totalBytes, len(entries))
	if failure != nil {
		return false, "", 0, failure
	}
	versionID, _ := versionResult.LastInsertId()
	for index, entry := range entries {
		if published[index] == "" {
			continue
		}
		if _, failure := transaction.Exec(
			`INSERT INTO design_file_entries (design_file_id, filename, path, size_bytes, file_hash, relative_path, blob_hash, gcode_meta)
			 VALUES (?,?,?,?,?,?,?,?)`,
			versionID, entry.filename, owner.Layout.Rel(published[index]), entry.size, entry.contentHash, entry.relativePath, entry.blobHash,
			nullIfEmpty(printmeta.ExtractFileJSON(entry.filename, published[index]))); failure != nil {
			return false, "", 0, failure
		}
	}
	if _, failure := transaction.Exec("UPDATE designs SET updated_at=CURRENT_TIMESTAMP WHERE id=?", designID); failure != nil {
		return false, "", 0, failure
	}
	if failure := transaction.Commit(); failure != nil {
		return false, "", 0, failure
	}

	return true, newVersion, len(entries), nil
}

// sanitizeRel cleans a relative path. It splits on the separator and drops every
// empty, "." and ".." segment, so no combination can reconstruct a traversal -
// the previous single-pass ReplaceAll was bypassable, since "....//x" collapses
// back to "../x".
func sanitizeRel(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	var parts []string
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		parts = append(parts, unsafeChars.ReplaceAllString(segment, "_"))
	}
	return strings.Join(parts, "/")
}

// withinBase reports whether target stays inside base, on top of sanitizeRel.
func withinBase(base, target string) bool {
	relative, failure := filepath.Rel(base, target)
	if failure != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func copyHash(source, destination string) (string, int64, error) {
	sourceFile, failure := os.Open(source)
	if failure != nil {
		return "", 0, failure
	}
	defer sourceFile.Close()
	destinationFile, failure := os.Create(destination)
	if failure != nil {
		return "", 0, failure
	}
	hasher := sha256.New()
	size, failure := io.Copy(io.MultiWriter(destinationFile, hasher), sourceFile)
	destinationFile.Close()
	if failure != nil {
		return "", 0, failure
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func nullStr(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// storeAndLink puts a temp file into the user's blob store and publishes it at
// destination as a hard link, so the version directory holds a real file while
// the content is stored once.
func storeAndLink(user storage.UserLayout, tempPath, destination string) (blobstore.Info, error) {
	sourceFile, failure := os.Open(tempPath)
	if failure != nil {
		return blobstore.Info{}, failure
	}
	blob, failure := blobstore.StoreReader(user, sourceFile)
	sourceFile.Close()
	if failure != nil {
		return blobstore.Info{}, failure
	}
	if failure := blobstore.Link(blob.Path, destination); failure != nil {
		return blobstore.Info{}, failure
	}
	return blob, nil
}

// publishImages moves staged images into the design's pictures directory and
// returns their root-relative paths. Already-relative paths belong to a design
// published earlier and pass through, which makes AddImages idempotent.
func publishImages(user storage.UserLayout, designID int, staged []string) []string {
	var stored []string
	for _, path := range staged {
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			stored = append(stored, path)
			continue
		}
		destination := filepath.Join(user.Pictures(designID), filepath.Base(path))
		if failure := storage.MkdirAll(filepath.Dir(destination)); failure != nil {
			continue
		}
		if failure := os.Rename(path, destination); failure != nil {
			continue
		}
		stored = append(stored, user.Rel(destination))
	}
	return stored
}

// publishedAs finds the published gallery entry a staged file was moved to;
// publishImages keeps the file name, so the base name identifies it.
func publishedAs(gallery []string, staged string) string {
	name := filepath.Base(staged)
	for _, published := range gallery {
		if filepath.Base(published) == name {
			return published
		}
	}
	return ""
}

// publishCover puts the title image at the design's own cover path, as a hard
// link to the gallery file rather than a copy.
//
// The second return value is the gallery entry it was made from, or "" when it
// came from outside. The caller needs it to mark that row as is_cover.
func publishCover(user storage.UserLayout, designID int, staged string, gallery []string) (string, string) {
	source := staged
	fromGallery := ""
	if source == "" {
		if len(gallery) == 0 {
			return "", ""
		}
		fromGallery = gallery[0]
		source = user.Abs(fromGallery)
	} else if filepath.IsAbs(source) {
		// Every downloader hands the cover in as one of the gallery images, which have
		// already been published - moving that file a second time fails, and the design
		// was then stored with no cover at all.
		if published := publishedAs(gallery, source); published != "" {
			fromGallery = published
			source = user.Abs(published)
		} else {
			published := publishImages(user, designID, []string{source})
			if len(published) == 0 {
				return "", ""
			}
			source = user.Abs(published[0])
		}
	} else {
		if published := publishedAs(gallery, source); published != "" {
			fromGallery = published
		}
		source = user.Abs(source)
	}
	destination := user.Cover(designID, filepath.Ext(source))
	if failure := blobstore.Link(source, destination); failure != nil {
		return user.Rel(source), fromGallery
	}
	return user.Rel(destination), fromGallery
}
