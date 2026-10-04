package api

// Folders inside one version, made by hand.
//
// A folder that holds files needs no row of its own - it follows from the
// entries' relative_path, which is also what the ZIP export writes. An empty one
// does need a row, because creating a folder before putting anything in it is
// how people work, and it would otherwise be gone on the next reload. Listing
// the folders of a version therefore reads both sources.

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"meshdepot/internal/coerce"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/logx"
	"meshdepot/internal/storage"
)

const (
	// Deep enough for "Druckteile/Varianten/A", short of a path nobody can read.
	maxFolderDepth      = 4
	maxFolderPathLength = 200
	// More files than one folder of a version will ever hold by hand.
	maxEntriesPerOrder = 2000
)

// folderCharacters matches what is replaced in a folder name. The same set the
// platform importers allow, minus the slash, which separates the segments.
var folderCharacters = regexp.MustCompile(`[^\w\-. ()]`)

// ownedVersion reads the design and version of the request and insists the
// design belongs to the caller - folders are not something a shared design
// offers.
func (server *Server) ownedVersion(responseWriter http.ResponseWriter, request *http.Request) (designID, fileID int, ok bool) {
	designID, ok = server.designID(responseWriter, request, "designId")
	if !ok {
		return 0, 0, false
	}
	fileID, ok = pathInt(request, "fileId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return 0, 0, false
	}
	if _, owned := server.requireOwnedDesign(responseWriter, designID, userID(request)); !owned {
		return 0, 0, false
	}
	return designID, fileID, true
}

type folderBody struct {
	Path string `json:"path"`
	// DeleteFiles turns the folder's files into a deletion rather than a move.
	// Asked for explicitly, because there is no undoing it: a design file is gone
	// the moment it goes, and it may be one the platform no longer offers.
	DeleteFiles bool `json:"delete_files"`
}

type entryFolderBody struct {
	Folder string `json:"folder"`
}

type entryOrderBody struct {
	EntryIDs []int `json:"entry_ids"`
}

// cleanFolderPath turns what the client sent into a path inside the version.
// Empty segments, "." and ".." go - a crafted name must not reach out of the
// version directory - and the result is capped in depth and length. An empty
// string is a valid answer: it names the version's own root.
func cleanFolderPath(raw string) (string, bool) {
	var segments []string
	for _, segment := range strings.Split(strings.ReplaceAll(raw, "\\", "/"), "/") {
		segment = strings.TrimSpace(folderCharacters.ReplaceAllString(segment, "_"))
		segment = strings.Trim(segment, ".")
		if segment == "" {
			continue
		}
		segments = append(segments, segment)
	}
	if len(segments) > maxFolderDepth {
		return "", false
	}
	path := strings.Join(segments, "/")
	if len(path) > maxFolderPathLength {
		return "", false
	}
	return path, true
}

// folderOf is the folder an entry sits in, "" for the version's root.
func folderOf(relativePath string) string {
	if index := strings.LastIndex(relativePath, "/"); index >= 0 {
		return relativePath[:index]
	}
	return ""
}

// foldersOfVersion lists every folder of a version: the ones recorded while
// empty, and the ones the files describe, including the folders above them -
// "parts/left" without "parts" would leave the tree with a gap.
func foldersOfVersion(querier dbutil.Querier, fileID int) []string {
	seen := map[string]bool{}
	add := func(path string) {
		for path != "" {
			seen[path] = true
			path = folderOf(path)
		}
	}
	rows, failure := dbutil.QueryMaps(querier,
		"SELECT path FROM design_file_folders WHERE design_file_id = ?", fileID)
	if failure != nil {
		logx.Errorf("[folders] reading folders of version %d failed: %v", fileID, failure)
	}
	for _, row := range rows {
		add(coerce.StringOr(row["path"], ""))
	}
	entries, failure := dbutil.QueryMaps(querier,
		"SELECT COALESCE(relative_path, '') AS relative_path FROM design_file_entries WHERE design_file_id = ?", fileID)
	if failure != nil {
		logx.Errorf("[folders] reading entries of version %d failed: %v", fileID, failure)
	}
	for _, row := range entries {
		add(folderOf(coerce.StringOr(row["relative_path"], "")))
	}

	folders := make([]string, 0, len(seen))
	for path := range seen {
		folders = append(folders, path)
	}
	sort.Strings(folders)
	return folders
}

