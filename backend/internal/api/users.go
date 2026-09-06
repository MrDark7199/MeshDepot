package api

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"meshdepot/internal/coerce"
	"meshdepot/internal/dateformat"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	"meshdepot/internal/auth"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/quota"
	"meshdepot/internal/storage"
)

// maxCustomCSSBytes bounds the per-account stylesheet. Hand-written themes are
// a few kilobytes; this leaves room for a pasted framework without letting the
// login payload grow without limit.
const maxCustomCSSBytes = 64 * 1024

// UsersSearch searches active users by name/email (max. 20).
//
// An empty query lists the first accounts instead of nothing: the share picker
// opens its list on click, before anything has been typed, and a member who
// does not know who else is on the server has no first letter to guess. The
// caller is left out - sharing with yourself does nothing.
func (server *Server) UsersSearch(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	query := strings.TrimSpace(queryStr(request, "q", ""))
	if query == "" {
		rows, _ := dbutil.QueryMaps(server.DB,
			`SELECT public_id AS id, name, email FROM users
			   WHERE state = 'active' AND id != ? ORDER BY name ASC LIMIT 20`, currentUserID)
		httpx.Success(responseWriter, rows)
		return
	}
	rows, _ := dbutil.QueryMaps(server.DB,
		`SELECT public_id AS id, name, email FROM users WHERE state = 'active' AND id != ?
		   AND (name LIKE ? ESCAPE '\' OR email LIKE ? ESCAPE '\') ORDER BY name ASC LIMIT 20`,
		currentUserID, likePattern(query), likePattern(query))
	httpx.Success(responseWriter, rows)
}

// UsersUpdateProfile changes name/email of the own profile.
func (server *Server) UsersUpdateProfile(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)
	var assignments []string
	var args []any
	if value, present := body["name"]; present {
		name := strings.TrimSpace(coerce.StringOr(value, ""))
		// The column is NOT NULL DEFAULT '', so a cleared name is the empty string.
		// Writing NULL here made the whole update fail with a 500 - including the
		// email submitted alongside it.
		assignments = append(assignments, "name = ?")
		args = append(args, name)
	}
	if value, present := body["email"]; present {
		email := strings.TrimSpace(coerce.StringOr(value, ""))
		if email != "" {
			if _, failure := mail.ParseAddress(email); failure != nil {
				httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_email")
				return
			}
		}
		assignments = append(assignments, "email = ?")
		args = append(args, nullIfEmpty(email))
	}
	if value, present := body["custom_css"]; present {
		css := coerce.StringOr(value, "")
		// A cap rather than a validator: the stylesheet only ever reaches the
		// browser of the account that wrote it, so what is in it is their own
		// business - but it travels in every /auth/me response, and an unbounded
		// column would make that payload unbounded too.
		if len(css) > maxCustomCSSBytes {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.custom_css_too_long")
			return
		}
		assignments = append(assignments, "custom_css = ?")
		args = append(args, css)
	}
	if value, present := body["language"]; present {
		language := strings.TrimSpace(coerce.StringOr(value, ""))
		// The display language belongs to the account, not to the browser: it
		// used to live in localStorage only, so the same account answered in a
		// different language on the next device - and a fresh one started in
		// whatever the last visitor of that browser had picked.
		if language != "en" && language != "de" {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_language")
			return
		}
		assignments = append(assignments, "language = ?")
		args = append(args, language)
	}
	if value, present := body["date_format"]; present {
		format := strings.TrimSpace(coerce.StringOr(value, ""))
		// Checked here because the value reaches the formatter as a pattern: an
		// unknown one would render every date as the pattern itself.
		if !dateformat.Valid(format) {
			httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_date_format")
			return
		}
		assignments = append(assignments, "date_format = ?")
		args = append(args, format)
	}
	if len(assignments) > 0 {
		args = append(args, currentUserID)
		if _, failure := server.DB.Exec("UPDATE users SET "+strings.Join(assignments, ", ")+" WHERE id = ?", args...); failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
			return
		}
	}
	row, ok := server.fetchRow(responseWriter, "SELECT public_id AS id, name, email, language, custom_css, date_format FROM users WHERE id = ? LIMIT 1", currentUserID)
	if !ok {
		return
	}
	httpx.Success(responseWriter, row)
}

