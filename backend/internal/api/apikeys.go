package api

// Management of a member's own API keys. A key is shown exactly once, when it is
// created; everything afterwards works with its head ("mdp_A1b2c3") and its
// dates, which is enough to tell two apart and to spot one still in use.

import (
	"meshdepot/internal/logx"
	"net/http"
	"strings"
	"time"

	"meshdepot/internal/auth"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
)

// maxAPIKeysPerUser is a sanity bound: a runaway client that creates one per
// request should hit something.
const maxAPIKeysPerUser = 20

func (server *Server) APIKeysIndex(responseWriter http.ResponseWriter, request *http.Request) {
	rows, failure := dbutil.QueryMaps(server.DB, `
		SELECT id, name, prefix, created_at, last_used_at, expires_at,
		       CASE WHEN expires_at IS NOT NULL AND expires_at <= CURRENT_TIMESTAMP THEN 1 ELSE 0 END AS expired
		FROM api_keys WHERE user_id = ? AND revoked_at IS NULL ORDER BY id DESC`, userID(request))
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	httpx.Success(responseWriter, rows)
}

// APIKeysStore returns the key in plaintext - the only time it is readable.
func (server *Server) APIKeysStore(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)

	var body struct {
		Name string `json:"name"`
		// 0 means never, which stays possible on purpose - a key for a machine nobody
		// will renew is a real case - but it is a choice, not the default.
		ExpiresInDays int `json:"expires_in_days"`
	}
	_ = httpx.DecodeJSON(request, &body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Browser extension"
	}
	if len(name) > 60 {
		name = name[:60]
	}

	var existing int
	if failure := server.DB.QueryRow("SELECT COUNT(*) FROM api_keys WHERE user_id = ? AND revoked_at IS NULL",
		currentUserID).Scan(&existing); failure != nil {
		// A failed count reads as zero, which is the one answer that lets the
		// limit be passed.
		logx.Errorf("[api] api key count failed (user %d): %v", currentUserID, failure)
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if existing >= maxAPIKeysPerUser {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.too_many_api_keys")
		return
	}

	key, failure := auth.NewAPIKey()
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	// A ceiling rather than a validated list: the interface offers a few durations,
	// and anything else is fine as long as it is bounded.
	days := body.ExpiresInDays
	if days < 0 || days > 3650 {
		days = 0
	}
	var expiresAt any
	if days > 0 {
		expiresAt = time.Now().AddDate(0, 0, days).UTC().Format("2006-01-02 15:04:05")
	}

	result, failure := server.DB.Exec(
		"INSERT INTO api_keys (user_id, name, key_hash, prefix, expires_at) VALUES (?, ?, ?, ?, ?)",
		currentUserID, name, auth.HashAPIKey(key), auth.APIKeyPreview(key), expiresAt)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	identifier, _ := result.LastInsertId()

	httpx.SuccessStatus(responseWriter, http.StatusCreated, map[string]any{
		"id":         identifier,
		"name":       name,
		"prefix":     auth.APIKeyPreview(key),
		"expires_at": expiresAt,
		// Returned here and nowhere else: only the hash is stored.
		"key": key,
	}, "API key created")
}

// APIKeysDestroy revokes rather than deletes, so the row keeps its dates and it
// stays visible that a key existed and when it was last used.
func (server *Server) APIKeysDestroy(responseWriter http.ResponseWriter, request *http.Request) {
	identifier, ok := pathInt(request, "id")
	if !ok {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	result, failure := server.DB.Exec(
		"UPDATE api_keys SET revoked_at = CURRENT_TIMESTAMP WHERE id = ? AND user_id = ? AND revoked_at IS NULL",
		identifier, userID(request))
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	httpx.Success(responseWriter, map[string]any{"revoked": true})
}