// FoldersCreate records an empty folder. A folder that already exists - as a row
// or through a file - is answered as success: the client asked for it to be
// there, and it is.
func (server *Server) FoldersCreate(responseWriter http.ResponseWriter, request *http.Request) {
	designID, fileID, ok := server.ownedVersion(responseWriter, request)
	if !ok {
		return
	}
	var body folderBody
	if failure := httpx.DecodeJSON(request, &body); failure != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	path, valid := cleanFolderPath(body.Path)
	if !valid || path == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.folder_name_invalid")
		return
	}
	if _, failure := server.DB.Exec(
		`INSERT INTO design_file_folders (design_file_id, path) VALUES (?, ?)
		 ON CONFLICT (design_file_id, path) DO NOTHING`, fileID, path); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	dbutil.ExecLogged(server.DB, "UPDATE designs SET updated_at = CURRENT_TIMESTAMP WHERE id = ?", designID)
	httpx.SuccessStatus(responseWriter, http.StatusCreated, map[string]any{"path": path}, "Folder created")
}

// FoldersDelete removes a folder. By default the files it holds are kept: they
// move up to the folder it sat in, keeping any structure below it, so "a/b/c/x.stl"
// becomes "a/c/x.stl" when "a/b" goes. With delete_files they are deleted along
// with it - the caller has to say so, since nothing brings them back.
func (server *Server) FoldersDelete(responseWriter http.ResponseWriter, request *http.Request) {
	designID, fileID, ok := server.ownedVersion(responseWriter, request)
	if !ok {
		return
	}
	var body folderBody
	if failure := httpx.DecodeJSON(request, &body); failure != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	path, valid := cleanFolderPath(body.Path)
	if !valid || path == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.folder_name_invalid")
		return
	}

	version, ok := server.requireFileVersion(responseWriter, fileID, designID)
	if !ok {
		return
	}
	entries, failure := versionEntries(server.DB, fileID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}

	if body.DeleteFiles {
		server.deleteFolderWithFiles(responseWriter, request, designID, fileID, path, entries)
		return
	}

	parent := folderOf(path)
	taken := map[string]bool{}
	for _, entry := range entries {
		if !underFolder(entry.RelativePath, path) {
			taken[strings.ToLower(entry.RelativePath)] = true
		}
	}
	// Worked out in full before anything moves: half a folder moved up and then a
	// refusal would leave the version in a state nobody asked for.
	type move struct {
		entry fileEntryRow
		to    string
	}
	var moves []move
	for _, entry := range entries {
		if !underFolder(entry.RelativePath, path) {
			continue
		}
		target := strings.TrimPrefix(entry.RelativePath, path+"/")
		if parent != "" {
			target = parent + "/" + target
		}
		if taken[strings.ToLower(target)] {
			httpx.Error(responseWriter, http.StatusConflict, "error.folder_merge_conflict")
			return
		}
		taken[strings.ToLower(target)] = true
		moves = append(moves, move{entry: entry, to: target})
	}

	layout := server.userLayout(request)
	versionDir := layout.Version(designID, versionNumber(version))
	for _, pending := range moves {
		if failure := server.moveEntry(pending.entry, versionDir, pending.to); failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
			return
		}
	}

	// The rows of the folder and of everything below it: their files now sit
	// somewhere else, so keeping them would resurrect empty folders.
	dbutil.ExecLogged(server.DB,
		"DELETE FROM design_file_folders WHERE design_file_id = ? AND (path = ? OR path LIKE ?)",
		fileID, path, path+"/%")
	removeEmptyDir(filepath.Join(versionDir, filepath.FromSlash(path)))
	httpx.Success(responseWriter, map[string]any{"moved": len(moves)})
}

