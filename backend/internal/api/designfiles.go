package api

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"math"
	"meshdepot/internal/coerce"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"meshdepot/internal/blobstore"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/platforms"
	"meshdepot/internal/printmeta"
	"meshdepot/internal/storage"
)

const downloadTokenTTL = 30 * time.Minute

// storedFile is the metadata of a file placed in the blob store. gcodeMeta holds
// the JSON of the extracted print parameters for sliced files (G-code as well as
// the resin formats), otherwise empty. The column keeps its historic name.
type storedFile struct {
	filename, blobPath, fileHash, blobHash, relativePath string
	size                                                 int64
	gcodeMeta                                            string
}

// maxVersionNumber bounds a version number. The value ends up in path names and
// is sorted with CAST(version AS REAL); anything beyond this is a typo, not a
// version.
const maxVersionNumber = 100000

// numericVersion checks whether value is a plain number (decimals allowed: 3, 3.0, 2.1).
//
// ParseFloat also accepts "NaN", "Inf" and overflowing literals like "1e400".
// Those would be formatted into path names as "NaN"/"+Inf" and make the
// CAST(version AS REAL) ordering in SaveSyncVersion unpredictable, so they are
// rejected along with negative values.
func numericVersion(value string) bool {
	if value == "" {
		return false
	}
	parsed, failure := strconv.ParseFloat(value, 64)
	if failure != nil {
		return false
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return false
	}
	return parsed >= 0 && parsed <= maxVersionNumber
}

// decodeEntryMeta replaces the gcode_meta JSON field (string from the DB) with a
// real object so the frontend can read the print parameters directly.
func decodeEntryMeta(entries []map[string]any) {
	for _, entry := range entries {
		metaJSON, ok := entry["gcode_meta"].(string)
		if !ok || metaJSON == "" {
			delete(entry, "gcode_meta")
			continue
		}
		var decoded map[string]any
		if json.Unmarshal([]byte(metaJSON), &decoded) == nil {
			entry["gcode_meta"] = decoded
		} else {
			delete(entry, "gcode_meta")
		}
	}
}

// requireDesignAccess grants access for the owner OR shared users.
func (server *Server) requireDesignAccess(responseWriter http.ResponseWriter, designID, currentUserID int) (designRow, bool) {
	design, found, failure := scanDesign(server.DB.QueryRow("SELECT "+designColumns+` FROM designs d
		WHERE d.id = ? AND (d.user_id = ? OR EXISTS (
			SELECT 1 FROM design_shares ds WHERE ds.design_id = d.id AND ds.shared_with_user_id = ?))
		LIMIT 1`, designID, currentUserID, currentUserID))
	return design, server.rowOK(responseWriter, found, failure)
}

// requireOwnedDesign is requireDesignAccess plus the restriction to the owner:
// shared users may read a design, but may not change its files.
func (server *Server) requireOwnedDesign(responseWriter http.ResponseWriter, designID, currentUserID int) (designRow, bool) {
	design, ok := server.requireDesignAccess(responseWriter, designID, currentUserID)
	if !ok {
		return designRow{}, false
	}
	if design.UserID != currentUserID {
		httpx.Error(responseWriter, http.StatusForbidden, "error.unauthorized")
		return designRow{}, false
	}
	return design, true
}

// FilesIndex returns all versions of a design incl. their file entries.
func (server *Server) FilesIndex(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	if _, ok := server.requireDesignAccess(responseWriter, designID, currentUserID); !ok {
		return
	}
	versions, _ := dbutil.QueryMaps(server.DB, "SELECT * FROM design_files WHERE design_id = ? ORDER BY is_current DESC, created_at DESC", designID)
	for _, version := range versions {
		entries, _ := dbutil.QueryMaps(server.DB, "SELECT * FROM design_file_entries WHERE design_file_id = ? ORDER BY relative_path ASC, filename ASC", coerce.Int(version["id"]))
		decodeEntryMeta(entries)
		version["entries"] = entries
	}
	httpx.Success(responseWriter, versions)
}

