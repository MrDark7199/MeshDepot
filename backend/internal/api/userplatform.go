package api

import (
	"meshdepot/internal/coerce"
	"net/http"
	"strconv"
	"strings"
	"time"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/platforms"
	"meshdepot/internal/safego"
)

// accountView builds the API view of a platform_accounts row: secrets removed,
// token and username decrypted, flags as bool.
func (server *Server) accountView(row map[string]any, currentUserID int) map[string]any {
	view := map[string]any{
		"id":                row["id"],
		"platform":          row["platform"],
		"token_expires_at":  row["token_expires_at"],
		"updated_at":        row["updated_at"],
		"has_password":      coerce.StringOr(row["password_encrypted"], "") != "",
		"has_totp_secret":   coerce.StringOr(row["totp_secret"], "") != "",
		"sync_likes":        coerce.Int(row["sync_likes"]) == 1,
		"sync_collections":  coerce.Int(row["sync_collections"]) == 1,
		"auto_library_sync": coerce.Int(row["auto_library_sync"]) == 1,
		// Seconds this account's "sync now" stays disabled; the client would otherwise
		// offer a call the server only rejects.
		"sync_cooldown_seconds": syncCooldownRemaining(coerce.StringOr(row["library_last_synced_at"], "")),
		// Stored but unreadable credentials would otherwise look like an account that
		// was never finished setting up.
		"credentials_unreadable": server.credentialsUnreadable(row, currentUserID),
	}
	view["token"] = server.decryptField(row["token"], currentUserID)
	view["username"] = server.decryptField(row["username"], currentUserID)
	return view
}

// credentialsUnreadable reports a credential that is present but will not
// decrypt, which is what an APP_KEY change leaves behind. Without the flag the UI
// cannot tell "never entered" from "unreadable", and a validation run checks the
// platform with an empty password and blames the credentials just typed.
func (server *Server) credentialsUnreadable(row map[string]any, currentUserID int) bool {
	// username is not checked: older rows keep it in plaintext and the readers fall
	// back to that, so a failed decrypt there is normal.
	for _, column := range []string{"token", "password_encrypted", "totp_secret"} {
		stored := coerce.StringOr(row[column], "")
		if stored == "" {
			continue
		}
		if _, ok := server.Crypto.Decrypt(stored, currentUserID); !ok {
			return true
		}
	}
	return false
}

func (server *Server) decryptField(value any, currentUserID int) any {
	encrypted := coerce.StringOr(value, "")
	if encrypted == "" {
		return nil
	}
	if decrypted, ok := server.Crypto.Decrypt(encrypted, currentUserID); ok {
		return decrypted
	}
	return nil
}

func (server *Server) PlatformAccountsIndex(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	rows, _ := dbutil.QueryMaps(server.DB, `
		SELECT id, platform, token, username, password_encrypted, totp_secret,
			token_expires_at, updated_at, sync_likes, sync_collections, auto_library_sync,
			library_last_synced_at
		FROM platform_accounts WHERE user_id = ? AND state = 'active' ORDER BY platform ASC`, currentUserID)
	views := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		views = append(views, server.accountView(row, currentUserID))
	}
	httpx.Success(responseWriter, views)
}