// UsersChangePassword changes the password after verifying the old one.
func (server *Server) UsersChangePassword(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	_ = httpx.DecodeJSON(request, &body)
	if body.Current == "" || body.New == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.credentials_required")
		return
	}
	if len(body.New) < 8 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.password_too_short")
		return
	}
	var hash string
	if failure := server.DB.QueryRow("SELECT hash FROM users WHERE id = ?", currentUserID).Scan(&hash); failure != nil {
		httpx.Error(responseWriter, http.StatusUnauthorized, "error.unauthorized")
		return
	}
	if !auth.VerifyPassword(hash, body.Current) {
		// Not 401: the session is perfectly valid, only the supplied password is
		// wrong. A 401 makes the frontend's global handler tear the session down,
		// so a typo logged the user out and looked like the change had gone
		// through.
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.wrong_password")
		return
	}
	server.setPassword(responseWriter, currentUserID, body.New, "Password changed")
}

// UsersForcePassword sets a new password without verifying the old one (for
// must_change_password).
func (server *Server) UsersForcePassword(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	var body struct {
		New string `json:"new_password"`
	}
	_ = httpx.DecodeJSON(request, &body)
	if len(body.New) < 8 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.password_too_short")
		return
	}
	server.setPassword(responseWriter, currentUserID, body.New, "Password set")
}

// setPassword writes a new bcrypt hash and clears must_change_password. All of
// the user's existing sessions are invalidated (so a hijacked session is locked
// out and the session ID rotates against fixation) and a fresh session is issued
// on this response so the user who just changed their own password stays logged
// in on the current device.
func (server *Server) setPassword(responseWriter http.ResponseWriter, currentUserID int, newPassword, message string) {
	hash, failure := auth.HashPassword(newPassword)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if _, failure := server.DB.Exec("UPDATE users SET hash = ?, must_change_password = 0 WHERE id = ?", hash, currentUserID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	server.Auth.Sessions().DeleteAllForUser(currentUserID)
	server.Auth.Sessions().StartForUser(responseWriter, currentUserID, false)
	httpx.SuccessMessage(responseWriter, nil, message)
}

// avatarMime maps detected MIME types to allowed extensions.
var avatarMime = map[string]string{
	"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif",
}

// UsersUploadAvatar stores a profile image (multipart, field "avatar").
func (server *Server) UsersUploadAvatar(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	limitRequestBody(responseWriter, request, maxAvatarUpload)
	if failure := request.ParseMultipartForm(16 << 20); failure != nil {
		uploadError(responseWriter, failure, "error.upload_failed")
		return
	}
	file, _, failure := request.FormFile("avatar")
	if failure != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.upload_failed")
		return
	}
	defer file.Close()
	headBytes := make([]byte, 512)
	readCount, _ := io.ReadFull(file, headBytes)
	mimeType := http.DetectContentType(headBytes[:readCount])
	extension, allowed := avatarMime[mimeType]
	if !allowed {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_image_type")
		return
	}
	if _, failure := file.Seek(0, io.SeekStart); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	directory := server.userLayout(request).Avatar()
	_ = storage.MkdirAll(directory)
	// The numeric id has no business in a path any more; the directory already
	// names the account.
	filename := "avatar_" + randomHex6() + "." + extension
	destination := filepath.Join(directory, filename)

	// Remove the old avatar.
	if oldPath := server.avatarFile("SELECT avatar_path FROM users WHERE id = ?", currentUserID); oldPath != "" {
		_ = os.Remove(oldPath)
	}
	destFile, failure := os.Create(destination)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.upload_failed")
		return
	}
	if _, failure := io.Copy(destFile, file); failure != nil {
		destFile.Close()
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.upload_failed")
		return
	}
	destFile.Close()
	dbutil.ExecLogged(server.DB, "UPDATE users SET avatar_path = ? WHERE id = ?", server.layout().Rel(destination), currentUserID)
	httpx.Success(responseWriter, map[string]any{"avatar_url": "/api/v1/users/" + publicID(request) + "/avatar"})
}

