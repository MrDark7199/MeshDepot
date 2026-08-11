package api

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/storage"
)

// imagePathPattern is the allowlist of paths this endpoint may serve, and it
// captures the two identities in them: the owner's public id and the design's
// row id. The image root is the data root, so the structural check is what keeps
// the endpoint from handing out the database, a blob or an account's avatar -
// only a design's gallery file or its cover qualifies, and both are images by
// construction.
//
// The shape alone is not authorization, though. A cover is stored under the
// fixed name cover.<ext>, and the design segment is the sequential row id, so
// the path of somebody else's cover can simply be counted to - and the owner's
// public id is not a secret either: every share payload contains it, and the
// user search hands it to any member. Who may see the image is therefore
// decided below, not by the pattern.
var imagePathPattern = regexp.MustCompile(`^user/([0-9a-f]{32})/design/(\d+)/(?:pictures/[^/]+|cover\.[A-Za-z0-9]{1,5})$`)

// CoversServe serves a cover/gallery image of a design to someone who may see
// the design: its owner, a user it was shared with, or the holder of a share
// link for it (?share=<token>, which is how the public share page loads its
// pictures - it has no session by definition).
func (server *Server) CoversServe(responseWriter http.ResponseWriter, request *http.Request) {
	relativePath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+request.PathValue("path"))), "/")
	match := imagePathPattern.FindStringSubmatch(relativePath)
	if match == nil {
		http.NotFound(responseWriter, request)
		return
	}
	designID, failure := strconv.Atoi(match[2])
	if failure != nil || !server.mayViewDesignImage(request, match[1], designID) {
		http.NotFound(responseWriter, request)
		return
	}
	layout := server.layout()
	fullPath := layout.Abs(relativePath)
	if !layout.Contains(fullPath) {
		http.NotFound(responseWriter, request)
		return
	}
	// private, not public: the answer now depends on who asked, so a shared cache
	// in front of the app must not hand one viewer's response to the next.
	responseWriter.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	serveImageFile(responseWriter, request, fullPath)
}

// mayViewDesignImage decides whether the request may see the pictures of a
// design. Every answer is the same 404 at the call site: telling a stranger
// "403" would confirm that the design exists, which is exactly what counting
// through the row ids is after.
func (server *Server) mayViewDesignImage(request *http.Request, ownerPublicID string, designID int) bool {
	// The path has to name the design's real owner. Without this the owner
	// segment would be decoration: any valid public id would do, and the design
	// id alone would decide which file is read.
	var designOwnerID int
	var designOwnerPublicID string
	if failure := server.DB.QueryRow(
		`SELECT d.user_id, COALESCE(u.public_id, '') FROM designs d
		   JOIN users u ON u.id = d.user_id WHERE d.id = ? LIMIT 1`, designID).
		Scan(&designOwnerID, &designOwnerPublicID); failure != nil {
		return false
	}
	if designOwnerPublicID != ownerPublicID {
		return false
	}

	// A share link is checked before the session: the public page carries no
	// cookie, and a member who opens someone's link should not need one either.
	if token := strings.TrimSpace(request.URL.Query().Get("share")); token != "" {
		if link, ok := server.resolveShareLink(token); ok && link.designID == designID {
			return true
		}
	}

	viewerID, ok := server.Auth.Sessions().UserID(request)
	if !ok {
		return false
	}
	if viewerID == designOwnerID {
		// Only for an account that still exists and is active - the same
		// re-check the Require middleware does on every other route.
		var state string
		if failure := server.DB.QueryRow("SELECT state FROM users WHERE id = ?", viewerID).Scan(&state); failure != nil {
			return false
		}
		return state == "active"
	}
	var shared int
	server.DB.QueryRow(`SELECT COUNT(*) FROM design_shares ds
		  JOIN users u ON u.id = ds.shared_with_user_id
		 WHERE ds.design_id = ? AND ds.shared_with_user_id = ? AND u.state = 'active'`,
		designID, viewerID).Scan(&shared)
	return shared > 0
}

