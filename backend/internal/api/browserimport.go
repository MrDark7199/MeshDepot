package api

// Import of a design collected by the browser extension.
//
// Deliberately outside the download queue. The links the extension hands over
// are presigned and expire about five minutes after MakerWorld issues them, so a
// job waiting behind a backlog would arrive to find them dead. The import runs
// during the request and answers with what happened - which is also what the
// extension needs in order to say "done" or "failed" while the visitor is still
// looking at the page.

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/notify"
	"meshdepot/internal/platforms"
	"meshdepot/internal/quota"
	"meshdepot/internal/translate"
)

// BrowserImportAPIVersion is the contract this server speaks with the browser
// extension, and BrowserImportAPIMinVersion the oldest it still accepts.
//
// They exist because the two halves are updated separately: the extension lives
// in someone's browser and updates itself, the server is self-hosted and gets
// updated when its operator gets round to it. Without a version to compare, a
// mismatch shows up as an import that fails for no stated reason.
//
// Raise BrowserImportAPIVersion when the payload gains something an older server
// would ignore; raise the minimum only when an older extension can genuinely no
// longer be served.
const (
	BrowserImportAPIVersion    = 1
	BrowserImportAPIMinVersion = 1
)

// maxBrowserImportFiles bounds one import. A model with more plates than this
// does not exist in practice, and the limit keeps a malformed or hostile request
// from turning into an unbounded series of downloads.
const maxBrowserImportFiles = 40

// BrowserImportStatus tells the extension whether it can reach this instance and
// whether its key is good, in one call.
//
// The public health endpoint answers the first question but not the second, and
// a client that shows "connected" on the strength of a reachable host is
// misleading: the import would still fail on a revoked key. Reaching this at all
// means the key passed the middleware.
func (server *Server) BrowserImportStatus(responseWriter http.ResponseWriter, request *http.Request) {
	var name string
	server.DB.QueryRow("SELECT name FROM users WHERE id = ?", userID(request)).Scan(&name)
	httpx.Success(responseWriter, map[string]any{
		"ok":          true,
		"user":        name,
		"version":     BrowserImportAPIVersion,
		"min_version": BrowserImportAPIMinVersion,
	})
}