func (server *Server) PlatformAccountsSave(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)
	platform := strings.TrimSpace(coerce.StringOr(body["platform"], ""))
	if platform == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.platform_required")
		return
	}
	token := trimOrNil(body["token"])
	username := trimOrNil(body["username"])
	password := coerce.StringOr(body["password"], "")
	totp := trimOrNil(body["totp_secret"])

	syncLikes := intFlag(body, "sync_likes", 1)
	syncCollections := intFlag(body, "sync_collections", 1)
	autoSync := intFlag(body, "auto_library_sync", 1)

	encryptOrNil := func(value *string) any {
		if value == nil {
			return nil
		}
		encrypted, _ := server.Crypto.Encrypt(*value, currentUserID)
		return encrypted
	}
	var tokenEncrypted any = encryptOrNil(token)
	var userEncrypted any = encryptOrNil(username)
	var passwordEncrypted any
	if password != "" && password != "***" {
		passwordEncrypted, _ = server.Crypto.Encrypt(password, currentUserID)
	}
	var totpEncrypted any = encryptOrNil(totp)

	// A failed read may not count as "no account yet": that inserts a second active
	// row and resolves the masked fields to nil, wiping the stored token.
	existing, hasExisting, failure := dbutil.QueryMap(server.DB, `
		SELECT id, token, username, password_encrypted, totp_secret, sync_likes, sync_collections, auto_library_sync
		FROM platform_accounts WHERE user_id = ? AND platform = ? AND state = 'active' LIMIT 1`, currentUserID, platform)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}

	oldLikes, oldCollections, oldAuto := 0, 0, 0
	if hasExisting {
		oldLikes = coerce.Int(existing["sync_likes"])
		oldCollections = coerce.Int(existing["sync_collections"])
		oldAuto = coerce.Int(existing["auto_library_sync"])
	}
	turnedOn := (syncLikes == 1 && oldLikes == 0) || (syncCollections == 1 && oldCollections == 0) || (autoSync == 1 && oldAuto == 0)
	// The server switch wins, or the response reports auto_synced although
	// RunLibrarySyncFor drops the run.
	shouldAutoSync := autoSync == 1 && turnedOn && (syncLikes == 1 || syncCollections == 1) && platforms.LibrarySyncEnabled(server.DB)

	// The write must be reported honestly: a swallowed error leaves the user
	// believing the credentials are stored, and the next sync fails for no visible
	// reason. A carried-over secret that will not decrypt is worthless and is
	// dropped, so the next login fetches a fresh token instead of retrying a dead
	// one. The username is exempt: older rows store it in plaintext.
	keepIfReadable := func(stored any) any {
		encrypted := coerce.StringOr(stored, "")
		if encrypted == "" {
			return nil
		}
		if _, ok := server.Crypto.Decrypt(encrypted, currentUserID); ok {
			return stored
		}
		return nil
	}

	var writeFailure error
	if hasExisting {
		if token == nil {
			tokenEncrypted = keepIfReadable(existing["token"])
		}
		if username == nil {
			userEncrypted = existing["username"]
		}
		if password == "***" || password == "" {
			passwordEncrypted = keepIfReadable(existing["password_encrypted"])
		}
		if totp == nil {
			totpEncrypted = keepIfReadable(existing["totp_secret"])
		}
		_, writeFailure = server.DB.Exec(`UPDATE platform_accounts
			SET token = ?, username = ?, password_encrypted = ?, totp_secret = ?,
				token_expires_at = CASE WHEN ? IS NULL THEN NULL ELSE token_expires_at END,
				sync_likes = ?, sync_collections = ?, auto_library_sync = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			tokenEncrypted, userEncrypted, passwordEncrypted, totpEncrypted, tokenEncrypted,
			syncLikes, syncCollections, autoSync, existing["id"])
	} else {
		_, writeFailure = server.DB.Exec(`INSERT INTO platform_accounts
			(user_id, platform, token, username, password_encrypted, totp_secret, sync_likes, sync_collections, auto_library_sync, state, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', CURRENT_TIMESTAMP)`,
			currentUserID, platform, tokenEncrypted, userEncrypted, passwordEncrypted, totpEncrypted, syncLikes, syncCollections, autoSync)
	}
	if writeFailure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}

	if shouldAutoSync {
		server.triggerLibrarySync(currentUserID, platform) // phase 8: background sync
	}

	row, ok := server.fetchRow(responseWriter, `
		SELECT id, platform, token, username, password_encrypted, totp_secret,
			token_expires_at, updated_at, sync_likes, sync_collections, auto_library_sync
		FROM platform_accounts WHERE user_id = ? AND platform = ? AND state = 'active' LIMIT 1`, currentUserID, platform)
	if !ok {
		return
	}
	view := server.accountView(row, currentUserID)
	view["auto_synced"] = shouldAutoSync
	httpx.SuccessMessage(responseWriter, view, "Saved")
}

