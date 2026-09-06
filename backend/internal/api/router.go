package api

import (
	"net/http"
	"strings"

	"meshdepot/internal/httpx"
	"meshdepot/internal/webui"
)

// Router builds the complete HTTP handler (routes + CORS).
func (server *Server) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", func(responseWriter http.ResponseWriter, _ *http.Request) {
		httpx.Success(responseWriter, map[string]string{"status": "ok"})
	})

	// ── Auth (public; session protection happens in the handler) ──
	mux.HandleFunc("POST /api/v1/auth/login", server.Auth.Login)
	mux.HandleFunc("POST /api/v1/auth/totp/verify", server.Auth.TotpVerify)
	mux.HandleFunc("POST /api/v1/auth/totp/setup", server.Auth.TotpSetup)
	mux.HandleFunc("POST /api/v1/auth/totp/enable", server.Auth.TotpEnable)
	mux.HandleFunc("POST /api/v1/auth/totp/disable", server.Auth.TotpDisable)
	mux.HandleFunc("POST /api/v1/auth/logout", server.Auth.Logout)
	mux.HandleFunc("GET /api/v1/auth/me", server.Auth.Me)

	// ── Tags (session-protected) ──
	mux.Handle("GET /api/v1/tags", server.Auth.Require(http.HandlerFunc(server.TagsIndex)))
	mux.Handle("GET /api/v1/tags/search", server.Auth.Require(http.HandlerFunc(server.TagsSearch)))
	mux.Handle("POST /api/v1/tags", server.Auth.Require(http.HandlerFunc(server.TagsStore)))
	mux.Handle("DELETE /api/v1/tags/{id}", server.Auth.Require(http.HandlerFunc(server.TagsDestroy)))
	mux.Handle("PUT /api/v1/designs/{designId}/tags", server.Auth.Require(http.HandlerFunc(server.TagsSetForDesign)))

	// ── Designs (session-protected) ──
	mux.Handle("GET /api/v1/designs/check-url", server.Auth.Require(http.HandlerFunc(server.DesignsCheckUrl)))
	mux.Handle("GET /api/v1/designs", server.Auth.Require(http.HandlerFunc(server.DesignsIndex)))
	mux.Handle("POST /api/v1/designs", server.Auth.Require(http.HandlerFunc(server.DesignsStore)))
	mux.Handle("GET /api/v1/designs/{id}", server.Auth.Require(http.HandlerFunc(server.DesignsShow)))
	mux.Handle("PUT /api/v1/designs/{id}", server.Auth.Require(http.HandlerFunc(server.DesignsUpdate)))
	mux.Handle("DELETE /api/v1/designs/{id}", server.Auth.Require(http.HandlerFunc(server.DesignsDestroy)))
	mux.Handle("POST /api/v1/designs/{id}/fetch-cover", server.Auth.Require(http.HandlerFunc(server.DesignsFetchCover)))
	mux.Handle("POST /api/v1/designs/{id}/sync", server.Auth.Require(http.HandlerFunc(server.DesignsSync)))
	mux.Handle("GET /api/v1/designs/{id}/duplicates", server.Auth.Require(http.HandlerFunc(server.DesignsDuplicates)))
	mux.Handle("GET /api/v1/designs/{id}/collections", server.Auth.Require(http.HandlerFunc(server.DesignCollections)))

	// ── Collections (session-protected) ──
	mux.Handle("GET /api/v1/collections", server.Auth.Require(http.HandlerFunc(server.CollectionsIndex)))
	mux.Handle("POST /api/v1/collections", server.Auth.Require(http.HandlerFunc(server.CollectionsStore)))
	mux.Handle("PUT /api/v1/collections/{id}", server.Auth.Require(http.HandlerFunc(server.CollectionsUpdate)))
	mux.Handle("DELETE /api/v1/collections/{id}", server.Auth.Require(http.HandlerFunc(server.CollectionsDestroy)))
	mux.Handle("GET /api/v1/collections/{id}/addable-designs", server.Auth.Require(http.HandlerFunc(server.CollectionAddableDesigns)))
	mux.Handle("GET /api/v1/collections/{id}/designs", server.Auth.Require(http.HandlerFunc(server.CollectionDesigns)))
	mux.Handle("POST /api/v1/collections/{id}/designs", server.Auth.Require(http.HandlerFunc(server.CollectionAddDesigns)))
	mux.Handle("DELETE /api/v1/collections/{id}/designs/{designId}", server.Auth.Require(http.HandlerFunc(server.CollectionRemoveDesign)))

	// ── Download queue + sync (session-protected) ──
	mux.Handle("POST /api/v1/download", server.Auth.Require(http.HandlerFunc(server.DownloadQueue)))
	mux.Handle("GET /api/v1/download/queue", server.Auth.Require(http.HandlerFunc(server.DownloadList)))
	mux.Handle("GET /api/v1/download/{id}", server.Auth.Require(http.HandlerFunc(server.DownloadStatus)))
	mux.Handle("POST /api/v1/download/{id}/retry", server.Auth.Require(http.HandlerFunc(server.DownloadRetry)))
	mux.Handle("DELETE /api/v1/download/{id}", server.Auth.Require(http.HandlerFunc(server.DownloadCancel)))
	mux.Handle("DELETE /api/v1/download/{id}/dismiss", server.Auth.Require(http.HandlerFunc(server.DownloadDismiss)))
	mux.Handle("GET /api/v1/queue/blocks", server.Auth.Require(http.HandlerFunc(server.QueueBlocks)))
	mux.Handle("POST /api/v1/designs/sync-all", server.Auth.Require(http.HandlerFunc(server.SyncAll)))
	mux.Handle("GET /api/v1/designs/sync-status", server.Auth.Require(http.HandlerFunc(server.SyncStatus)))

	// ── Design files (session-protected; token serve is public) ──
	mux.Handle("GET /api/v1/designs/{designId}/files", server.Auth.Require(http.HandlerFunc(server.FilesIndex)))
	mux.Handle("POST /api/v1/designs/{designId}/files", server.Auth.Require(http.HandlerFunc(server.FilesStore)))
	mux.Handle("POST /api/v1/designs/{designId}/files/{fileId}/entries", server.Auth.Require(http.HandlerFunc(server.FilesAddEntries)))
	mux.Handle("DELETE /api/v1/designs/{designId}/files/{fileId}", server.Auth.Require(http.HandlerFunc(server.FilesDestroy)))
	mux.Handle("GET /api/v1/designs/{designId}/files/{fileId}/download", server.Auth.Require(http.HandlerFunc(server.FilesDownload)))
	mux.Handle("GET /api/v1/designs/{designId}/files/{fileId}/entry/{entryId}", server.Auth.Require(http.HandlerFunc(server.FilesServeEntry)))
	mux.Handle("DELETE /api/v1/designs/{designId}/files/{fileId}/entry/{entryId}", server.Auth.Require(http.HandlerFunc(server.FilesDeleteEntry)))
	mux.Handle("POST /api/v1/designs/{designId}/files/{fileId}/entry/{entryId}/token", server.Auth.Require(http.HandlerFunc(server.FilesCreateToken)))
	mux.Handle("GET /api/v1/designs/{designId}/files/{fileId}/stl", server.Auth.Require(http.HandlerFunc(server.FilesServeStl)))
	mux.Handle("GET /api/v1/designs/{designId}/files/{fileId}/entry/{entryId}/pwmx/mesh", server.Auth.Require(http.HandlerFunc(server.FilesServePwmxMesh)))
	mux.HandleFunc("GET /api/v1/files/token/{token}", server.FilesServeByToken) // public (slicer)

	// ── Design images (session-protected) ──
	mux.Handle("POST /api/v1/designs/{designId}/images", server.Auth.Require(http.HandlerFunc(server.ImagesUpload)))
	mux.Handle("PUT /api/v1/designs/{designId}/images/{imageId}/cover", server.Auth.Require(http.HandlerFunc(server.ImagesSetCover)))
	mux.Handle("DELETE /api/v1/designs/{designId}/images/{imageId}", server.Auth.Require(http.HandlerFunc(server.ImagesDelete)))

	// Serve cover/gallery images (public like avatars; <img> sends no auth header).
	// Relative path "{userId}/stl/{designId}/pictures/{file}" under BASE_PATH_DATA.
	mux.HandleFunc("GET /api/v1/covers/{path...}", server.CoversServe)

	// ── Public share links (no session; the token is the whole credential) ──
	mux.HandleFunc("GET /api/v1/public/share/{token}", server.PublicShareShow)
	mux.HandleFunc("GET /api/v1/public/share/{token}/files/{entryId}", server.PublicShareDownload)
	mux.HandleFunc("GET /api/v1/public/share/{token}/download", server.PublicShareDownloadAll)

	// ── Shares (session-protected) ──
	mux.Handle("GET /api/v1/designs/{designId}/links", server.Auth.Require(http.HandlerFunc(server.ShareLinksIndex)))
	mux.Handle("POST /api/v1/designs/{designId}/links", server.Auth.Require(http.HandlerFunc(server.ShareLinksStore)))
	mux.Handle("DELETE /api/v1/designs/{designId}/links/{linkId}", server.Auth.Require(http.HandlerFunc(server.ShareLinksDestroy)))
	mux.Handle("GET /api/v1/designs/{designId}/shares", server.Auth.Require(http.HandlerFunc(server.SharesIndex)))
	mux.Handle("POST /api/v1/designs/{designId}/shares", server.Auth.Require(http.HandlerFunc(server.SharesStore)))
	mux.Handle("DELETE /api/v1/designs/{designId}/shares/{shareId}", server.Auth.Require(http.HandlerFunc(server.SharesDestroy)))

	// ── Notifications (session-protected, self-scoped) ──
	mux.Handle("GET /api/v1/users/{id}/notifications", server.Auth.Require(http.HandlerFunc(server.NotificationsIndex)))
	mux.Handle("POST /api/v1/users/{id}/notifications/read-all", server.Auth.Require(http.HandlerFunc(server.NotificationsReadAll)))
	mux.Handle("DELETE /api/v1/users/{id}/notifications", server.Auth.Require(http.HandlerFunc(server.NotificationsDeleteAll)))
	mux.Handle("DELETE /api/v1/users/{id}/notifications/{notifId}", server.Auth.Require(http.HandlerFunc(server.NotificationsDelete)))
	mux.Handle("GET /api/v1/users/{id}/notification-prefs", server.Auth.Require(http.HandlerFunc(server.NotificationsGetPrefs)))
	mux.Handle("PUT /api/v1/users/{id}/notification-prefs", server.Auth.Require(http.HandlerFunc(server.NotificationsSavePrefs)))

	// ── Users ──
	mux.Handle("GET /api/v1/users/search", server.Auth.Require(http.HandlerFunc(server.UsersSearch)))
	mux.Handle("PUT /api/v1/users/{id}/profile", server.Auth.Require(http.HandlerFunc(server.UsersUpdateProfile)))
	mux.HandleFunc("GET /api/v1/users/{id}/avatar", server.UsersServeAvatar) // public
	mux.Handle("POST /api/v1/users/{id}/avatar", server.Auth.Require(http.HandlerFunc(server.UsersUploadAvatar)))
	mux.Handle("DELETE /api/v1/users/{id}/avatar", server.Auth.Require(http.HandlerFunc(server.UsersDeleteAvatar)))
	mux.Handle("POST /api/v1/users/{id}/change-password", server.Auth.Require(http.HandlerFunc(server.UsersChangePassword)))
	mux.Handle("POST /api/v1/users/{id}/force-password", server.Auth.Require(http.HandlerFunc(server.UsersForcePassword)))
	mux.Handle("GET /api/v1/users/{id}/stats", server.Auth.Require(http.HandlerFunc(server.UsersStats)))
	mux.Handle("GET /api/v1/users/{id}/sync-state", server.Auth.Require(http.HandlerFunc(server.SyncState)))
	mux.Handle("GET /api/v1/users/{id}/share-links", server.Auth.Require(http.HandlerFunc(server.UserShareLinksIndex)))

	// ── Platform accounts (session-protected, self-scoped) ──
	mux.Handle("GET /api/v1/users/{id}/platform-accounts", server.Auth.Require(http.HandlerFunc(server.PlatformAccountsIndex)))
	mux.Handle("POST /api/v1/users/{id}/platform-accounts", server.Auth.Require(http.HandlerFunc(server.PlatformAccountsSave)))
	mux.Handle("POST /api/v1/users/{id}/platform-accounts/validate", server.Auth.Require(http.HandlerFunc(server.PlatformAccountsValidate)))
	mux.Handle("DELETE /api/v1/users/{id}/platform-accounts/{accountId}", server.Auth.Require(http.HandlerFunc(server.PlatformAccountsDelete)))
	mux.Handle("POST /api/v1/users/{id}/platform-accounts/sync-all", server.Auth.Require(http.HandlerFunc(server.PlatformAccountsSyncAll)))
	mux.Handle("POST /api/v1/users/{id}/platform-accounts/{platform}/sync", server.Auth.Require(http.HandlerFunc(server.PlatformAccountsSyncOne)))

	// ── Admin (admin rights required) ──
	mux.Handle("GET /api/v1/admin/users", server.Auth.RequireAdmin(http.HandlerFunc(server.AdminList)))
	mux.Handle("POST /api/v1/admin/users", server.Auth.RequireAdmin(http.HandlerFunc(server.AdminCreate)))
	mux.Handle("PUT /api/v1/admin/users/{id}", server.Auth.RequireAdmin(http.HandlerFunc(server.AdminUpdate)))
	mux.Handle("DELETE /api/v1/admin/users/{id}", server.Auth.RequireAdmin(http.HandlerFunc(server.AdminDelete)))
	mux.Handle("POST /api/v1/admin/users/{id}/reset-password", server.Auth.RequireAdmin(http.HandlerFunc(server.AdminResetPassword)))
	mux.Handle("GET /api/v1/admin/users/{id}/detail", server.Auth.RequireAdmin(http.HandlerFunc(server.AdminDetail)))
	mux.Handle("GET /api/v1/admin/stats", server.Auth.RequireAdmin(http.HandlerFunc(server.AdminStats)))
	mux.Handle("GET /api/v1/admin/health", server.Auth.RequireAdmin(http.HandlerFunc(server.AdminHealth)))
	mux.Handle("POST /api/v1/admin/translations/backfill", server.Auth.RequireAdmin(http.HandlerFunc(server.RunTranslationBackfill)))
	mux.Handle("GET /api/v1/admin/settings", server.Auth.RequireAdmin(http.HandlerFunc(server.GetSettings)))
	mux.Handle("PUT /api/v1/admin/settings", server.Auth.RequireAdmin(http.HandlerFunc(server.SaveSettings)))
	mux.Handle("GET /api/v1/admin/mail", server.Auth.RequireAdmin(http.HandlerFunc(server.GetMailSettings)))
	mux.Handle("PUT /api/v1/admin/mail", server.Auth.RequireAdmin(http.HandlerFunc(server.SaveMailSettings)))
	mux.Handle("POST /api/v1/admin/mail/test", server.Auth.RequireAdmin(http.HandlerFunc(server.TestMailSettings)))
	mux.Handle("POST /api/v1/admin/library-sync/run", server.Auth.RequireAdmin(http.HandlerFunc(server.RunLibrarySync)))
	mux.Handle("POST /api/v1/admin/queue/{platform}/pause", server.Auth.RequireAdmin(http.HandlerFunc(server.QueuePause)))
	mux.Handle("POST /api/v1/admin/queue/{platform}/resume", server.Auth.RequireAdmin(http.HandlerFunc(server.QueueResume)))
	mux.Handle("GET /api/v1/settings/public", server.Auth.Require(http.HandlerFunc(server.GetPublicSettings)))

	// ── Embedded SPA (catch-all; /api/* stays unaffected) ──
	mux.Handle("/", webui.Handler())

	return server.cors(mux)
}

// cors allows exactly one configured origin (APP_URL) to make credentialed
// cross-origin calls, and answers OPTIONS preflights with 204.
//
// Without APP_URL no CORS headers are sent at all. The SPA is served from this
// same origin, so it needs none; the earlier behaviour - reflecting whatever
// Origin the request carried, together with Allow-Credentials: true - let any
// website read authenticated API responses, and it was the default. SameSite
// cookies happen to block that in current browsers, but CORS and cookie policy
// are two separate lines of defence and the default must not disarm one of them.
func (server *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		allowedOrigin := strings.TrimRight(server.Cfg.AppURL, "/")
		requestOrigin := request.Header.Get("Origin")
		if allowedOrigin != "" {
			header := responseWriter.Header()
			header.Add("Vary", "Origin")
			if strings.TrimRight(requestOrigin, "/") == allowedOrigin {
				header.Set("Access-Control-Allow-Origin", requestOrigin)
				header.Set("Access-Control-Allow-Credentials", "true")
				header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				header.Set("Access-Control-Max-Age", "600")
			}
		}
		if request.Method == http.MethodOptions {
			responseWriter.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(responseWriter, request)
	})
}
