package api

// Share links hand a design to someone who has no account: whoever holds the
// URL may look at it and download its files, and nothing else.
//
// The token is the whole credential, so the rules that follow from that are
// deliberate rather than incidental:
//
//   - 128 random bits, checked on every request, and the only copy lives in the
//     row. There is no second factor to fall back on.
//   - The public view is assembled by hand, field by field, rather than by
//     handing out the design row. A `SELECT *` would carry the owner's notes
//     and every column added later straight to a stranger.
//   - An expired or deleted link is a 404, like a token that never existed. A
//     separate "expired" answer would confirm that the design is there and
//     invite waiting for the next link.

import (
	"archive/zip"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"meshdepot/internal/coerce"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/publicid"
)

// maxShareLinkHours caps the lifetime at ten years. 0 is a link without an
// expiry - it runs until the owner deletes it. Anything in between is the
// owner's business; the cap only keeps an absurd number out of the column.
const maxShareLinkHours = 24 * 365 * 10

// shareLink is a resolved, still-valid link.
type shareLink struct {
	id       int
	designID int
	ownerID  int
}

// resolveShareLink looks a token up and checks that it may still be used.
// found=false covers every reason equally: unknown, deleted, expired.
func (server *Server) resolveShareLink(token string) (shareLink, bool) {
	if !publicid.Valid(token) {
		return shareLink{}, false
	}
	var link shareLink
	var expiresAt sql.NullString
	failure := server.DB.QueryRow(
		"SELECT id, design_id, user_id, expires_at FROM design_share_links WHERE token = ? LIMIT 1", token).
		Scan(&link.id, &link.designID, &link.ownerID, &expiresAt)
	if errors.Is(failure, sql.ErrNoRows) || failure != nil {
		return shareLink{}, false
	}
	if expiresAt.Valid && expiresAt.String != "" {
		deadline, parseFailure := time.Parse(sqliteTimeLayout, strings.TrimSpace(expiresAt.String))
		if parseFailure != nil || time.Now().UTC().After(deadline) {
			return shareLink{}, false
		}
	}
	return link, true
}

// ShareLinksIndex lists the links of a design, for its owner.
func (server *Server) ShareLinksIndex(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	if !server.ownsDesign(responseWriter, designID, currentUserID) {
		return
	}
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT id, token, expires_at, last_used_at, view_count, created_at,
			CASE WHEN expires_at IS NOT NULL AND expires_at <= datetime('now') THEN 1 ELSE 0 END AS expired
		FROM design_share_links WHERE design_id = ? AND user_id = ? ORDER BY created_at DESC`,
		designID, currentUserID)
	httpx.Success(responseWriter, rows)
}

// UserShareLinksIndex lists every link the member has handed out, across all of
// their designs. The account settings show them in one place: a link that lives
// on a design nobody opens is otherwise easy to forget.
//
// Each row names its design by the public id, so the existing per-design delete
// can revoke it - the account view needs no endpoint of its own for that.
func (server *Server) UserShareLinksIndex(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	// d.public_id is aliased to design_id rather than selected as public_id: the
	// response writer promotes a public_id over "id", which here is the link's.
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT l.id, l.token, l.expires_at, l.last_used_at, l.view_count, l.created_at,
			d.public_id AS design_id, d.name AS design_name,
			CASE WHEN l.expires_at IS NOT NULL AND l.expires_at <= datetime('now') THEN 1 ELSE 0 END AS expired
		FROM design_share_links l JOIN designs d ON d.id = l.design_id
		WHERE l.user_id = ? ORDER BY l.created_at DESC`, currentUserID)
	httpx.Success(responseWriter, rows)
}

// ShareLinksStore creates a link for a design.
func (server *Server) ShareLinksStore(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	if !server.ownsDesign(responseWriter, designID, currentUserID) {
		return
	}
	var body struct {
		ExpiresInHours int `json:"expires_in_hours"`
	}
	_ = httpx.DecodeJSON(request, &body)
	if body.ExpiresInHours < 0 || body.ExpiresInHours > maxShareLinkHours {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_share_expiry")
		return
	}
	var expiresAt any
	if body.ExpiresInHours > 0 {
		expiresAt = time.Now().UTC().Add(time.Duration(body.ExpiresInHours) * time.Hour).Format(sqliteTimeLayout)
	}
	token := publicid.New()
	if _, failure := server.DB.Exec(
		"INSERT INTO design_share_links (design_id, user_id, token, expires_at) VALUES (?, ?, ?, ?)",
		designID, currentUserID, token, expiresAt); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	row, ok := server.fetchRow(responseWriter,
		"SELECT id, token, expires_at, last_used_at, view_count, created_at, 0 AS expired FROM design_share_links WHERE token = ? LIMIT 1", token)
	if !ok {
		return
	}
	httpx.SuccessStatus(responseWriter, http.StatusCreated, row, "Share link created")
}

// ShareLinksDestroy deletes a link, which takes effect immediately.
func (server *Server) ShareLinksDestroy(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	linkID, ok := pathInt(request, "linkId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if !server.ownsDesign(responseWriter, designID, currentUserID) {
		return
	}
	dbutil.ExecLogged(server.DB, "DELETE FROM design_share_links WHERE id = ? AND design_id = ? AND user_id = ?",
		linkID, designID, currentUserID)
	httpx.SuccessMessage(responseWriter, nil, "Share link removed")
}

