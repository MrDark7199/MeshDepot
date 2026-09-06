package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"meshdepot/internal/crypto"
	"meshdepot/internal/httpx"
)

// Auth bundles all authentication endpoints and their state.
type Auth struct {
	db       *sql.DB
	crypto   *crypto.Crypto
	sessions *SessionManager
	limiter  *rateLimiter
	pending  *ttlStore // totp_pending:{token} -> userID
	setup    *ttlStore // totp_setup:{userID}  -> secret
	used     *ttlStore // totp_used:{userID}:{code} -> "1"
}

func New(db *sql.DB, cryptoHelper *crypto.Crypto, secureCookies bool) *Auth {
	return &Auth{
		db:       db,
		crypto:   cryptoHelper,
		sessions: NewSessionManager(db, secureCookies),
		limiter:  newRateLimiter(),
		pending:  newTTLStore(),
		setup:    newTTLStore(),
		used:     newTTLStore(),
	}
}

// Sessions exposes the session manager (for other packages/tests).
func (service *Auth) Sessions() *SessionManager { return service.sessions }

// userRow is the subset of the users table relevant to Auth.
type userRow struct {
	id         int
	publicID   string
	email      sql.NullString
	hash       string
	name       string
	admin      int
	language   sql.NullString
	mustChange int
	avatarPath sql.NullString
	totpSecret sql.NullString
	state      string
	customCSS  string
	dateFormat string
}

const userColumns = "id, COALESCE(public_id, ''), email, hash, name, admin, language, must_change_password, avatar_path, totp_secret, state, COALESCE(custom_css, ''), COALESCE(date_format, '')"

// scanUser reads a userRow from a row scanner.
func scanUser(row interface{ Scan(...any) error }) (userRow, error) {
	var user userRow
	failure := row.Scan(&user.id, &user.publicID, &user.email, &user.hash, &user.name, &user.admin, &user.language, &user.mustChange, &user.avatarPath, &user.totpSecret, &user.state, &user.customCSS, &user.dateFormat)
	return user, failure
}

// loadByIdentifier loads a user by email or name, whatever its state. The
// identifier is compared case-insensitively (COLLATE NOCASE) - only the
// password must match exactly. Deactivated accounts are returned as well so
// Login can tell their owner why they are refused; every other caller has to
// check userRow.state itself.
func (service *Auth) loadByIdentifier(identifier string) (userRow, error) {
	return scanUser(service.db.QueryRow(
		"SELECT "+userColumns+" FROM users WHERE (email = ? COLLATE NOCASE OR name = ? COLLATE NOCASE) LIMIT 1",
		identifier, identifier,
	))
}

// loadByID loads an active user by ID.
func (service *Auth) loadByID(id int) (userRow, error) {
	return scanUser(service.db.QueryRow(
		"SELECT "+userColumns+" FROM users WHERE id = ? AND state = 'active' LIMIT 1", id,
	))
}

// payload builds the user envelope for API responses (equivalent to
// Auth::userPayload).
func payload(user userRow) map[string]any {
	languageCode := "en"
	if user.language.Valid && user.language.String != "" {
		languageCode = user.language.String
	}
	var avatarURL any
	if user.avatarPath.Valid && user.avatarPath.String != "" {
		avatarURL = "/api/v1/users/" + user.publicID + "/avatar"
	}
	var email any
	if user.email.Valid {
		email = user.email.String
	}
	return map[string]any{
		"id":                   user.publicID,
		"name":                 user.name,
		"email":                email,
		"admin":                user.admin == 1,
		"must_change_password": user.mustChange == 1,
		"language":             languageCode,
		"avatar_url":           avatarURL,
		"totp_enabled":         user.totpSecret.Valid && user.totpSecret.String != "",
		// Travels with the session so the browser can apply it before the first
		// paint. Keeping it in localStorage alone lost it on every reload and on
		// every other device.
		"custom_css": user.customCSS,
		// Travels with the session for the same reason: every date in the interface
		// is written in this notation, and reading it after the first paint would
		// show one format and then swap it for another.
		"date_format": user.dateFormat,
	}
}

