package api

import (
	"database/sql"
	"errors"
	"net/http"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/storage"
)

// The path columns hold a location relative to the data root, never an absolute
// path: the data directory is configurable. They are called StoredPath so no
// caller hands one to os.Open by reflex - the absPath methods are the only way
// from a row to the filesystem.

// Typed rows for the paths where a column decides something: who owns a design,
// which file is streamed, which version is current.
//
// On a map[string]any a renamed column is silent - "" or 0. For an owner check
// that fails closed, but for a path it means "file does not exist" and for
// is_current "was not current", so a version silently stops being promoted.
//
// Rows that go to the client unchanged stay maps: typing them would fix the JSON
// shape in Go and break the response format the frontend expects.

type designRow struct {
	ID              int
	UserID          int
	Name            string
	StoredCoverPath string
	SourceURL       string
	SourcePlatform  string
}

// designColumns matches scanDesign (alias d).
const designColumns = `d.id, d.user_id, COALESCE(d.name, ''), COALESCE(d.cover_path, ''),
	COALESCE(d.source_url, ''), COALESCE(d.source_platform, '')`

// scanDesign reads one row. found=false is a missing row, an error is an error -
// never the same thing.
func scanDesign(row *sql.Row) (designRow, bool, error) {
	var design designRow
	failure := row.Scan(&design.ID, &design.UserID, &design.Name, &design.StoredCoverPath, &design.SourceURL, &design.SourcePlatform)
	if errors.Is(failure, sql.ErrNoRows) {
		return designRow{}, false, nil
	}
	if failure != nil {
		return designRow{}, false, failure
	}
	return design, true, nil
}

// loadDesign reads through the given querier, so it also works inside an open
// transaction.
func loadDesign(querier dbutil.Querier, designID int) (designRow, bool, error) {
	return scanDesign(querier.QueryRow("SELECT "+designColumns+" FROM designs d WHERE d.id = ? LIMIT 1", designID))
}

type fileVersionRow struct {
	ID         int
	DesignID   int
	Version    string
	Filename   string
	StoredPath string
	SizeBytes  int64
	FileCount  int
	IsCurrent  bool
}

// fileVersionColumns matches scanFileVersion (alias df).
const fileVersionColumns = `df.id, df.design_id, COALESCE(df.version, ''), COALESCE(df.filename, ''),
	COALESCE(df.path, ''), COALESCE(df.size_bytes, 0), COALESCE(df.file_count, 0), COALESCE(df.is_current, 0)`

func scanFileVersion(row *sql.Row) (fileVersionRow, bool, error) {
	var version fileVersionRow
	var isCurrent int
	failure := row.Scan(&version.ID, &version.DesignID, &version.Version, &version.Filename,
		&version.StoredPath, &version.SizeBytes, &version.FileCount, &isCurrent)
	if errors.Is(failure, sql.ErrNoRows) {
		return fileVersionRow{}, false, nil
	}
	if failure != nil {
		return fileVersionRow{}, false, failure
	}
	version.IsCurrent = isCurrent == 1
	return version, true, nil
}

func (version fileVersionRow) absPath(layout storage.Layout) string {
	return layout.Abs(version.StoredPath)
}

func loadFileVersion(querier dbutil.Querier, fileID, designID int) (fileVersionRow, bool, error) {
	return scanFileVersion(querier.QueryRow(
		"SELECT "+fileVersionColumns+" FROM design_files df WHERE df.id = ? AND df.design_id = ? LIMIT 1", fileID, designID))
}

func loadFileVersionByID(querier dbutil.Querier, fileID int) (fileVersionRow, bool, error) {
	return scanFileVersion(querier.QueryRow(
		"SELECT "+fileVersionColumns+" FROM design_files df WHERE df.id = ? LIMIT 1", fileID))
}

type fileEntryRow struct {
	ID           int
	DesignFileID int
	Filename     string
	StoredPath   string
	RelativePath string
	FileHash     string
	BlobHash     string
	SizeBytes    int64
}

// fileEntryColumns matches scanFileEntry (alias dfe).
const fileEntryColumns = `dfe.id, dfe.design_file_id, COALESCE(dfe.filename, ''), COALESCE(dfe.path, ''),
	COALESCE(dfe.relative_path, ''), COALESCE(dfe.file_hash, ''), COALESCE(dfe.blob_hash, ''), COALESCE(dfe.size_bytes, 0)`

func entryScanTargets(entry *fileEntryRow) []any {
	return []any{&entry.ID, &entry.DesignFileID, &entry.Filename, &entry.StoredPath,
		&entry.RelativePath, &entry.FileHash, &entry.BlobHash, &entry.SizeBytes}
}

