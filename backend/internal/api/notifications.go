package api

import (
	"net/http"
	"strconv"

	"meshdepot/internal/coerce"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/publicid"
	"meshdepot/internal/scheduler"
)

// requireSelf ensures the session user is the addressed {id}, which is the
// account's public id. No database access - the session's own public id is in the
// context. A malformed id is a 404, a well-formed foreign one a 403.
func (server *Server) requireSelf(responseWriter http.ResponseWriter, request *http.Request) (int, bool) {
	target := request.PathValue("id")
	if !publicid.Valid(target) {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return 0, false
	}
	if publicID(request) != target {
		httpx.Error(responseWriter, http.StatusForbidden, "error.forbidden")
		return 0, false
	}
	return userID(request), true
}

func (server *Server) NotificationsIndex(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT n.id, n.type, n.title, n.body,
			(SELECT public_id FROM designs WHERE id = n.design_id) AS design_id,
			n.read_at, n.created_at
		FROM notifications n WHERE n.user_id = ? ORDER BY n.created_at DESC LIMIT 50`, currentUserID)
	unread := 0
	for _, notification := range rows {
		if notification["read_at"] == nil {
			unread++
		}
	}
	httpx.Success(responseWriter, map[string]any{"items": rows, "unread": unread})
}

func (server *Server) NotificationsReadAll(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	dbutil.ExecLogged(server.DB, "UPDATE notifications SET read_at = CURRENT_TIMESTAMP WHERE user_id = ? AND read_at IS NULL", currentUserID)
	httpx.SuccessMessage(responseWriter, nil, "Marked as read")
}

func (server *Server) NotificationsDeleteAll(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	dbutil.ExecLogged(server.DB, "DELETE FROM notifications WHERE user_id = ?", currentUserID)
	httpx.Success(responseWriter, nil)
}

func (server *Server) NotificationsDelete(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	notificationID, ok := pathInt(request, "notifId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	result, failure := server.DB.Exec("DELETE FROM notifications WHERE id = ? AND user_id = ?", notificationID, currentUserID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	httpx.Success(responseWriter, nil)
}

func (server *Server) NotificationsGetPrefs(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	// A failed read must not fall through to the defaults: the user would be shown
	// "everything enabled" although their stored preferences say otherwise.
	prefs, found, failure := dbutil.QueryMap(server.DB, "SELECT * FROM notification_prefs WHERE user_id = ? LIMIT 1", currentUserID)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if !found {
		// In-app switches default on, e-mail ones off: an account that never touched
		// this page keeps the bell it had and gets no mail it did not ask for.
		prefs = map[string]any{
			"user_id": currentUserID, "sync_update": 1, "download_failed": 1,
			"download_done": 1, "design_shared": 1, "storage_80": 1, "user_storage_80": 1,
			"sync_min_age_days": 7,
			"sync_update_email": 0, "download_failed_email": 0,
			"download_done_email": 0, "design_shared_email": 0, "storage_80_email": 0,
			"user_storage_80_email": 0,
		}
	}
	httpx.Success(responseWriter, prefs)
}

func (server *Server) NotificationsSavePrefs(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)

	floor := server.designUpdateFloor()
	var syncDays any = floor
	if value, present := body["sync_min_age_days"]; present {
		if value == nil || value == "null" {
			syncDays = nil
		} else {
			// Rejected rather than clamped: the interval decides how often this member's
			// whole library is re-downloaded.
			days := coerce.Int(value)
			if days < floor {
				httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.design_update_interval_too_low")
				return
			}
			syncDays = days
		}
	}
	syncUpdate := intFlag(body, "sync_update", 1)
	downloadFailed := intFlag(body, "download_failed", 1)
	// Evaluated by notify.User but missing here, so the class could never be
	// switched off.
	downloadDone := intFlag(body, "download_done", 1)
	designShared := intFlag(body, "design_shared", 1)
	storage80 := intFlag(body, "storage_80", 1)
	userStorage80 := intFlag(body, "user_storage_80", 1)
	// The e-mail half of each type, independent of the switches above. Default 0, so
	// a client that omits them does not silently enable mail.
	syncUpdateMail := intFlag(body, "sync_update_email", 0)
	downloadFailedMail := intFlag(body, "download_failed_email", 0)
	downloadDoneMail := intFlag(body, "download_done_email", 0)
	designSharedMail := intFlag(body, "design_shared_email", 0)
	storage80Mail := intFlag(body, "storage_80_email", 0)
	userStorage80Mail := intFlag(body, "user_storage_80_email", 0)

	_, failure := server.DB.Exec(`
		INSERT INTO notification_prefs (user_id, sync_update, download_failed, download_done, design_shared, storage_80, user_storage_80,
			sync_update_email, download_failed_email, download_done_email, design_shared_email, storage_80_email, user_storage_80_email,
			sync_min_age_days)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			sync_update = excluded.sync_update,
			download_failed = excluded.download_failed,
			download_done = excluded.download_done,
			design_shared = excluded.design_shared,
			storage_80 = excluded.storage_80,
			user_storage_80 = excluded.user_storage_80,
			sync_update_email = excluded.sync_update_email,
			download_failed_email = excluded.download_failed_email,
			download_done_email = excluded.download_done_email,
			design_shared_email = excluded.design_shared_email,
			storage_80_email = excluded.storage_80_email,
			user_storage_80_email = excluded.user_storage_80_email,
			sync_min_age_days = excluded.sync_min_age_days`,
		currentUserID, syncUpdate, downloadFailed, downloadDone, designShared, storage80, userStorage80,
		syncUpdateMail, downloadFailedMail, downloadDoneMail, designSharedMail, storage80Mail, userStorage80Mail,
		syncDays)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.Success(responseWriter, map[string]any{
		"sync_update": syncUpdate, "download_failed": downloadFailed, "download_done": downloadDone,
		"design_shared": designShared, "storage_80": storage80, "user_storage_80": userStorage80,
		"sync_update_email": syncUpdateMail, "download_failed_email": downloadFailedMail,
		"download_done_email": downloadDoneMail, "design_shared_email": designSharedMail,
		"storage_80_email":  storage80Mail,
		"sync_min_age_days": syncDays,
	})
}

func intFlag(body map[string]any, key string, defaultValue int) int {
	value, present := body[key]
	if !present {
		return defaultValue
	}
	switch number := value.(type) {
	case bool:
		if number {
			return 1
		}
		return 0
	case float64:
		return int(number)
	case string:
		if number == "" || number == "0" || number == "false" {
			return 0
		}
		return 1
	default:
		return defaultValue
	}
}

// designUpdateFloor: the admin can raise the shortest interval a member may
// choose, never lower it below the built-in minimum.
func (server *Server) designUpdateFloor() int {
	floor := scheduler.DesignUpdateMinDays
	var value string
	if server.DB.QueryRow("SELECT value FROM app_settings WHERE key='design_update_min_days'").Scan(&value) == nil {
		if days, failure := strconv.Atoi(value); failure == nil && days > floor {
			floor = days
		}
	}
	return floor
}