type loginBody struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	PendingToken string `json:"pending_token"`
	Code         string `json:"code"`
	Remember     bool   `json:"remember"`
}

// Login authenticates with identifier + password; when TOTP is active a
// pending_token is returned instead of a session.
func (service *Auth) Login(responseWriter http.ResponseWriter, request *http.Request) {
	ip := httpx.ClientIP(request)
	var body loginBody
	_ = httpx.DecodeJSON(request, &body)
	identifier := strings.TrimSpace(body.Email)
	if identifier == "" || body.Password == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.credentials_required")
		return
	}
	if service.limiter.Exceeded(ip) {
		httpx.Error(responseWriter, http.StatusTooManyRequests, "error.too_many_attempts")
		return
	}
	user, failure := service.loadByIdentifier(identifier)
	if failure != nil {
		// Spend the same time as a real bcrypt check (see VerifyDummyPassword).
		VerifyDummyPassword(body.Password)
	}
	if failure != nil || !VerifyPassword(user.hash, body.Password) {
		service.limiter.Record(ip)
		httpx.Error(responseWriter, http.StatusUnauthorized, "error.invalid_credentials")
		return
	}
	service.limiter.Clear(ip)
	// Only after the password checked out: "this account is deactivated" would
	// otherwise answer anyone who guesses a name, turning the login into a
	// directory of the members. Whoever knows the password already knows the
	// account exists, and telling them beats leaving them to retype a password
	// that was never the problem.
	if user.state != "active" {
		httpx.Error(responseWriter, http.StatusForbidden, "error.account_deactivated")
		return
	}

	if user.totpSecret.Valid && user.totpSecret.String != "" {
		token := randomHex(32)
		service.pending.Set("totp_pending:"+token, itoa(user.id), 300*time.Second)
		httpx.Success(responseWriter, map[string]any{"totp_required": true, "pending_token": token})
		return
	}
	service.sessions.StartForUser(responseWriter, user.id, body.Remember)
	httpx.Success(responseWriter, payload(user))
}

// TotpVerify completes a TOTP-protected login with pending_token + code.
func (service *Auth) TotpVerify(responseWriter http.ResponseWriter, request *http.Request) {
	ip := httpx.ClientIP(request)
	var body loginBody
	_ = httpx.DecodeJSON(request, &body)
	token := strings.TrimSpace(body.PendingToken)
	code := strings.TrimSpace(body.Code)
	if token == "" || code == "" {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.credentials_required")
		return
	}
	if service.limiter.Exceeded(ip) {
		httpx.Error(responseWriter, http.StatusTooManyRequests, "error.too_many_attempts")
		return
	}
	userIDText, ok := service.pending.Get("totp_pending:" + token)
	if !ok {
		service.limiter.Record(ip)
		httpx.Error(responseWriter, http.StatusUnauthorized, "error.totp_expired")
		return
	}
	user, failure := service.loadByID(atoi(userIDText))
	if failure != nil || !user.totpSecret.Valid || user.totpSecret.String == "" {
		httpx.Error(responseWriter, http.StatusUnauthorized, "error.invalid_credentials")
		return
	}
	secret, ok := service.crypto.Decrypt(user.totpSecret.String, user.id)
	if !ok || !service.verifyTotp(secret, code, user.id) {
		service.limiter.Record(ip)
		httpx.Error(responseWriter, http.StatusUnauthorized, "error.totp_invalid")
		return
	}
	service.pending.Del("totp_pending:" + token)
	service.limiter.Clear(ip)
	service.sessions.StartForUser(responseWriter, user.id, body.Remember)
	httpx.Success(responseWriter, payload(user))
}