// ImagesUpload stores one or more gallery images (multipart "image"). Invalid
// files are silently skipped.
func (server *Server) ImagesUpload(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	design, ok := server.requireOwnership(responseWriter, designID, currentUserID)
	if !ok {
		return
	}
	limitRequestBody(responseWriter, request, maxImageUpload)
	if failure := request.ParseMultipartForm(64 << 20); failure != nil {
		uploadError(responseWriter, failure, "error.file_required")
		return
	}
	if request.MultipartForm == nil || len(request.MultipartForm.File["image"]) == 0 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.file_required")
		return
	}
	layout := server.userLayout(request)
	directory := layout.Pictures(designID)
	_ = storage.MkdirAll(directory)

	saved := 0
	firstImageID := int64(0)
	firstImagePath := ""
	for _, fileHeader := range request.MultipartForm.File["image"] {
		sourceFile, failure := fileHeader.Open()
		if failure != nil {
			continue
		}
		headBytes := make([]byte, 512)
		readCount, _ := io.ReadFull(sourceFile, headBytes)
		extension, allowed := avatarMime[http.DetectContentType(headBytes[:readCount])]
		if !allowed {
			sourceFile.Close()
			continue
		}
		if _, failure := sourceFile.Seek(0, io.SeekStart); failure != nil {
			sourceFile.Close()
			continue
		}
		filename := uniqID("img_") + "." + extension
		destFile, failure := os.Create(filepath.Join(directory, filename))
		if failure != nil {
			sourceFile.Close()
			continue
		}
		_, copyFailure := io.Copy(destFile, sourceFile)
		destFile.Close()
		sourceFile.Close()
		if copyFailure != nil {
			continue
		}
		var sortOrder int
		_ = server.DB.QueryRow("SELECT COALESCE(MAX(sort_order),0)+1 FROM design_images WHERE design_id = ?", designID).Scan(&sortOrder)
		relativePath := layout.Rel(filepath.Join(directory, filename))
		insertResult, failure := server.DB.Exec("INSERT INTO design_images (design_id, path, sort_order) VALUES (?, ?, ?)", designID, relativePath, sortOrder)
		if failure != nil {
			continue
		}
		if saved == 0 {
			firstImageID, _ = insertResult.LastInsertId()
			firstImagePath = relativePath
		}
		saved++
	}
	if saved == 0 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.upload_failed")
		return
	}

	// Auto cover: if the design had no cover image yet (e.g. manually created), the
	// first uploaded image automatically becomes the cover - so the placeholder in
	// the card overview disappears without an extra click.
	coverPath := design.StoredCoverPath
	if coverPath == "" && firstImagePath != "" {
		if _, failure := server.DB.Exec("UPDATE designs SET cover_path = ? WHERE id = ?", firstImagePath, designID); failure == nil {
			dbutil.ExecLogged(server.DB, "UPDATE design_images SET is_cover = 1 WHERE id = ?", firstImageID)
			coverPath = firstImagePath
		}
	}
	httpx.Success(responseWriter, map[string]any{"uploaded": saved, "cover_path": coverPath})
}

// ImagesDelete deletes a gallery image from DB and filesystem.
func (server *Server) ImagesDelete(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	imageID, ok := pathInt(request, "imageId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireOwnership(responseWriter, designID, currentUserID); !ok {
		return
	}
	image, ok := server.requireDesignImage(responseWriter, imageID, designID)
	if !ok {
		return
	}

	// Deleting the row and reassigning the cover belong together: if the delete
	// went through and the reassignment did not, the design kept pointing at a
	// cover_path whose file is gone - a permanently broken thumbnail.
	transaction, failure := server.DB.Begin()
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	defer transaction.Rollback()

	current, found, failure := loadDesign(transaction, designID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	wasCover := found && current.StoredCoverPath == image.StoredPath

	writeFailed := false
	exec := func(query string, args ...any) {
		if _, failure := transaction.Exec(query, args...); failure != nil {
			log.Printf("[api] images delete: %v", failure)
			writeFailed = true
		}
	}
	exec("DELETE FROM design_images WHERE id = ?", imageID)
	if wasCover {
		// New cover = first remaining image (or none → placeholder).
		exec("UPDATE designs SET cover_path = (SELECT path FROM design_images WHERE design_id = ? ORDER BY sort_order ASC, created_at ASC LIMIT 1) WHERE id = ?", designID, designID)
		exec("UPDATE design_images SET is_cover = (id = (SELECT id FROM design_images WHERE design_id = ? ORDER BY sort_order ASC, created_at ASC LIMIT 1)) WHERE design_id = ?", designID, designID)
	}
	if writeFailed {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if failure := transaction.Commit(); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}

	// Only after the commit: a file deleted first would be missing while the row
	// still referenced it if the transaction failed.
	_ = os.Remove(image.absPath(server.layout()))
	httpx.Success(responseWriter, nil)
}

// ImagesSetCover makes an existing gallery image the cover image of the design:
// sets designs.cover_path and marks exactly this image as is_cover.
func (server *Server) ImagesSetCover(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	imageID, ok := pathInt(request, "imageId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireOwnership(responseWriter, designID, currentUserID); !ok {
		return
	}
	image, ok := server.requireDesignImage(responseWriter, imageID, designID)
	if !ok {
		return
	}
	coverPath := image.StoredPath
	if _, failure := server.DB.Exec("UPDATE designs SET cover_path = ? WHERE id = ?", coverPath, designID); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	dbutil.ExecLogged(server.DB, "UPDATE design_images SET is_cover = (id = ?) WHERE design_id = ?", imageID, designID)
	httpx.Success(responseWriter, map[string]any{"cover_path": coverPath})
}

func uniqID(prefix string) string {
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 16) + randomHex6()
}