func (server *Server) UsersDeleteAvatar(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	if path := server.avatarFile("SELECT avatar_path FROM users WHERE id = ?", currentUserID); path != "" {
		_ = os.Remove(path)
	}
	dbutil.ExecLogged(server.DB, "UPDATE users SET avatar_path = NULL WHERE id = ?", currentUserID)
	httpx.Success(responseWriter, nil)
}

// UsersServeAvatar streams the profile image (public, no session required).
func (server *Server) UsersServeAvatar(responseWriter http.ResponseWriter, request *http.Request) {
	// Addressed by the public id, so the endpoint cannot be walked by counting.
	path := server.avatarFile("SELECT avatar_path FROM users WHERE public_id = ?", request.PathValue("id"))
	if path == "" {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	responseWriter.Header().Set("Cache-Control", "public, max-age=3600")
	serveImageFile(responseWriter, request, path)
}

// UsersStats returns storage/design statistics.
func (server *Server) UsersStats(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	countScalar := func(query string) int {
		var count int
		_ = server.DB.QueryRow(query, currentUserID).Scan(&count)
		return count
	}
	platformCounts, _ := dbutil.QueryMaps(server.DB,
		`SELECT source_platform, COUNT(*) as cnt FROM designs
		 WHERE user_id = ? AND source_platform IS NOT NULL AND source_platform != ''
		 GROUP BY source_platform ORDER BY cnt DESC`, currentUserID)
	var newest any
	var newestStr string
	if failure := server.DB.QueryRow("SELECT created_at FROM designs WHERE user_id = ? ORDER BY created_at DESC LIMIT 1", currentUserID).Scan(&newestStr); failure == nil {
		newest = newestStr
	}
	httpx.Success(responseWriter, map[string]any{
		"design_count": countScalar("SELECT COUNT(*) FROM designs WHERE user_id = ?"),
		"entry_count": countScalar(`SELECT COUNT(*) FROM design_file_entries dfe
			JOIN design_files df ON df.id = dfe.design_file_id
			JOIN designs d ON d.id = df.design_id WHERE d.user_id = ?`),
		"used_bytes": countScalar(`SELECT COALESCE(SUM(df.size_bytes), 0) FROM design_files df
			JOIN designs d ON d.id = df.design_id WHERE d.user_id = ?`),
		"tag_count":        countScalar("SELECT COUNT(*) FROM tags WHERE user_id = ?"),
		"collection_count": countScalar("SELECT COUNT(*) FROM collections WHERE user_id = ?"),
		"synced_count":     countScalar("SELECT COUNT(*) FROM designs WHERE user_id = ? AND source_url IS NOT NULL AND source_url != ''"),
		"platforms":        platformCounts,
		"newest_design_at": newest,
		// The member's own limit, 0 when they have none. The page already had the
		// arithmetic for a fill level but nothing ever supplied a maximum, so it
		// could never show one.
		"max_bytes": quota.Of(server.DB, currentUserID).LimitBytes,
	})
}

// avatarFile resolves a stored avatar path against the data root. The column
// holds the root-relative form, so nothing may hand it to os.Open directly.
// Returns "" when the query finds nothing or the user has no avatar.
func (server *Server) avatarFile(query string, args ...any) string {
	var stored string
	if failure := server.DB.QueryRow(query, args...).Scan(&stored); failure != nil || stored == "" {
		return ""
	}
	return server.layout().Abs(stored)
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// randomHex6 generates 6 random bytes as hex (12 characters).
func randomHex6() string {
	randomBytes := make([]byte, 6)
	_, _ = rand.Read(randomBytes)
	return hex.EncodeToString(randomBytes)
}