// TotpSetup creates a new TOTP secret and returns the otpauth URI (not yet active).
func (service *Auth) TotpSetup(responseWriter http.ResponseWriter, request *http.Request) {
	user, ok := service.currentUser(responseWriter, request)
	if !ok {
		return
	}
	secret := generateTotpSecret()
	rawName := user.name
	if user.email.Valid && user.email.String != "" {
		rawName = user.email.String
	}
	label := rawurlencode("MeshDepot:" + rawName)
	uri := "otpauth://totp/" + label + "?secret=" + secret + "&issuer=MeshDepot&algorithm=SHA1&digits=6&period=30"
	service.setup.Set("totp_setup:"+itoa(user.id), secret, 600*time.Second)
	httpx.Success(responseWriter, map[string]any{"secret": secret, "uri": uri})
}

// TotpEnable confirms the setup with the first code and activates 2FA.
func (service *Auth) TotpEnable(responseWriter http.ResponseWriter, request *http.Request) {
	user, ok := service.currentUser(responseWriter, request)
	if !ok {
		return
	}
	var body loginBody
	_ = httpx.DecodeJSON(request, &body)
	code := strings.TrimSpace(body.Code)
	secret, ok := service.setup.Get("totp_setup:" + itoa(user.id))
	if !ok {
		httpx.Error(responseWriter, http.StatusBadRequest, "error.totp_setup_expired")
		return
	}
	if !service.verifyTotp(secret, code, user.id) {
		httpx.Error(responseWriter, http.StatusBadRequest, "error.totp_invalid")
		return
	}
	encrypted, failure := service.crypto.Encrypt(secret, user.id)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if _, failure := service.db.Exec("UPDATE users SET totp_secret = ? WHERE id = ?", encrypted, user.id); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	service.setup.Del("totp_setup:" + itoa(user.id))
	httpx.SuccessMessage(responseWriter, nil, "Two-factor authentication enabled")
}

// TotpDisable disables 2FA after password confirmation.
func (service *Auth) TotpDisable(responseWriter http.ResponseWriter, request *http.Request) {
	user, ok := service.currentUser(responseWriter, request)
	if !ok {
		return
	}
	ip := httpx.ClientIP(request)
	if service.limiter.Exceeded(ip) {
		httpx.Error(responseWriter, http.StatusTooManyRequests, "error.too_many_attempts")
		return
	}
	var body loginBody
	_ = httpx.DecodeJSON(request, &body)
	if !VerifyPassword(user.hash, body.Password) {
		service.limiter.Record(ip)
		httpx.Error(responseWriter, http.StatusUnauthorized, "error.invalid_credentials")
		return
	}
	service.limiter.Clear(ip)
	if _, failure := service.db.Exec("UPDATE users SET totp_secret = NULL WHERE id = ?", user.id); failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	httpx.SuccessMessage(responseWriter, nil, "Two-factor authentication disabled")
}

func (service *Auth) Logout(responseWriter http.ResponseWriter, request *http.Request) {
	service.sessions.Destroy(responseWriter, request)
	httpx.SuccessMessage(responseWriter, nil, "Logged out")
}

// Me returns the profile of the current session user.
func (service *Auth) Me(responseWriter http.ResponseWriter, request *http.Request) {
	user, ok := service.currentUser(responseWriter, request)
	if !ok {
		return
	}
	httpx.Success(responseWriter, payload(user))
}

type ctxKey int

const (
	userIDKey ctxKey = iota
	// publicIDKey carries the account's outward identifier alongside the numeric
	// one. Both come from the query the middleware already runs, so a handler can
	// compare a path parameter or build a storage path without a second lookup.
	publicIDKey
)

// currentUser loads the session user or writes 401 and returns ok=false.
func (service *Auth) currentUser(responseWriter http.ResponseWriter, request *http.Request) (userRow, bool) {
	id, ok := service.sessions.UserID(request)
	if !ok {
		httpx.Error(responseWriter, http.StatusUnauthorized, "error.unauthorized")
		return userRow{}, false
	}
	user, failure := service.loadByID(id)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusUnauthorized, "error.unauthorized")
		return userRow{}, false
	}
	return user, true
}