func (server *Server) PlatformAccountsDelete(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	accountID, ok := pathInt(request, "accountId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	dbutil.ExecLogged(server.DB, "DELETE FROM platform_accounts WHERE id = ? AND user_id = ?", accountID, currentUserID)
	httpx.SuccessMessage(responseWriter, nil, "Deleted")
}

// manualSyncCooldown is how long the "sync now" buttons stay closed. Every
// trigger walks a whole library on the platform, and clicking repeatedly is the
// burst that gets an account blocked - for a sync that finds nothing new anyway.
const manualSyncCooldown = 10 * time.Minute

const sqliteTimeLayout = "2006-01-02 15:04:05"

// syncCooldownRemaining reports the seconds left. An empty or unreadable stamp
// means never synced, which must not block the first sync.
func syncCooldownRemaining(lastSync string) int {
	if strings.TrimSpace(lastSync) == "" {
		return 0
	}
	stamp, failure := time.Parse(sqliteTimeLayout, strings.TrimSpace(lastSync))
	if failure != nil {
		return 0
	}
	remaining := manualSyncCooldown - time.Since(stamp)
	if remaining <= 0 {
		return 0
	}
	return int(remaining.Seconds()) + 1
}

// guardSyncCooldown rejects a manual sync too soon after the last. An empty
// platform means all of the member's accounts.
func (server *Server) guardSyncCooldown(responseWriter http.ResponseWriter, userID int, platform string) bool {
	// The account-wide stamp guards "sync all" only, and covers a member with no
	// platform account. A single-platform sync must not be blocked by it, or syncing
	// one platform puts every other "sync now" on cooldown.
	if platform == "" {
		var lastManual string
		if server.DB.QueryRow("SELECT COALESCE(last_manual_sync_at, '') FROM users WHERE id = ?", userID).Scan(&lastManual) == nil {
			if syncCooldownRemaining(lastManual) > 0 {
				httpx.Error(responseWriter, http.StatusTooManyRequests, "error.sync_cooldown")
				return false
			}
		}
	}
	query := "SELECT COALESCE(MAX(library_last_synced_at), '') FROM platform_accounts WHERE user_id = ? AND state = 'active'"
	args := []any{userID}
	if platform != "" {
		query += " AND platform = ?"
		args = append(args, platform)
	}
	var lastSync string
	if server.DB.QueryRow(query, args...).Scan(&lastSync) != nil {
		return true
	}
	if syncCooldownRemaining(lastSync) == 0 {
		return true
	}
	httpx.Error(responseWriter, http.StatusTooManyRequests, "error.sync_cooldown")
	return false
}

func (server *Server) PlatformAccountsSyncAll(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	if !server.guardLibrarySyncEnabled(responseWriter) || !server.guardSyncCooldown(responseWriter, currentUserID, "") {
		return
	}
	server.triggerLibrarySync(currentUserID, "")
	httpx.SuccessMessage(responseWriter, nil, "Sync started")
}

func (server *Server) PlatformAccountsSyncOne(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	platform := request.PathValue("platform")
	if !server.guardLibrarySyncEnabled(responseWriter) || !server.guardSyncCooldown(responseWriter, currentUserID, platform) {
		return
	}
	server.triggerLibrarySync(currentUserID, platform)
	httpx.SuccessMessage(responseWriter, nil, "Sync started")
}

// guardLibrarySyncEnabled answers a client that asks anyway while the server
// switch is off - with a reason rather than a silent success.
func (server *Server) guardLibrarySyncEnabled(responseWriter http.ResponseWriter) bool {
	if platforms.LibrarySyncEnabled(server.DB) {
		return true
	}
	httpx.Error(responseWriter, http.StatusForbidden, "error.library_sync_disabled")
	return false
}

// PlatformAccountsValidate checks entered credentials without saving.
// Thingiverse and MyMiniFactory check their token over HTTP, the rest perform a
// real login. A platform with neither validator is saved unchecked.
func (server *Server) PlatformAccountsValidate(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID, ok := server.requireSelf(responseWriter, request)
	if !ok {
		return
	}
	var body map[string]any
	_ = httpx.DecodeJSON(request, &body)
	platform := strings.TrimSpace(coerce.StringOr(body["platform"], ""))
	email := strings.TrimSpace(coerce.StringOr(body["username"], ""))
	password := coerce.StringOr(body["password"], "")
	token := strings.TrimSpace(coerce.StringOr(body["token"], ""))
	totp := strings.TrimSpace(coerce.StringOr(body["totp_secret"], ""))

	// Noted before the masked fields are resolved: whether the user actually typed a
	// secret decides how an unreadable stored one is handled below.
	suppliedSecret := (password != "" && password != "***") || (token != "" && token != "***")

	// A failed read would silently validate with empty values and report wrong
	// credentials to the user.
	saved, hasSaved, failure := dbutil.QueryMap(server.DB, `
		SELECT password_encrypted, username, token, totp_secret FROM platform_accounts
		WHERE user_id = ? AND platform = ? AND state = 'active' LIMIT 1`, currentUserID, platform)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	// A stored value that will not decrypt must not fall back to "": that is a login
	// attempt with an empty password, blamed on the user's credentials.
	storedUnreadable := false
	decryptSaved := func(key string) string {
		if !hasSaved {
			return ""
		}
		stored := coerce.StringOr(saved[key], "")
		if stored == "" {
			return ""
		}
		if value, ok := server.Crypto.Decrypt(stored, currentUserID); ok {
			return value
		}
		storedUnreadable = true
		return ""
	}
	if email == "" {
		email = decryptSaved("username")
	}
	if password == "" || password == "***" {
		password = decryptSaved("password_encrypted")
	}
	if token == "" || token == "***" {
		token = decryptSaved("token")
	}
	if totp == "" || totp == "***" {
		totp = decryptSaved("totp_secret")
	}
	// Only give up when there is nothing to go on. A typed password or token is
	// exactly what this request overwrites, and refusing left the user with a red
	// account they had no way to repair. The remaining case is a form with masked
	// fields only, where validating would test empty values.
	if storedUnreadable && !suppliedSecret {
		httpx.Success(responseWriter, map[string]any{
			"ok":    false,
			"error": "error.platform_credentials_unreadable",
		})
		return
	}

	downloader := server.Registry.Get(platform)
	credentials := platforms.Credentials{Email: email, Password: password, Token: token, TOTP: totp}
	var errorMessage any
	switch validator := downloader.(type) {
	case platforms.ReasonValidator:
		// A precise i18n key: token versus username wrong.
		if reason := validator.ValidateReason(credentials); reason != "" {
			errorMessage = reason
		}
	case platforms.Validator:
		if !validator.Validate(credentials) {
			errorMessage = "error.platform_invalid_credentials"
		}
	default:
		httpx.Success(responseWriter, map[string]any{"ok": true, "unsupported": true})
		return
	}
	httpx.Success(responseWriter, map[string]any{"ok": errorMessage == nil, "error": errorMessage})
}

// triggerLibrarySync runs the sync in the background. One already running for
// the same (user, platform) is not started again: ten presses used to start ten
// syncs, whose rate-limiting sleeps then stop working and whose burst the
// platform may well block.
func (server *Server) triggerLibrarySync(userID int, platform string) {
	key := strconv.Itoa(userID) + "|" + platform
	if _, running := server.syncsInFlight.LoadOrStore(key, struct{}{}); running {
		return
	}
	// The cooldown starts here rather than when the sync finishes: it protects the
	// platform from repeated triggers, and a ten-minute run would leave the button
	// open throughout.
	server.markSyncStarted(userID, platform)
	safego.Go("library-sync", func() {
		defer server.syncsInFlight.Delete(key)
		platforms.RunLibrarySyncFor(server.Deps, userID, platform)
	})
}

func trimOrNil(value any) *string {
	trimmed := strings.TrimSpace(coerce.StringOr(value, ""))
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// markSyncStarted stamps the accounts, which is what guardSyncCooldown and the
// button state read.
func (server *Server) markSyncStarted(userID int, platform string) {
	query := "UPDATE platform_accounts SET library_last_synced_at = CURRENT_TIMESTAMP WHERE user_id = ? AND state = 'active'"
	args := []any{userID}
	if platform != "" {
		query += " AND platform = ?"
		args = append(args, platform)
	}
	dbutil.ExecLogged(server.DB, query, args...)
	// The account-wide stamp is only for "sync all": setting it on a single-platform
	// sync would put every other platform on cooldown too (issue #4). For "sync all"
	// it also covers a member with no platform account.
	if platform == "" {
		dbutil.ExecLogged(server.DB, "UPDATE users SET last_manual_sync_at = CURRENT_TIMESTAMP WHERE id = ?", userID)
	}
}
