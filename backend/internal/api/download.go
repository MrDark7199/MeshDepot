package api

import (
	"log"
	"meshdepot/internal/coerce"
	"net/http"
	"strings"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/platforms"
)

// How long a finished queue entry stays in the list the frontend polls (SQLite
// date modifiers). A failed download has to survive long enough for the user to
// come back and see it; a successful one only needs to outlive one poll
// interval, otherwise the queue would never look empty.
const (
	failedJobVisible = "-7 days"
	doneJobVisible   = "-60 seconds"
	doneSyncVisible  = "-30 seconds"
)

// platformCredsOK checks whether the credentials required for the platform are
// present. Empty errorKey = ok.
func (server *Server) platformCredsOK(platform string, currentUserID int) (bool, string) {
	if !platforms.NeedsCredentials(platform) {
		return true, ""
	}
	// A failed read is reported as a server error rather than as "credentials
	// missing" - otherwise the user is sent to re-enter credentials that are
	// stored correctly.
	account, hasAccount, failure := dbutil.QueryMap(server.DB, "SELECT token, username FROM platform_accounts WHERE user_id = ? AND platform = ? LIMIT 1", currentUserID, platform)
	if failure != nil {
		log.Printf("[api] platform credentials lookup failed (user %d, %s): %v", currentUserID, platform, failure)
		return false, "error.server"
	}
	hasToken := hasAccount && coerce.StringOr(account["token"], "") != ""
	hasUser := hasAccount && coerce.StringOr(account["username"], "") != ""
	switch platform {
	case "thingiverse":
		if !hasToken {
			return false, "error.platform_credentials_required:thingiverse"
		}
	case "makerworld":
		if !hasToken || !hasUser {
			return false, "error.platform_credentials_required:makerworld"
		}
	case "myminifactory":
		if !hasToken {
			return false, "error.platform_credentials_required:myminifactory"
		}
	case "cults3d":
		// The download needs email (+ password); the API key (token) is optional and
		// only for the sync. So the email (username) is enough here.
		if !hasUser {
			return false, "error.platform_credentials_required:cults3d"
		}
	case "printables", "thangs":
		// Both log in with email + password and hand the token they get back to
		// the download. Without an account the token stays empty, the file
		// request comes back empty-handed, and the queue entry fails minutes
		// later with an error nobody connects to the missing account.
		if !hasUser && !hasToken {
			return false, "error.platform_credentials_required:" + platform
		}
	}
	return true, ""
}

// DownloadQueue puts a platform download into the queue.
func (server *Server) DownloadQueue(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	var body struct {
		SourceURL string `json:"source_url"`
	}
	_ = httpx.DecodeJSON(request, &body)
	sourceURL := strings.TrimSpace(body.SourceURL)
	if sourceURL == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.url_required")
		return
	}
	platform := detectPlatform(sourceURL)
	if platform == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.unsupported_platform")
		return
	}
	if ok, errorKey := server.platformCredsOK(platform, currentUserID); !ok {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, errorKey)
		return
	}
	duplicate, isDuplicate, failure := dbutil.QueryMap(server.DB, "SELECT id, name FROM designs WHERE user_id = ? AND source_url = ? LIMIT 1", currentUserID, sourceURL)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if isDuplicate {
		httpx.Error(responseWriter, http.StatusConflict, "error.duplicate_design:"+coerce.StringOr(duplicate["name"], "Unknown"))
		return
	}
	// Asking for this design by hand outranks an earlier "delete and keep it
	// gone": without lifting the block the import would succeed and the design
	// would silently disappear again on the next library sync.
	platforms.LiftSyncExclusion(server.DB, currentUserID, platform, "", sourceURL)
	insertResult, failure := server.DB.Exec("INSERT INTO download_queue (user_id, source_url, platform, status) VALUES (?, ?, ?, 'pending')", currentUserID, sourceURL, platform)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	id, _ := insertResult.LastInsertId()
	httpx.SuccessStatus(responseWriter, http.StatusCreated, map[string]any{"queue_id": id, "platform": platform, "status": "pending"}, "Download queued")
}