// Require is middleware that enforces a valid session and puts the user ID into
// the request context.
func (service *Auth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		id, ok := service.sessions.UserID(request)
		if !ok {
			httpx.Error(responseWriter, http.StatusUnauthorized, "error.unauthorized")
			return
		}
		// Re-check the account state on every request so a deactivated (or
		// deleted) user cannot keep using a session that was already
		// established. Without this a valid cookie outlives the account.
		var state, publicID string
		var mustChange int
		if failure := service.db.QueryRow("SELECT state, COALESCE(public_id, ''), must_change_password FROM users WHERE id = ?", id).
			Scan(&state, &publicID, &mustChange); failure != nil || state != "active" {
			httpx.Error(responseWriter, http.StatusUnauthorized, "error.unauthorized")
			return
		}
		if !passwordChangeAllowed(request, mustChange) {
			httpx.Error(responseWriter, http.StatusForbidden, "error.password_change_required")
			return
		}
		next.ServeHTTP(responseWriter, withIdentity(request, id, publicID))
	})
}

// RequireAdmin enforces a session with admin rights.
func (service *Auth) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		id, ok := service.sessions.UserID(request)
		if !ok {
			httpx.Error(responseWriter, http.StatusUnauthorized, "error.unauthorized")
			return
		}
		var admin, mustChange int
		var publicID string
		failure := service.db.QueryRow("SELECT admin, COALESCE(public_id, ''), must_change_password FROM users WHERE id = ? AND state = 'active'", id).
			Scan(&admin, &publicID, &mustChange)
		if failure != nil || admin != 1 {
			httpx.Error(responseWriter, http.StatusForbidden, "error.forbidden")
			return
		}
		if !passwordChangeAllowed(request, mustChange) {
			httpx.Error(responseWriter, http.StatusForbidden, "error.password_change_required")
			return
		}
		next.ServeHTTP(responseWriter, withIdentity(request, id, publicID))
	})
}

// passwordChangeAllowed reports whether a request may proceed while the account
// still owes a password change.
//
// The blocking modal in the frontend only covers the screen - the session
// itself is valid, so every endpoint stayed usable for anyone who closed the
// modal or skipped the UI altogether. A forced password change now holds at the
// door: nothing but setting the new password gets through, and the seeded
// admin/admin credentials are worth exactly one request.
func passwordChangeAllowed(request *http.Request, mustChange int) bool {
	if mustChange != 1 {
		return true
	}
	return strings.HasSuffix(request.URL.Path, "/force-password")
}

// withIdentity puts both identities of the session user into the request
// context.
func withIdentity(request *http.Request, id int, publicID string) *http.Request {
	ctx := context.WithValue(request.Context(), userIDKey, id)
	return request.WithContext(context.WithValue(ctx, publicIDKey, publicID))
}

// PublicIDFromContext returns the outward user identifier set by the
// middleware.
func PublicIDFromContext(ctx context.Context) (string, bool) {
	publicID, ok := ctx.Value(publicIDKey).(string)
	return publicID, ok && publicID != ""
}

// UserIDFromContext returns the user ID set by the middleware.
func UserIDFromContext(ctx context.Context) (int, bool) {
	id, ok := ctx.Value(userIDKey).(int)
	return id, ok
}

func randomHex(length int) string {
	randomBytes := make([]byte, length)
	_, _ = rand.Read(randomBytes)
	return hex.EncodeToString(randomBytes)
}

// rawurlencode percent-encodes per RFC 3986: unlike url.QueryEscape a space
// becomes %20, not '+'.
func rawurlencode(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func itoa(value int) string { return strconv.Itoa(value) }

func atoi(text string) int {
	number, _ := strconv.Atoi(text)
	return number
}
