package api

// Import of a design collected by the browser extension.
//
// Deliberately outside the download queue: the links are presigned and expire
// about five minutes after MakerWorld issues them, so a job waiting behind a
// backlog would arrive to find them dead. The import runs during the request and
// answers with what happened, which is what the extension needs while the visitor
// is still on the page.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"meshdepot/internal/coerce"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/notify"
	"meshdepot/internal/platforms"
	"meshdepot/internal/quota"
	"meshdepot/internal/translate"

	"meshdepot/internal/logx"
)

// BrowserImportAPIVersion is the contract this server speaks with the extension,
// BrowserImportAPIMinVersion the oldest it still accepts. The two halves update
// separately, so without a version to compare a mismatch shows up as an import
// that fails for no stated reason.
//
// Raise the version when the payload gains something an older server would
// ignore; raise the minimum only when an older extension cannot be served.
const (
	BrowserImportAPIVersion    = 1
	BrowserImportAPIMinVersion = 1
)

// maxBrowserImportFiles keeps a malformed or hostile request from turning into
// an unbounded series of downloads.
const maxBrowserImportFiles = 40

// BrowserImportStatus tells the extension whether it can reach this instance and
// whether its key is good, in one call. The public health endpoint answers only
// the first, and "connected" on the strength of a reachable host is misleading.
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
	// AddToCollection files the design under a collection of the extension's own.
	AddToCollection bool `json:"add_to_collection"`
	Files           []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"files"`
}

// BrowserImport takes a design the extension collected and stores it.
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
	// Derived from the URL rather than believed: the body says what it likes, and
	// this decides where the design is filed and which sync later touches it.
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

	// Before anything is fetched, as the download queue does it. A quota that
	// could not be read is not a quota of zero: refuse for now rather than let
	// the import past a limit nobody could check.
	if usage := quota.Of(server.DB, currentUserID); usage.Unknown {
		httpx.Error(responseWriter, http.StatusServiceUnavailable, "error.storage_check_failed")
		return
	} else if usage.Exceeded() {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.storage_quota_exceeded")
		return
	}

	duplicate, isDuplicate, failure := dbutil.QueryMap(server.DB,
		"SELECT id, name FROM designs WHERE user_id = ? AND source_url = ? LIMIT 1", currentUserID, sourceURL)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	// A model already in the library does not make this a duplicate any more: the
	// files are offered to its newest version, which refuses a name that version
	// already holds. A model not there yet takes the unchanged path below.
	attachTo := 0
	if isDuplicate {
		attachTo = coerce.Int(duplicate["id"])
	}

	// Asking for this design by hand outranks an earlier "delete and keep it gone".
	platforms.LiftSyncExclusion(server.DB, currentUserID, platform, "", sourceURL)

	importRequest := browserImportRequestFrom(body, platform, sourceURL)
	importRequest.AttachToDesignID = attachTo
	for _, file := range body.Files {
		importRequest.Files = append(importRequest.Files, platforms.BrowserImportFile{
			Name: file.Name, URL: file.URL,
		})
	}

	outcome, failure := platforms.ImportFromBrowser(server.DB, server.owner(request), importRequest)
	if failure != nil {
		// The download URLs are left out on purpose: for their five minutes they are a
		// complete substitute for the visitor's session, and a log outlives them.
		logx.Errorf("[browser-import] user %d, %s: %v (%d link(s) unusable)",
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
		// How many links were dead by the time the server tried them. The import still
		// counts as done, but the extension should be able to say so.
		"skipped_count": len(outcome.SkippedURL),
		"platform":      platform,
		// So the extension can say where the design went.
		"collection": outcome.Collection,
	}, "Design imported")
}

// BrowserImportUpload takes a design whose files the extension carries itself,
// for platforms that hand out no link the server can follow: Thingiverse
// assembles its archive in the browser, and a blob: address means nothing outside
// it. "payload" carries the same JSON as the link route, every "file" part is one
// file. API key only, for the same reason as its sibling.
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

	if usage := quota.Of(server.DB, currentUserID); usage.Unknown {
		httpx.Error(responseWriter, http.StatusServiceUnavailable, "error.storage_check_failed")
		return
	} else if usage.Exceeded() {
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
	// Same rule as the link import above: an existing design takes the files into
	// its newest version rather than refusing them.
	uploadAttachTo := 0
	if isDuplicate {
		uploadAttachTo = coerce.Int(duplicate["id"])
	}
	platforms.LiftSyncExclusion(server.DB, currentUserID, platform, "", sourceURL)

	uploads := make([]platforms.BrowserUpload, 0, len(headers))
	for _, header := range headers {
		fileHeader := header
		uploads = append(uploads, platforms.BrowserUpload{
			Name: fileHeader.Filename,
			// Opened when the file is written, so nothing is held open meanwhile.
			Open: func() (io.ReadCloser, error) { return fileHeader.Open() },
		})
	}

	uploadRequest := browserImportRequestFrom(body, platform, sourceURL)
	uploadRequest.AttachToDesignID = uploadAttachTo
	outcome, failure := platforms.ImportUploadsFromBrowser(server.DB, server.owner(request), uploadRequest, uploads)
	if failure != nil {
		logx.Errorf("[browser-import] upload for user %d, %s: %v", currentUserID, sourceURL, failure)
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

// browserImportRequestFrom is shared by both routes, which differ only in where
// the files come from.
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

// afterBrowserImport does what the download queue does once a design is stored:
// the texts go through the translator when that is switched on - a design
// imported in Spanish stayed Spanish - and the storage warning fires on the
// crossing. Neither may fail the import, which has already succeeded.
func (server *Server) afterBrowserImport(userID int, outcome platforms.BrowserImportOutcome) {
	var description *string
	if strings.TrimSpace(outcome.Description) != "" {
		text := outcome.Description
		description = &text
	}
	translate.New(server.DB).ApplyToDesign(outcome.DesignID, outcome.Name, description, true)
	notify.StorageNearlyFull(server.DB, userID)
}