// DownloadList returns active/recently completed queue entries.
func (server *Server) DownloadList(responseWriter http.ResponseWriter, request *http.Request) {
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT dq.id, dq.source_url, dq.platform, dq.status, dq.error_msg, dq.retry_count,
			dq.created_at, dq.started_at, dq.done_at, dq.current_step, dq.step_current, dq.step_total,
			d.public_id AS design_id, d.name AS design_name FROM download_queue dq
		LEFT JOIN designs d ON d.id = dq.design_id
		WHERE dq.user_id = ? AND (
			dq.status IN ('pending','downloading')
			OR (dq.status = 'failed' AND dq.done_at > datetime('now', ?))
			OR (dq.status = 'done'   AND dq.done_at > datetime('now', ?))
		) ORDER BY dq.created_at DESC`, userID(request), failedJobVisible, doneJobVisible)
	httpx.Success(responseWriter, rows)
}

func (server *Server) DownloadStatus(responseWriter http.ResponseWriter, request *http.Request) {
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	row, ok := server.fetchRow(responseWriter, `
		SELECT dq.id, dq.source_url, dq.platform, dq.status, dq.error_msg, dq.retry_count,
			dq.created_at, dq.started_at, dq.done_at, dq.current_step, dq.step_current, dq.step_total,
			d.public_id AS design_id, d.name AS design_name
		FROM download_queue dq LEFT JOIN designs d ON d.id = dq.design_id
		WHERE dq.id = ? AND dq.user_id = ? LIMIT 1`, id, userID(request))
	if !ok {
		return
	}
	httpx.Success(responseWriter, row)
}

// DownloadRetry resets an entry back to pending.
func (server *Server) DownloadRetry(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	job, ok := server.fetchRow(responseWriter, "SELECT id, platform, source_url FROM download_queue WHERE id = ? AND user_id = ? LIMIT 1", id, currentUserID)
	if !ok {
		return
	}
	if ok, errorKey := server.platformCredsOK(coerce.StringOr(job["platform"], ""), currentUserID); !ok {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, errorKey)
		return
	}
	dbutil.ExecLogged(server.DB, "UPDATE download_queue SET status='pending', error_msg=NULL, done_at=NULL, retry_count=0 WHERE id = ? AND user_id = ?", id, currentUserID)
	httpx.SuccessMessage(responseWriter, map[string]any{"queue_id": id, "status": "pending"}, "Retrying download")
}

// DownloadCancel removes a pending/downloading entry.
func (server *Server) DownloadCancel(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	result, _ := server.DB.Exec("DELETE FROM download_queue WHERE id = ? AND user_id = ? AND status IN ('pending','downloading')", id, currentUserID)
	if affected, _ := result.RowsAffected(); affected == 0 {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	httpx.SuccessMessage(responseWriter, nil, "Cancelled")
}

// DownloadDismiss removes a failed entry.
func (server *Server) DownloadDismiss(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	id, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	dbutil.ExecLogged(server.DB, "DELETE FROM download_queue WHERE id = ? AND user_id = ? AND status = 'failed'", id, currentUserID)
	httpx.SuccessMessage(responseWriter, nil, "Dismissed")
}

// SyncAll enqueues all syncable designs into the sync_queue.
//
// Every queued design is a full re-download from its platform, so the run gets
// the same cooldown as the manual library sync: triggering it repeatedly is the
// burst that gets a platform account rate-limited, and the second run would
// find nothing the first one has not already fetched.
func (server *Server) SyncAll(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	if !server.guardUpdateAllCooldown(responseWriter, currentUserID) {
		return
	}
	rows, _ := dbutil.QueryMaps(server.DB, "SELECT id FROM designs WHERE user_id = ? AND source_url IS NOT NULL AND source_url != ''", currentUserID)
	queued := 0
	for _, designRow := range rows {
		designIDValue := coerce.Int(designRow["id"])
		// A failed probe skips the design instead of enqueueing it: sync_queue has no
		// unique constraint, so "unknown" must not turn into a second running job for
		// the same design. The user can trigger the sync again.
		_, alreadyQueued, failure := dbutil.QueryMap(server.DB, "SELECT id FROM sync_queue WHERE design_id = ? AND status IN ('pending','running') LIMIT 1", designIDValue)
		if failure != nil || alreadyQueued {
			continue
		}
		dbutil.ExecLogged(server.DB, "INSERT INTO sync_queue (design_id, user_id, status) VALUES (?, ?, 'pending')", designIDValue, currentUserID)
		queued++
	}
	// Stamped even when nothing was queued: a library that is already fully
	// queued would otherwise let the button be pressed again immediately.
	dbutil.ExecLogged(server.DB, "UPDATE users SET last_update_all_at = CURRENT_TIMESTAMP WHERE id = ?", currentUserID)
	httpx.Success(responseWriter, map[string]any{"queued": queued, "cooldown_seconds": int(manualSyncCooldown.Seconds())})
}

// guardUpdateAllCooldown rejects an "update all designs" run that follows the
// previous one too closely.
func (server *Server) guardUpdateAllCooldown(responseWriter http.ResponseWriter, currentUserID int) bool {
	var lastRun string
	if server.DB.QueryRow("SELECT COALESCE(last_update_all_at, '') FROM users WHERE id = ?", currentUserID).Scan(&lastRun) != nil {
		return true
	}
	if syncCooldownRemaining(lastRun) == 0 {
		return true
	}
	httpx.Error(responseWriter, http.StatusTooManyRequests, "error.sync_cooldown")
	return false
}

// SyncState reports what the sync buttons of the account settings need to
// decide whether they may be offered: the two cooldowns, whether any platform
// account exists at all, and whether work is still in the queues.
//
// The client cannot work this out on its own. The cooldowns live on the server,
// and a queue that is still being filled looks empty for a moment - a button
// that only reads its own countdown would reopen in the middle of the run it
// just started.
func (server *Server) SyncState(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	var accounts int
	server.DB.QueryRow("SELECT COUNT(*) FROM platform_accounts WHERE user_id = ? AND state = 'active'", currentUserID).Scan(&accounts)

	var lastManual, lastUpdateAll, lastAccountSync string
	server.DB.QueryRow("SELECT COALESCE(last_manual_sync_at, ''), COALESCE(last_update_all_at, '') FROM users WHERE id = ?", currentUserID).
		Scan(&lastManual, &lastUpdateAll)
	server.DB.QueryRow("SELECT COALESCE(MAX(library_last_synced_at), '') FROM platform_accounts WHERE user_id = ? AND state = 'active'", currentUserID).
		Scan(&lastAccountSync)
	librarySeconds := syncCooldownRemaining(lastManual)
	if fromAccounts := syncCooldownRemaining(lastAccountSync); fromAccounts > librarySeconds {
		librarySeconds = fromAccounts
	}

	var downloadsPending, syncsPending int
	server.DB.QueryRow("SELECT COUNT(*) FROM download_queue WHERE user_id = ? AND status IN ('pending','downloading')", currentUserID).Scan(&downloadsPending)
	server.DB.QueryRow("SELECT COUNT(*) FROM sync_queue WHERE user_id = ? AND status IN ('pending','running')", currentUserID).Scan(&syncsPending)

	httpx.Success(responseWriter, map[string]any{
		"has_accounts":                accounts > 0,
		"library_cooldown_seconds":    librarySeconds,
		"update_all_cooldown_seconds": syncCooldownRemaining(lastUpdateAll),
		"downloads_pending":           downloadsPending,
		"syncs_pending":               syncsPending,
		"queue_busy":                  downloadsPending+syncsPending > 0,
	})
}

// SyncStatus returns active/recently completed sync jobs.
func (server *Server) SyncStatus(responseWriter http.ResponseWriter, request *http.Request) {
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT sq.id, d.public_id AS design_id, sq.status, sq.progress, sq.current_step, sq.step_current, sq.step_total, sq.error_msg, sq.done_at, d.name AS design_name
		FROM sync_queue sq JOIN designs d ON d.id = sq.design_id
		WHERE sq.user_id = ? AND (
			sq.status IN ('pending','running')
			OR (sq.status IN ('done','failed') AND sq.done_at > datetime('now', ?))
		) ORDER BY sq.created_at ASC`, userID(request), doneSyncVisible)
	httpx.Success(responseWriter, rows)
}

// The synchronous SSE endpoints (DownloadStream/SyncStream) were removed: the
// frontend polls the queue and never opened an EventSource, while the streaming
// path bypassed the worker's credential check, cooldowns, retries and rate
// limits - dead code that would have reintroduced those bugs on reactivation.