// FilesStore uploads a new file version (multiple files possible; ZIP is
// extracted, everything into the blob store). The version number must be a number
// and must not already exist in the design. Only the owner may upload.
func (server *Server) FilesStore(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	if _, ok := server.requireOwnedDesign(responseWriter, designID, currentUserID); !ok {
		return
	}
	limitRequestBody(responseWriter, request, maxModelUpload)
	if failure := request.ParseMultipartForm(64 << 20); failure != nil {
		uploadError(responseWriter, failure, "error.file_required")
		return
	}
	headers := request.MultipartForm.File["file"]
	if len(headers) == 0 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.file_required")
		return
	}
	version := strings.TrimSpace(request.FormValue("version"))
	if !numericVersion(version) {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.version_invalid")
		return
	}
	_, versionExists, failure := dbutil.QueryMap(server.DB, "SELECT id FROM design_files WHERE design_id = ? AND version = ? LIMIT 1", designID, version)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if versionExists {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.version_exists")
		return
	}
	notes := strings.TrimSpace(request.FormValue("notes"))

	user := server.userLayout(request)
	stored, total := server.storeUploads(headers, user)
	if len(stored) == 0 {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}

	versionDir := user.Version(designID, version)
	var notesValue any
	if notes != "" {
		notesValue = notes
	}

	// Demoting the previous version, creating the new one and writing its entries
	// is one unit: an abort in between left the design either with no current
	// version at all or with a current version that has no entries - in both cases
	// the UI shows an empty file list and the upload is lost.
	transaction, failure := server.DB.Begin()
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	defer transaction.Rollback()

	if _, failure := transaction.Exec("UPDATE design_files SET is_current = 0 WHERE design_id = ?", designID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	insertResult, failure := transaction.Exec(
		`INSERT INTO design_files (design_id, version, filename, path, size_bytes, file_count, is_current, notes)
		 VALUES (?, ?, ?, ?, ?, ?, 1, ?)`,
		designID, version, headers[0].Filename, user.Rel(versionDir), total, len(stored), notesValue)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	versionID, _ := insertResult.LastInsertId()

	warnings, failure := server.insertEntries(transaction, int(versionID), designID, currentUserID, versionDir, stored)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	if _, failure := transaction.Exec("UPDATE designs SET updated_at = CURRENT_TIMESTAMP WHERE id = ?", designID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	saved, found, failure := dbutil.QueryMap(transaction, "SELECT * FROM design_files WHERE id = ? LIMIT 1", versionID)
	if failure != nil || !found {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	if failure := transaction.Commit(); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}

	saved["duplicate_warnings"] = warnings
	httpx.SuccessStatus(responseWriter, http.StatusCreated, saved, "File uploaded")
}

// FilesAddEntries adds further files (multiselect, ZIP allowed) into an EXISTING
// version without creating a new version.
func (server *Server) FilesAddEntries(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	fileID, ok := pathInt(request, "fileId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireOwnedDesign(responseWriter, designID, currentUserID); !ok {
		return
	}
	versionRow, ok := server.fetchRow(responseWriter, "SELECT id, COALESCE(version, '1.0') AS version FROM design_files WHERE id = ? AND design_id = ? LIMIT 1", fileID, designID)
	if !ok {
		return
	}
	limitRequestBody(responseWriter, request, maxModelUpload)
	if failure := request.ParseMultipartForm(64 << 20); failure != nil {
		uploadError(responseWriter, failure, "error.file_required")
		return
	}
	headers := request.MultipartForm.File["file"]
	if len(headers) == 0 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.file_required")
		return
	}
	// Collect existing filenames of this version (case-insensitive) to prevent name
	// collisions within the version.
	existing, _ := dbutil.QueryMaps(server.DB, "SELECT filename FROM design_file_entries WHERE design_file_id = ?", fileID)
	existingNames := make(map[string]bool, len(existing))
	for _, entry := range existing {
		existingNames[strings.ToLower(coerce.StringOr(entry["filename"], ""))] = true
	}

	stored, total := server.storeUploads(headers, server.userLayout(request))
	if len(stored) == 0 {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	// If one of the (possibly ZIP-extracted) filenames collides with a file already
	// present in the version, the version is not changed.
	for _, storedItem := range stored {
		if existingNames[strings.ToLower(storedItem.filename)] {
			httpx.Error(responseWriter, http.StatusConflict, "error.filename_exists")
			return
		}
	}
	// Entries and the counters of their version must land together: entries
	// without the counter update make the version report a wrong file count and
	// size forever, and there is no place that recomputes it.
	transaction, failure := server.DB.Begin()
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	defer transaction.Rollback()

	warnings, failure := server.insertEntries(transaction, fileID, designID, currentUserID, server.userLayout(request).Version(designID, coerce.StringOr(versionRow["version"], "1.0")), stored)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	writeFailed := false
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"UPDATE design_files SET size_bytes = size_bytes + ?, file_count = file_count + ? WHERE id = ?", []any{total, len(stored), fileID}},
		{"UPDATE designs SET updated_at = CURRENT_TIMESTAMP WHERE id = ?", []any{designID}},
	} {
		if _, failure := transaction.Exec(statement.query, statement.args...); failure != nil {
			log.Printf("[api] add entries: %v", failure)
			writeFailed = true
		}
	}
	saved, found, failure := dbutil.QueryMap(transaction, "SELECT * FROM design_files WHERE id = ? LIMIT 1", fileID)
	entries, entriesFailure := dbutil.QueryMaps(transaction, "SELECT * FROM design_file_entries WHERE design_file_id = ? ORDER BY relative_path ASC, filename ASC", fileID)
	if writeFailed || failure != nil || !found || entriesFailure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	if failure := transaction.Commit(); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	decodeEntryMeta(entries)
	saved["entries"] = entries
	saved["duplicate_warnings"] = warnings
	httpx.SuccessStatus(responseWriter, http.StatusCreated, saved, "Files added")
}