// deleteFolderWithFiles drops the folder and everything in it. The bookkeeping
// is the same the per-file delete does - the version's counters, an empty
// version, the blobs - only for several files at once.
func (server *Server) deleteFolderWithFiles(responseWriter http.ResponseWriter, request *http.Request,
	designID, fileID int, path string, entries []fileEntryRow) {
	var doomed []int
	for _, entry := range entries {
		if underFolder(entry.RelativePath, path) {
			doomed = append(doomed, entry.ID)
		}
	}
	released := []releasedEntry{}
	for _, entryID := range doomed {
		released = append(released, entriesToRelease(server.DB, "dfe.id = ?", entryID)...)
	}

	// Without a transaction a failure in the middle leaves the version with
	// counters that no longer match its entries.
	transaction, failure := server.DB.Begin()
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	defer transaction.Rollback()

	for _, entryID := range doomed {
		if _, failure := transaction.Exec("DELETE FROM design_file_entries WHERE id = ?", entryID); failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
			return
		}
	}
	// Read rather than counted down from: a transient busy timeout must not be
	// mistaken for "no entries left", which would take the whole version with it.
	aggregate, found, failure := dbutil.QueryMap(transaction,
		"SELECT COUNT(*) AS cnt, COALESCE(SUM(size_bytes), 0) AS total FROM design_file_entries WHERE design_file_id = ?", fileID)
	if failure != nil || !found {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	writeFailed := false
	if coerce.Int(aggregate["cnt"]) == 0 {
		fileVersion, versionFound, versionFailure := loadFileVersionByID(transaction, fileID)
		if versionFailure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
			return
		}
		if _, failure := transaction.Exec("DELETE FROM design_files WHERE id = ?", fileID); failure != nil {
			writeFailed = true
		}
		if !server.promoteNewestVersion(transaction, designID, versionFound && fileVersion.IsCurrent) {
			writeFailed = true
		}
	} else if _, failure := transaction.Exec("UPDATE design_files SET file_count = ?, size_bytes = ? WHERE id = ?",
		coerce.Int(aggregate["cnt"]), coerce.Int(aggregate["total"]), fileID); failure != nil {
		writeFailed = true
	}
	if _, failure := transaction.Exec("DELETE FROM design_file_folders WHERE design_file_id = ? AND (path = ? OR path LIKE ?)",
		fileID, path, path+"/%"); failure != nil {
		writeFailed = true
	}
	if _, failure := transaction.Exec("UPDATE designs SET updated_at = CURRENT_TIMESTAMP WHERE id = ?", designID); failure != nil {
		writeFailed = true
	}
	if writeFailed {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	if failure := transaction.Commit(); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	// After the commit: an orphan blob only costs disk, while content another
	// version still links must not go.
	releaseEntries(server.userLayout(request), released)
	httpx.Success(responseWriter, map[string]any{"deleted": len(doomed)})
}