type browserImportBody struct {
	SourceURL string `json:"source_url"`
	Platform  string `json:"platform"`
	Meta      struct {
		SourceID    string   `json:"source_id"`
		Name        string   `json:"name"`
		Author      string   `json:"author"`
		Description string   `json:"description"`
		License     string   `json:"license"`
		CoverURL    string   `json:"cover_url"`
		Images      []string `json:"images"`
		Tags        []string `json:"tags"`
	} `json:"meta"`
	// AddToCollection files the design under a collection of the extension's own,
	// for members who want their browser imports kept together.
	AddToCollection bool `json:"add_to_collection"`
	Files           []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"files"`
}

// BrowserImport takes a design the extension collected and stores it.
//
// Authenticated by API key only (see auth.RequireAPIKey): the route downloads
// files from URLs in the request body, and one reachable with an ambient session
// cookie could be triggered by any page the member happens to visit.
func (server *Server) BrowserImport(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)

	var body browserImportBody
	if httpx.DecodeJSON(request, &body) != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}

	sourceURL := strings.TrimSpace(body.SourceURL)
	if sourceURL == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.url_required")
		return
	}
	// The platform is derived from the URL rather than believed: the body says
	// what it likes, and this decides where the design is filed and which sync
	// later touches it.
	platform := detectPlatform(sourceURL)
	if platform == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.unsupported_platform")
		return
	}
	if len(body.Files) == 0 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.no_files")
		return
	}
	if len(body.Files) > maxBrowserImportFiles {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.too_many_files")
		return
	}

	// Checked before anything is fetched, exactly as the download queue does: a
	// design that cannot be kept should not spend minutes being downloaded first.
	if usage := quota.Of(server.DB, currentUserID); usage.Exceeded() {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.storage_quota_exceeded")
		return
	}

	duplicate, isDuplicate, failure := dbutil.QueryMap(server.DB,
		"SELECT id, name FROM designs WHERE user_id = ? AND source_url = ? LIMIT 1", currentUserID, sourceURL)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if isDuplicate {
		httpx.Error(responseWriter, http.StatusConflict, "error.duplicate_design:"+coerceName(duplicate["name"]))
		return
	}

	// Asking for this design by hand outranks an earlier "delete and keep it
	// gone", exactly as it does for a queued download.
	platforms.LiftSyncExclusion(server.DB, currentUserID, platform, "", sourceURL)

	importRequest := browserImportRequestFrom(body, platform, sourceURL)
	for _, file := range body.Files {
		importRequest.Files = append(importRequest.Files, platforms.BrowserImportFile{
			Name: file.Name, URL: file.URL,
		})
	}

	outcome, failure := platforms.ImportFromBrowser(server.DB, server.owner(request), importRequest)
	if failure != nil {
		// The download URLs are left out of the log on purpose: for their five
		// minutes they are a complete substitute for the visitor's session on
		// that platform, and a log file outlives them by rather longer.
		log.Printf("[browser-import] user %d, %s: %v (%d link(s) unusable)",
			currentUserID, sourceURL, failure, len(outcome.SkippedURL))
		key := failure.Error()
		if !strings.HasPrefix(key, "error.") {
			key = "error.browser_import_failed"
		}
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, key)
		return
	}

	server.afterBrowserImport(currentUserID, outcome)

	httpx.SuccessStatus(responseWriter, http.StatusCreated, map[string]any{
		"design_id":  outcome.DesignID,
		"file_count": outcome.FileCount,
		// How many links were dead by the time the server tried them. The import
		// still counts as done - four of five plates beats none - but the
		// extension should be able to say so.
		"skipped_count": len(outcome.SkippedURL),
		"platform":      platform,
		// Named back so the extension can say where the design went, and stay
		// quiet when the filing did not happen.
		"collection": outcome.Collection,
	}, "Design imported")
}

// coerceName reads a design name out of a row, with a fallback for the message.
func coerceName(value any) string {
	if text, ok := value.(string); ok && text != "" {
		return text
	}
	return "Unknown"
}

// BrowserImportUpload takes a design whose files the extension carries itself.
//
// The sibling of BrowserImport, and it exists because not every platform hands
// out a link the server can follow. Thingiverse assembles its archive in the
// browser - that is the countdown before the download starts - and the result is
// a blob: address that means nothing outside that browser. There is nothing to
// fetch, so the bytes come with the request.
//
// multipart/form-data: "payload" carries the same JSON as the link route, and
// every "file" part is one file. Authenticated by API key only, for the same
// reason as its sibling.
func (server *Server) BrowserImportUpload(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)

	limitRequestBody(responseWriter, request, maxModelUpload)
	if failure := request.ParseMultipartForm(64 << 20); failure != nil {
		uploadError(responseWriter, failure, "error.file_required")
		return
	}

	var body browserImportBody
	if json.Unmarshal([]byte(request.FormValue("payload")), &body) != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	sourceURL := strings.TrimSpace(body.SourceURL)
	platform := detectPlatform(sourceURL)
	if sourceURL == "" || platform == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.unsupported_platform")
		return
	}

	if usage := quota.Of(server.DB, currentUserID); usage.Exceeded() {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.storage_quota_exceeded")
		return
	}

	headers := request.MultipartForm.File["file"]
	if len(headers) == 0 {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.no_files")
		return
	}
	if len(headers) > maxBrowserImportFiles {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.too_many_files")
		return
	}

	duplicate, isDuplicate, failure := dbutil.QueryMap(server.DB,
		"SELECT id, name FROM designs WHERE user_id = ? AND source_url = ? LIMIT 1", currentUserID, sourceURL)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if isDuplicate {
		httpx.Error(responseWriter, http.StatusConflict, "error.duplicate_design:"+coerceName(duplicate["name"]))
		return
	}
	platforms.LiftSyncExclusion(server.DB, currentUserID, platform, "", sourceURL)

	uploads := make([]platforms.BrowserUpload, 0, len(headers))
	for _, header := range headers {
		fileHeader := header
		uploads = append(uploads, platforms.BrowserUpload{
			Name: fileHeader.Filename,
			// Opened when the file is actually written, so nothing is held open
			// while the others are processed.
			Open: func() (io.ReadCloser, error) { return fileHeader.Open() },
		})
	}

	outcome, failure := platforms.ImportUploadsFromBrowser(
		server.DB, server.owner(request), browserImportRequestFrom(body, platform, sourceURL), uploads)
	if failure != nil {
		log.Printf("[browser-import] upload for user %d, %s: %v", currentUserID, sourceURL, failure)
		key := failure.Error()
		if !strings.HasPrefix(key, "error.") {
			key = "error.browser_import_failed"
		}
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, key)
		return
	}

	server.afterBrowserImport(currentUserID, outcome)

	httpx.SuccessStatus(responseWriter, http.StatusCreated, map[string]any{
		"design_id":     outcome.DesignID,
		"file_count":    outcome.FileCount,
		"skipped_count": len(outcome.SkippedURL),
		"platform":      platform,
		"collection":    outcome.Collection,
	}, "Design imported")
}

// browserImportRequestFrom builds the platform-layer request from a decoded
// body. Shared by both routes, which differ only in where the files come from.
func browserImportRequestFrom(body browserImportBody, platform, sourceURL string) platforms.BrowserImportRequest {
	request := platforms.BrowserImportRequest{
		SourceURL:   sourceURL,
		Platform:    platform,
		SourceID:    strings.TrimSpace(body.Meta.SourceID),
		Name:        strings.TrimSpace(body.Meta.Name),
		Author:      strings.TrimSpace(body.Meta.Author),
		Description: strings.TrimSpace(body.Meta.Description),
		License:     strings.TrimSpace(body.Meta.License),
		CoverURL:    strings.TrimSpace(body.Meta.CoverURL),
		ImageURLs:   body.Meta.Images,
		Tags:        body.Meta.Tags,

		AddToCollection: body.AddToCollection,
	}
	if request.SourceID == "" {
		request.SourceID = extractSourceID(platform, sourceURL)
	}
	if request.Name == "" {
		request.Name = "Untitled"
	}
	return request
}

// afterBrowserImport does what the download queue does once a design is stored.
//
// Both were missing here, and both are things a member has already asked for
// elsewhere: the texts go through the translator when that is switched on - a
// design imported in Spanish stayed Spanish, while the same design through the
// queue arrived in English - and the storage warning fires on the crossing.
//
// Neither may fail the import. The design is in the library by this point, and
// reporting an error over a translation would be a lie about what happened.
func (server *Server) afterBrowserImport(userID int, outcome platforms.BrowserImportOutcome) {
	var description *string
	if strings.TrimSpace(outcome.Description) != "" {
		text := outcome.Description
		description = &text
	}
	translate.New(server.DB).ApplyToDesign(outcome.DesignID, outcome.Name, description, true)
	notify.StorageNearlyFull(server.DB, userID)
}