// storeUploads places all uploaded files in the blob store (ZIP is extracted) and
// parses G-code metadata. Returns all entries and the total size.
func (server *Server) storeUploads(headers []*multipart.FileHeader, user storage.UserLayout) ([]storedFile, int64) {
	var stored []storedFile
	var total int64
	for _, fileHeader := range headers {
		file, failure := fileHeader.Open()
		if failure != nil {
			continue
		}
		switch {
		case strings.EqualFold(filepath.Ext(fileHeader.Filename), ".zip"):
			extracted, subtotal := server.extractZip(file, fileHeader.Size, user)
			stored = append(stored, extracted...)
			total += subtotal
		default:
			if info, storeFailure := blobstore.StoreReader(user, file); storeFailure == nil {
				stored = append(stored, storedFile{filename: fileHeader.Filename, blobPath: info.Path, size: info.SizeBytes, fileHash: platforms.ContentHash(info.Path, info.Hash), blobHash: info.Hash, relativePath: fileHeader.Filename, gcodeMeta: printmeta.ExtractFileJSON(fileHeader.Filename, info.Path)})
				total += info.SizeBytes
			}
		}
		file.Close()
	}
	return stored, total
}

// insertEntries writes the file entries of a version and collects duplicate
// warnings (same hash in another design of the same user).
//
// It takes a querier rather than reaching for server.DB: the callers run inside
// a transaction, and with SetMaxOpenConns(1) a second connection would deadlock
// against the open one. A failed insert is returned instead of only logged, so
// the caller can roll the version back rather than publish a version whose
// entry list is short a file.
func (server *Server) insertEntries(querier dbutil.Querier, versionID, designID, currentUserID int, versionDir string, stored []storedFile) ([]map[string]any, error) {
	warnings := []map[string]any{}
	for _, storedItem := range stored {
		// The blob carries the content, the version directory the structure: the
		// entry is published as a hard link so the version stays a directory of
		// real files without storing an unchanged file twice.
		published := filepath.Join(versionDir, sanitizeUploadPath(storedItem.relativePath, storedItem.filename))
		if failure := blobstore.Link(storedItem.blobPath, published); failure != nil {
			return nil, failure
		}
		var metaValue any
		if storedItem.gcodeMeta != "" {
			metaValue = storedItem.gcodeMeta
		}
		if _, failure := querier.Exec(
			`INSERT INTO design_file_entries (design_file_id, filename, path, size_bytes, file_hash, relative_path, blob_hash, gcode_meta)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			versionID, storedItem.filename, server.layout().Rel(published), storedItem.size, storedItem.fileHash, storedItem.relativePath, storedItem.blobHash, metaValue); failure != nil {
			return nil, failure
		}
		if storedItem.fileHash != "" {
			duplicateDesigns, _ := dbutil.QueryMaps(querier, `
				SELECT d.name, d.id FROM design_file_entries dfe
				JOIN design_files df ON df.id = dfe.design_file_id
				JOIN designs d ON d.id = df.design_id
				WHERE dfe.file_hash = ? AND df.design_id != ? AND d.user_id = ? LIMIT 3`, storedItem.fileHash, designID, currentUserID)
			for _, duplicate := range duplicateDesigns {
				warnings = append(warnings, map[string]any{"filename": storedItem.filename, "design_id": duplicate["id"], "design_name": duplicate["name"]})
			}
		}
	}
	return warnings, nil
}

// extractZip unpacks a ZIP, stores each file as a blob and removes a common
// top-level folder. On error the ZIP itself is stored as a blob.
func (server *Server) extractZip(readerAt io.ReaderAt, size int64, user storage.UserLayout) ([]storedFile, int64) {
	zipReader, failure := zip.NewReader(readerAt, size)
	if failure != nil {
		if reader, ok := readerAt.(io.Reader); ok {
			if info, storeFailure := blobstore.StoreReader(user, reader); storeFailure == nil {
				return []storedFile{{filename: "upload.zip", blobPath: info.Path, size: info.SizeBytes, fileHash: platforms.ContentHash(info.Path, info.Hash), blobHash: info.Hash, relativePath: "upload.zip"}}, info.SizeBytes
			}
		}
		return nil, 0
	}
	// Detect a common top folder.
	var names []string
	for _, zipFile := range zipReader.File {
		if !strings.HasSuffix(zipFile.Name, "/") {
			names = append(names, zipFile.Name)
		}
	}
	topFolder := ""
	if len(names) > 0 && strings.Contains(names[0], "/") {
		candidate := strings.SplitN(names[0], "/", 2)[0]
		all := true
		for _, entryName := range names {
			if !strings.HasPrefix(entryName, candidate+"/") {
				all = false
				break
			}
		}
		if all {
			topFolder = candidate + "/"
		}
	}

	var stored []storedFile
	var total int64
	for _, zipFile := range zipReader.File {
		if strings.HasSuffix(zipFile.Name, "/") {
			continue
		}
		basename := path.Base(zipFile.Name)
		if basename == "" || strings.HasPrefix(basename, ".") {
			continue
		}
		relativePath := zipFile.Name
		if topFolder != "" {
			relativePath = strings.TrimPrefix(relativePath, topFolder)
		}
		relativePath = strings.TrimLeft(strings.ReplaceAll(strings.ReplaceAll(relativePath, "../", ""), "..\\", ""), "/")
		// Declared size first: a zip entry may expand to far more than the
		// archive itself (zip bomb), and the upload limit only bounds the
		// compressed bytes. The LimitReader then bounds a lying header too.
		if zipFile.UncompressedSize64 > maxZipEntryBytes {
			continue
		}
		readCloser, failure := zipFile.Open()
		if failure != nil {
			continue
		}
		data, failure := io.ReadAll(io.LimitReader(readCloser, maxZipEntryBytes))
		readCloser.Close()
		if failure != nil {
			continue
		}
		info, failure := blobstore.StoreBytes(user, data)
		if failure != nil {
			continue
		}
		total += info.SizeBytes
		stored = append(stored, storedFile{filename: basename, blobPath: info.Path, size: info.SizeBytes, fileHash: platforms.ContentHash(info.Path, info.Hash), blobHash: info.Hash, relativePath: relativePath, gcodeMeta: printmeta.ExtractBytesJSON(basename, data)})
	}
	return stored, total
}

// FilesDestroy deletes a version (blobs are kept); if needed promotes the newest
// remaining version to current.
func (server *Server) FilesDestroy(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	fileID, ok := pathInt(request, "fileId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireOwnedDesign(responseWriter, designID, currentUserID); !ok {
		return
	}
	fileVersion, ok := server.requireFileVersion(responseWriter, fileID, designID)
	if !ok {
		return
	}
	released := entriesToRelease(server.DB, "dfe.design_file_id = ?", fileID)

	// Deleting the row and promoting a successor is one unit: in between, the
	// design has no current version, and nothing would ever set one again.
	transaction, failure := server.DB.Begin()
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	defer transaction.Rollback()

	if _, failure := transaction.Exec("DELETE FROM design_files WHERE id = ?", fileID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if !server.promoteNewestVersion(transaction, designID, fileVersion.IsCurrent) {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if failure := transaction.Commit(); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}

	// Only after the commit - a directory removed first would be gone even if the
	// delete failed.
	if versionDir := fileVersion.absPath(server.layout()); versionDir != "" {
		if fileInfo, failure := os.Stat(versionDir); failure == nil && fileInfo.IsDir() {
			_ = os.RemoveAll(versionDir)
		}
	}
	releaseEntries(server.userLayout(request), released)
	httpx.SuccessMessage(responseWriter, nil, "Deleted")
}

// FilesDeleteEntry deletes a single file (entry) from a version. If the version
// becomes empty it is removed entirely and, if needed, the newest remaining
// version is promoted to current. The published file goes with the row, and the
// blob behind it once no other version links the same content. Only the owner
// may delete.
func (server *Server) FilesDeleteEntry(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	fileID, ok := pathInt(request, "fileId")
	entryID, ok2 := pathInt(request, "entryId")
	if !ok || !ok2 {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireOwnedDesign(responseWriter, designID, currentUserID); !ok {
		return
	}
	if _, ok := server.requireEntry(responseWriter, entryID, fileID, designID); !ok {
		return
	}
	released := entriesToRelease(server.DB, "dfe.id = ?", entryID)

	// Delete, recount and (if the version is now empty) drop the version: without a
	// transaction a failure in the middle leaves the version with counters that no
	// longer match its entries.
	transaction, failure := server.DB.Begin()
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	defer transaction.Rollback()

	if _, failure := transaction.Exec("DELETE FROM design_file_entries WHERE id = ?", entryID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	// Recompute the version counter/size from the remaining entries. The error must
	// be checked: read as "no entries left" it would drop the whole version (plus its
	// entries via ON DELETE CASCADE) because of a transient busy timeout.
	aggregate, found, failure := dbutil.QueryMap(transaction, "SELECT COUNT(*) AS cnt, COALESCE(SUM(size_bytes), 0) AS total FROM design_file_entries WHERE design_file_id = ?", fileID)
	if failure != nil || !found {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	writeFailed := false
	if coerce.Int(aggregate["cnt"]) == 0 {
		fileVersion, versionFound, versionFailure := loadFileVersionByID(transaction, fileID)
		if versionFailure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
			return
		}
		if _, failure := transaction.Exec("DELETE FROM design_files WHERE id = ?", fileID); failure != nil {
			writeFailed = true
		}
		if !server.promoteNewestVersion(transaction, designID, versionFound && fileVersion.IsCurrent) {
			writeFailed = true
		}
	} else if _, failure := transaction.Exec("UPDATE design_files SET file_count = ?, size_bytes = ? WHERE id = ?", coerce.Int(aggregate["cnt"]), coerce.Int(aggregate["total"]), fileID); failure != nil {
		writeFailed = true
	}
	if _, failure := transaction.Exec("UPDATE designs SET updated_at = CURRENT_TIMESTAMP WHERE id = ?", designID); failure != nil {
		writeFailed = true
	}
	if writeFailed {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if failure := transaction.Commit(); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	releaseEntries(server.userLayout(request), released)
	httpx.SuccessMessage(responseWriter, nil, "Deleted")
}

// releasedEntry is a file a delete is about to drop: where it is published and
// which content it holds.
type releasedEntry struct {
	storedPath string
	blobHash   string
}

// entriesToRelease lists the published files behind a delete. It has to run
// before the rows go: afterwards nothing connects the design to its content any
// more, and the blobs would stay forever.
func entriesToRelease(querier dbutil.Querier, condition string, args ...any) []releasedEntry {
	rows, failure := querier.Query(`SELECT COALESCE(dfe.path, ''), COALESCE(NULLIF(dfe.blob_hash, ''), dfe.file_hash, '')
		FROM design_file_entries dfe
		JOIN design_files df ON df.id = dfe.design_file_id
		WHERE `+condition, args...)
	if failure != nil {
		return nil
	}
	defer rows.Close()
	var entries []releasedEntry
	for rows.Next() {
		var entry releasedEntry
		if rows.Scan(&entry.storedPath, &entry.blobHash) == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// releaseEntries removes the published files and, with the last of them, the
// blob behind the content. Called after the commit, never before: an orphan
// blob only costs disk, while content another version still links must not go.
// The link count decides, so a file shared with a second version survives.
func releaseEntries(user storage.UserLayout, entries []releasedEntry) {
	for _, entry := range entries {
		blobPath := ""
		if entry.blobHash != "" {
			blobPath = user.Blob(entry.blobHash)
		}
		if failure := blobstore.Unlink(blobPath, user.Abs(entry.storedPath)); failure != nil {
			log.Printf("[api] release %s: %v", entry.storedPath, failure)
		}
	}
}

// promoteNewestVersion makes the newest remaining version of a design the current
// one. It is a no-op unless the version just deleted was the current one; ok=false
// means a statement failed and the caller must roll back. A design without a
// current version shows an empty file list even though its files are still there.
func (server *Server) promoteNewestVersion(querier dbutil.Querier, designID int, wasCurrent bool) bool {
	if !wasCurrent {
		return true
	}
	newest, found, failure := dbutil.QueryMap(querier, "SELECT id FROM design_files WHERE design_id = ? ORDER BY created_at DESC LIMIT 1", designID)
	if failure != nil {
		return false
	}
	if !found {
		return true // that was the last version - nothing left to promote
	}
	if _, failure := querier.Exec("UPDATE design_files SET is_current = 1 WHERE id = ?", coerce.Int(newest["id"])); failure != nil {
		return false
	}
	return true
}

// FilesDownload returns all files of a version as a ZIP (or the single file directly).
func (server *Server) FilesDownload(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	fileID, ok := pathInt(request, "fileId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireDesignAccess(responseWriter, designID, currentUserID); !ok {
		return
	}
	fileVersion, ok3 := server.requireFileVersion(responseWriter, fileID, designID)
	if !ok3 {
		return
	}
	entries, failure := versionEntries(server.DB, fileID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	layout := server.layout()
	if len(entries) == 1 {
		if single := entries[0].absPath(layout); fileExists(single) {
			serveAttachment(responseWriter, single, entryFilename(entries[0]))
			return
		}
	}
	version := fileVersion.Version
	if version == "" {
		version = "1"
	}
	responseWriter.Header().Set("Content-Type", "application/zip")
	responseWriter.Header().Set("Content-Disposition", `attachment; filename="design_v`+version+`.zip"`)
	zipWriter := zip.NewWriter(responseWriter)
	defer zipWriter.Close()
	for _, entry := range entries {
		entryPath := entry.absPath(layout)
		if !fileExists(entryPath) {
			continue
		}
		// relative_path keeps the directory structure the downloader stored; using the
		// bare filename would collapse parts/left/body.stl and parts/right/body.stl
		// into two entries named body.stl, and most unpackers silently keep only one.
		zipEntryWriter, failure := zipWriter.Create(zipEntryName(entry))
		if failure != nil {
			continue
		}
		sourceFile, failure := os.Open(entryPath)
		if failure != nil {
			continue
		}
		io.Copy(zipEntryWriter, sourceFile)
		sourceFile.Close()
	}
}

// entryFilename is the name a single file is offered under - never empty, so a
// download does not end up unnamed.
func entryFilename(entry fileEntryRow) string {
	if entry.Filename == "" {
		return "file"
	}
	return entry.Filename
}

// zipEntryName is the path an entry gets inside the exported ZIP: the stored
// relative_path (so the directory layout survives the round trip), falling back to
// the bare filename. Leading slashes and ".." segments are dropped - a ZIP that
// contains them makes careless extractors write outside the target directory.
func zipEntryName(entry fileEntryRow) string {
	name := entry.RelativePath
	if name == "" {
		name = entry.Filename
	}
	var segments []string
	for _, segment := range strings.Split(filepath.ToSlash(name), "/") {
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		segments = append(segments, segment)
	}
	if len(segments) == 0 {
		return "file"
	}
	return strings.Join(segments, "/")
}

// FilesServeEntry streams a single file (3D preview), CORS open.
func (server *Server) FilesServeEntry(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	fileID, _ := pathInt(request, "fileId")
	entryID, ok := pathInt(request, "entryId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireDesignAccess(responseWriter, designID, currentUserID); !ok {
		return
	}
	entry, ok := server.requireEntry(responseWriter, entryID, fileID, designID)
	if !ok {
		return
	}
	entryPath := entry.absPath(server.layout())
	if !fileExists(entryPath) {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	serveInline(responseWriter, entryPath, entryFilename(entry))
}

// FilesServeStl returns the first STL/OBJ/3MF of a version (legacy).
func (server *Server) FilesServeStl(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	fileID, ok := pathInt(request, "fileId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireDesignAccess(responseWriter, designID, currentUserID); !ok {
		return
	}
	entry, ok := server.requireModelEntry(responseWriter, fileID, designID)
	if !ok {
		return
	}
	entryPath := entry.absPath(server.layout())
	if !fileExists(entryPath) {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	serveInline(responseWriter, entryPath, entryFilename(entry))
}

// FilesCreateToken creates a one-time download token (slicer without a session).
func (server *Server) FilesCreateToken(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	fileID, _ := pathInt(request, "fileId")
	entryID, ok := pathInt(request, "entryId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireDesignAccess(responseWriter, designID, currentUserID); !ok {
		return
	}
	if _, ok := server.requireEntry(responseWriter, entryID, fileID, designID); !ok {
		return
	}
	randomBytes := make([]byte, 32)
	_, _ = rand.Read(randomBytes)
	token := hex.EncodeToString(randomBytes)
	server.downloadTokens.Set(token, dlToken{designID: designID, fileVersionID: fileID, entryID: entryID}, downloadTokenTTL)
	httpx.JSON(responseWriter, http.StatusOK, map[string]string{"token": token})
}

// FilesServeByToken streams a file via a one-time token without a session.
func (server *Server) FilesServeByToken(responseWriter http.ResponseWriter, request *http.Request) {
	tokenData, ok := server.downloadTokens.Take(request.PathValue("token"))
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	entry, ok := server.requireEntry(responseWriter, tokenData.entryID, tokenData.fileVersionID, tokenData.designID)
	if !ok {
		return
	}
	entryPath := entry.absPath(server.layout())
	if !fileExists(entryPath) {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	serveAttachment(responseWriter, entryPath, entryFilename(entry))
}

func mimeForExt(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".stl":
		return "application/octet-stream"
	case ".obj":
		return "text/plain"
	case ".3mf":
		return "application/vnd.ms-package.3dmanufacturing-3dmodel+xml"
	default:
		return "application/octet-stream"
	}
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, failure := os.Stat(path)
	return failure == nil
}

// serveInline streams a file with open CORS (for the 3D preview).
func serveInline(responseWriter http.ResponseWriter, path, filename string) {
	file, failure := os.Open(path)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	defer file.Close()
	responseWriter.Header().Set("Content-Type", mimeForExt(filename))
	if fileInfo, failure := file.Stat(); failure == nil {
		responseWriter.Header().Set("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
	}
	io.Copy(responseWriter, file)
}

// serveAttachment streams a file as a download (Content-Disposition).
func serveAttachment(responseWriter http.ResponseWriter, path, filename string) {
	file, failure := os.Open(path)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	defer file.Close()
	safe := strings.NewReplacer("\r", "", "\n", "", `"`, "'").Replace(filename)
	responseWriter.Header().Set("Content-Type", mimeForExt(filename))
	responseWriter.Header().Set("Content-Disposition", `attachment; filename="`+safe+`"`)
	if fileInfo, failure := file.Stat(); failure == nil {
		responseWriter.Header().Set("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
	}
	io.Copy(responseWriter, file)
}

// sanitizeUploadPath picks the path an uploaded entry gets inside the version
// directory: the ZIP-relative path when it is usable, the bare filename
// otherwise. Traversal segments are dropped, so no upload can write outside its
// own version.
func sanitizeUploadPath(relativePath, filename string) string {
	candidate := relativePath
	if candidate == "" {
		candidate = filename
	}
	var segments []string
	for _, segment := range strings.Split(filepath.ToSlash(candidate), "/") {
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		segments = append(segments, segment)
	}
	if len(segments) == 0 {
		return filename
	}
	return filepath.Join(segments...)
}