func scanFileEntry(row *sql.Row) (fileEntryRow, bool, error) {
	var entry fileEntryRow
	failure := row.Scan(entryScanTargets(&entry)...)
	if errors.Is(failure, sql.ErrNoRows) {
		return fileEntryRow{}, false, nil
	}
	if failure != nil {
		return fileEntryRow{}, false, failure
	}
	return entry, true, nil
}

// absPath resolves the stored file against the data root. Every read of an
// entry's bytes goes through here.
func (entry fileEntryRow) absPath(layout storage.Layout) string {
	return layout.Abs(entry.StoredPath)
}

// entryJoin restricts an entry to its version and design - every entry lookup
// goes through it, so no handler can serve a file of a foreign design.
const entryJoin = ` FROM design_file_entries dfe
	JOIN design_files df ON df.id = dfe.design_file_id
	WHERE df.id = ? AND df.design_id = ?`

// versionEntries lists a version's files in the order the UI and the ZIP export
// expect.
func versionEntries(querier dbutil.Querier, fileID int) ([]fileEntryRow, error) {
	rows, failure := querier.Query("SELECT "+fileEntryColumns+
		" FROM design_file_entries dfe WHERE dfe.design_file_id = ? ORDER BY dfe.sort_order ASC, dfe.relative_path ASC, dfe.filename ASC", fileID)
	if failure != nil {
		return nil, failure
	}
	defer rows.Close()
	var entries []fileEntryRow
	for rows.Next() {
		var entry fileEntryRow
		if failure := rows.Scan(entryScanTargets(&entry)...); failure != nil {
			return nil, failure
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

type designImageRow struct {
	ID         int
	DesignID   int
	StoredPath string
	SortOrder  int
}

// designImageColumns matches scanDesignImage (alias di).
const designImageColumns = `di.id, di.design_id, COALESCE(di.path, ''), COALESCE(di.sort_order, 0)`

func scanDesignImage(row *sql.Row) (designImageRow, bool, error) {
	var image designImageRow
	failure := row.Scan(&image.ID, &image.DesignID, &image.StoredPath, &image.SortOrder)
	if errors.Is(failure, sql.ErrNoRows) {
		return designImageRow{}, false, nil
	}
	if failure != nil {
		return designImageRow{}, false, failure
	}
	return image, true, nil
}

func (image designImageRow) absPath(layout storage.Layout) string {
	return layout.Abs(image.StoredPath)
}

// Same contract as fetchRow: they answer the request themselves and ok=false
// means the handler must return.

func (server *Server) requireFileVersion(responseWriter http.ResponseWriter, fileID, designID int) (fileVersionRow, bool) {
	version, found, failure := loadFileVersion(server.DB, fileID, designID)
	return version, server.rowOK(responseWriter, found, failure)
}

func (server *Server) requireEntry(responseWriter http.ResponseWriter, entryID, fileID, designID int) (fileEntryRow, bool) {
	entry, found, failure := scanFileEntry(server.DB.QueryRow(
		"SELECT "+fileEntryColumns+entryJoin+" AND dfe.id = ? LIMIT 1", fileID, designID, entryID))
	return entry, server.rowOK(responseWriter, found, failure)
}

// requireModelEntry loads the first 3D model file of a version, for the legacy
// STL endpoint that has no entry id.
func (server *Server) requireModelEntry(responseWriter http.ResponseWriter, fileID, designID int) (fileEntryRow, bool) {
	entry, found, failure := scanFileEntry(server.DB.QueryRow(
		"SELECT "+fileEntryColumns+entryJoin+`
		  AND (LOWER(dfe.filename) LIKE '%.stl' OR LOWER(dfe.filename) LIKE '%.obj' OR LOWER(dfe.filename) LIKE '%.3mf')
		ORDER BY dfe.filename ASC LIMIT 1`, fileID, designID))
	return entry, server.rowOK(responseWriter, found, failure)
}

func (server *Server) requireDesignImage(responseWriter http.ResponseWriter, imageID, designID int) (designImageRow, bool) {
	image, found, failure := scanDesignImage(server.DB.QueryRow(
		"SELECT "+designImageColumns+" FROM design_images di WHERE di.id = ? AND di.design_id = ? LIMIT 1", imageID, designID))
	return image, server.rowOK(responseWriter, found, failure)
}

// rowOK writes the response belonging to a failed or empty read and reports
// whether the caller may keep going.
func (server *Server) rowOK(responseWriter http.ResponseWriter, found bool, failure error) bool {
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return false
	}
	if !found {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return false
	}
	return true
}