// PublicShareShow is the read-only view behind a link. No session.
func (server *Server) PublicShareShow(responseWriter http.ResponseWriter, request *http.Request) {
	link, ok := server.resolveShareLink(request.PathValue("token"))
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	design, found, failure := dbutil.QueryMap(server.DB, `
		SELECT name, description, author, source_url, source_platform, cover_path, category, license,
			print_time_minutes, updated_at
		FROM designs WHERE id = ? LIMIT 1`, link.designID)
	if failure != nil || !found {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	// Counted rather than logged: the owner sees that the link is being used
	// without the app keeping a record of who used it.
	dbutil.ExecLogged(server.DB,
		"UPDATE design_share_links SET view_count = view_count + 1, last_used_at = CURRENT_TIMESTAMP WHERE id = ?", link.id)

	tags, _ := dbutil.QueryMaps(server.DB, `
		SELECT t.name, t.color FROM design_tags dt JOIN tags t ON t.id = dt.tag_id
		WHERE dt.design_id = ? ORDER BY t.name ASC`, link.designID)
	images, _ := dbutil.QueryMaps(server.DB,
		"SELECT path, is_cover FROM design_images WHERE design_id = ? ORDER BY sort_order ASC, created_at ASC", link.designID)

	// Only the current version: a link is for handing someone the design as it
	// stands, not its history.
	files := []map[string]any{}
	version, hasVersion, _ := dbutil.QueryMap(server.DB,
		"SELECT id, version FROM design_files WHERE design_id = ? ORDER BY is_current DESC, id DESC LIMIT 1", link.designID)
	versionLabel := ""
	if hasVersion {
		versionLabel = coerce.StringOr(version["version"], "")
		entries, _ := dbutil.QueryMaps(server.DB,
			"SELECT id, filename, size_bytes FROM design_file_entries WHERE design_file_id = ? ORDER BY filename ASC",
			coerce.Int(version["id"]))
		files = entries
	}

	httpx.Success(responseWriter, map[string]any{
		"name":               design["name"],
		"description":        design["description"],
		"author":             design["author"],
		"source_url":         design["source_url"],
		"source_platform":    design["source_platform"],
		"cover_path":         design["cover_path"],
		"category":           design["category"],
		"license":            design["license"],
		"print_time_minutes": design["print_time_minutes"],
		"updated_at":         design["updated_at"],
		"version":            versionLabel,
		"tags":               tags,
		"images":             images,
		"files":              files,
	})
}

// PublicShareDownload streams one file of the shared design.
func (server *Server) PublicShareDownload(responseWriter http.ResponseWriter, request *http.Request) {
	link, ok := server.resolveShareLink(request.PathValue("token"))
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	entryID, ok := pathInt(request, "entryId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	// Restricted to the design the token names. Without the join to design_files
	// a valid token would be a key to every file on the server.
	entry, found, failure := scanFileEntry(server.DB.QueryRow("SELECT "+fileEntryColumns+`
		FROM design_file_entries dfe
		JOIN design_files df ON df.id = dfe.design_file_id
		WHERE dfe.id = ? AND df.design_id = ? LIMIT 1`, entryID, link.designID))
	if failure != nil || !found {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	entryPath := entry.absPath(server.layout())
	if !fileExists(entryPath) {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	serveAttachment(responseWriter, entryPath, entryFilename(entry))
}

// PublicShareDownloadAll streams the whole current version as one ZIP, which is
// what someone handed a link usually wants - the alternative is clicking every
// file. A version with a single file is served as that file.
func (server *Server) PublicShareDownloadAll(responseWriter http.ResponseWriter, request *http.Request) {
	link, ok := server.resolveShareLink(request.PathValue("token"))
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	version, hasVersion, failure := dbutil.QueryMap(server.DB,
		"SELECT id, COALESCE(version, '') AS version FROM design_files WHERE design_id = ? ORDER BY is_current DESC, id DESC LIMIT 1",
		link.designID)
	if failure != nil || !hasVersion {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	entries, failure := versionEntries(server.DB, coerce.Int(version["id"]))
	if failure != nil || len(entries) == 0 {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	layout := server.layout()
	if len(entries) == 1 {
		if single := entries[0].absPath(layout); fileExists(single) {
			serveAttachment(responseWriter, single, entryFilename(entries[0]))
			return
		}
	}
	versionLabel := coerce.StringOr(version["version"], "")
	if versionLabel == "" {
		versionLabel = "1"
	}
	responseWriter.Header().Set("Content-Type", "application/zip")
	responseWriter.Header().Set("Content-Disposition", `attachment; filename="design_v`+versionLabel+`.zip"`)
	zipWriter := zip.NewWriter(responseWriter)
	defer zipWriter.Close()
	for _, entry := range entries {
		entryPath := entry.absPath(layout)
		if !fileExists(entryPath) {
			continue
		}
		// relative_path, like the authenticated download: the bare filename would
		// collapse parts/left/body.stl and parts/right/body.stl into one name.
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