// EntryMove puts one file into a folder, or back into the version's root. The
// bytes are untouched: the published file is a hard link into the blob store and
// is simply renamed.
func (server *Server) EntryMove(responseWriter http.ResponseWriter, request *http.Request) {
	designID, fileID, ok := server.ownedVersion(responseWriter, request)
	if !ok {
		return
	}
	entryID, ok := pathInt(request, "entryId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	var body entryFolderBody
	if failure := httpx.DecodeJSON(request, &body); failure != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	folder, valid := cleanFolderPath(body.Folder)
	if !valid {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.folder_name_invalid")
		return
	}
	version, ok := server.requireFileVersion(responseWriter, fileID, designID)
	if !ok {
		return
	}
	entry, ok := server.requireEntry(responseWriter, entryID, fileID, designID)
	if !ok {
		return
	}

	target := entryFilename(entry)
	if folder != "" {
		target = folder + "/" + target
	}
	if strings.EqualFold(target, entry.RelativePath) {
		httpx.Success(responseWriter, map[string]any{"relative_path": entry.RelativePath})
		return
	}
	entries, failure := versionEntries(server.DB, fileID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	for _, other := range entries {
		if other.ID != entry.ID && strings.EqualFold(other.RelativePath, target) {
			httpx.Error(responseWriter, http.StatusConflict, "error.filename_exists")
			return
		}
	}

	layout := server.userLayout(request)
	versionDir := layout.Version(designID, versionNumber(version))
	if failure := server.moveEntry(entry, versionDir, target); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	// A folder the file was the last one in keeps its row if it had one, and is
	// otherwise gone - which is what "the files describe the folders" means.
	removeEmptyDir(filepath.Join(versionDir, filepath.FromSlash(folderOf(entry.RelativePath))))
	httpx.Success(responseWriter, map[string]any{"relative_path": target})
}

// EntriesReorder writes the order the user dragged a folder's files into. The
// list is one folder's worth, in the order it should read; everything else keeps
// the number it has, which is why two folders may both count from one - the
// files are grouped by folder before they are sorted.
func (server *Server) EntriesReorder(responseWriter http.ResponseWriter, request *http.Request) {
	designID, fileID, ok := server.ownedVersion(responseWriter, request)
	if !ok {
		return
	}
	var body entryOrderBody
	if failure := httpx.DecodeJSON(request, &body); failure != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	if len(body.EntryIDs) == 0 || len(body.EntryIDs) > maxEntriesPerOrder {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	if _, ok := server.requireFileVersion(responseWriter, fileID, designID); !ok {
		return
	}
	// Only the version's own files, so a stray id cannot reach into another
	// design - and a repeated one cannot be given two places.
	entries, failure := versionEntries(server.DB, fileID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	belongs := map[int]bool{}
	for _, entry := range entries {
		belongs[entry.ID] = true
	}
	seen := map[int]bool{}
	for _, entryID := range body.EntryIDs {
		if !belongs[entryID] || seen[entryID] {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
			return
		}
		seen[entryID] = true
	}

	transaction, failure := server.DB.Begin()
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	defer transaction.Rollback()
	// Counted from one: zero is what an entry carries that nobody has arranged,
	// and it has to keep sorting ahead of nothing in particular.
	for position, entryID := range body.EntryIDs {
		if _, failure := transaction.Exec(
			"UPDATE design_file_entries SET sort_order = ? WHERE id = ? AND design_file_id = ?",
			position+1, entryID, fileID); failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
			return
		}
	}
	if failure := transaction.Commit(); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.internal")
		return
	}
	httpx.Success(responseWriter, map[string]any{"ordered": len(body.EntryIDs)})
}

// moveEntry renames the published file and writes the new path. A file that is
// no longer on disk - a version restored by hand, say - only updates its rows,
// so the structure can still be put right.
func (server *Server) moveEntry(entry fileEntryRow, versionDir, target string) error {
	layout := server.layout()
	destination := filepath.Join(versionDir, filepath.FromSlash(target))
	source := entry.absPath(layout)
	if _, failure := os.Stat(source); failure == nil {
		if failure := storage.MkdirAll(filepath.Dir(destination)); failure != nil {
			return failure
		}
		if failure := os.Rename(source, destination); failure != nil {
			return failure
		}
	}
	// Last in its new folder, which is where a file dropped on the folder itself
	// belongs. A drop between two files sets the order right afterwards.
	var nextOrder int
	_ = server.DB.QueryRow("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM design_file_entries WHERE design_file_id = ?",
		entry.DesignFileID).Scan(&nextOrder)
	_, failure := server.DB.Exec(
		"UPDATE design_file_entries SET relative_path = ?, path = ?, sort_order = ? WHERE id = ?",
		target, layout.Rel(destination), nextOrder, entry.ID)
	return failure
}

// versionNumber is a version's own number, never empty: the directory on disk
// is named after it, and an empty one would point at the design directory.
func versionNumber(version fileVersionRow) string {
	if strings.TrimSpace(version.Version) == "" {
		return "1.0"
	}
	return version.Version
}

// underFolder says whether a path sits in that folder or below it.
func underFolder(relativePath, folder string) bool {
	return strings.HasPrefix(relativePath, folder+"/")
}

// removeEmptyDir drops a directory that nothing is left in. A directory that
// still holds something refuses by itself, which is exactly the check needed.
func removeEmptyDir(directory string) {
	if directory == "" || filepath.Base(directory) == "." {
		return
	}
	_ = os.Remove(directory)
}
