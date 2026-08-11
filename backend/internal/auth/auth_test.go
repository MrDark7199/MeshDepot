package auth

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginSucceedsWithEmailAndName(t *testing.T) {
	service, database := newTestAuth(t)
	insertUser(t, database, "user@example.org", "ada", "secret-password")

	for _, identifier := range []string{"user@example.org", "ada"} {
		recorder, request := postJSON(t, `{"email":"`+identifier+`","password":"secret-password"}`)
		service.Login(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("login with %q returned %d: %s", identifier, recorder.Code, recorder.Body.String())
		}
		data := dataOf(t, recorder)
		if data["name"] != "ada" || data["email"] != "user@example.org" {
			t.Fatalf("unexpected payload for %q: %v", identifier, data)
		}
		sessionCookieOf(t, recorder)
	}
}

// The identifier is compared case-insensitively, the password is not.
func TestLoginIdentifierIsCaseInsensitive(t *testing.T) {
	service, database := newTestAuth(t)
	insertUser(t, database, "User@Example.org", "Ada", "secret-password")

	recorder, request := postJSON(t, `{"email":"user@example.ORG","password":"secret-password"}`)
	service.Login(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("case-insensitive identifier was rejected: %d", recorder.Code)
	}

	recorder, request = postJSON(t, `{"email":"User@Example.org","password":"SECRET-PASSWORD"}`)
	service.Login(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("a password with wrong case was accepted: %d", recorder.Code)
	}
}

func TestLoginRejectsMissingFields(t *testing.T) {
	service, database := newTestAuth(t)
	insertUser(t, database, "user@example.org", "ada", "secret-password")

	for _, body := range []string{
		`{"email":"","password":"secret-password"}`,
		`{"email":"user@example.org","password":""}`,
		`{"email":"   ","password":"secret-password"}`,
		`{}`,
	} {
		recorder, request := postJSON(t, body)
		service.Login(recorder, request)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body %s returned %d instead of 422", body, recorder.Code)
		}
		if key := errorKeyOf(t, recorder); key != "error.credentials_required" {
			t.Fatalf("body %s returned %q", body, key)
		}
	}
}

// Wrong password and unknown account must be indistinguishable from outside.
func TestLoginUnknownUserAndWrongPasswordLookIdentical(t *testing.T) {
	service, database := newTestAuth(t)
	insertUser(t, database, "user@example.org", "ada", "secret-password")

	wrongPassword, request := postJSON(t, `{"email":"user@example.org","password":"wrong"}`)
	service.Login(wrongPassword, request)
	unknownUser, request := postJSON(t, `{"email":"nobody@example.org","password":"wrong"}`)
	service.Login(unknownUser, request)

	if wrongPassword.Code != http.StatusUnauthorized || unknownUser.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 twice, got %d and %d", wrongPassword.Code, unknownUser.Code)
	}
	if wrongPassword.Body.String() != unknownUser.Body.String() {
		t.Fatalf("the two responses differ: %s vs %s", wrongPassword.Body, unknownUser.Body)
	}
}

// A deactivated account is refused with its own reason rather than with "wrong
// password": whoever gets the password right is not making a typo, and leaving
// them to retype it explains nothing.
func TestLoginRejectsInactiveUser(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	database.Exec("UPDATE users SET state = 'inactive' WHERE id = ?", userID)

	recorder, request := postJSON(t, `{"email":"user@example.org","password":"secret-password"}`)
	service.Login(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("an inactive user could log in: %d", recorder.Code)
	}
	if key := errorKeyOf(t, recorder); key != "error.account_deactivated" {
		t.Fatalf("the reason was not reported: %q", key)
	}
}

// The reason may only follow a correct password. Otherwise anyone who guesses a
// name learns whether the account exists, which turns the login into a
// directory of the members.
func TestLoginHidesDeactivationBehindTheWrongPassword(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	database.Exec("UPDATE users SET state = 'inactive' WHERE id = ?", userID)

	recorder, request := postJSON(t, `{"email":"user@example.org","password":"not-the-password"}`)
	service.Login(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong password answered %d", recorder.Code)
	}
	if key := errorKeyOf(t, recorder); key != "error.invalid_credentials" {
		t.Fatalf("the deactivation leaked to a wrong password: %q", key)
	}
}

func TestLoginBlocksAfterTooManyFailures(t *testing.T) {
	service, database := newTestAuth(t)
	insertUser(t, database, "user@example.org", "ada", "secret-password")

	for attempt := 0; attempt < rateLimitMax; attempt++ {
		recorder, request := postJSON(t, `{"email":"user@example.org","password":"wrong"}`)
		service.Login(recorder, request)
	}
	recorder, request := postJSON(t, `{"email":"user@example.org","password":"secret-password"}`)
	service.Login(recorder, request)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after %d failures, got %d", rateLimitMax, recorder.Code)
	}
	if key := errorKeyOf(t, recorder); key != "error.too_many_attempts" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// A successful login clears the counter, so a user who mistypes a few times and
// then succeeds is not left one attempt away from a lockout.
func TestSuccessfulLoginClearsRateLimit(t *testing.T) {
	service, database := newTestAuth(t)
	insertUser(t, database, "user@example.org", "ada", "secret-password")

	for attempt := 0; attempt < rateLimitMax-1; attempt++ {
		recorder, request := postJSON(t, `{"email":"user@example.org","password":"wrong"}`)
		service.Login(recorder, request)
	}
	loginAndGetSession(t, service, "user@example.org", "secret-password")

	for attempt := 0; attempt < rateLimitMax-1; attempt++ {
		recorder, request := postJSON(t, `{"email":"user@example.org","password":"wrong"}`)
		service.Login(recorder, request)
		if recorder.Code == http.StatusTooManyRequests {
			t.Fatalf("locked out after %d attempts, so the counter was not cleared", attempt+1)
		}
	}
}

func TestLoginWithTotpReturnsPendingTokenInsteadOfSession(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	enableTotp(t, service, database, userID)

	recorder, request := postJSON(t, `{"email":"user@example.org","password":"secret-password"}`)
	service.Login(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("login returned %d: %s", recorder.Code, recorder.Body.String())
	}
	data := dataOf(t, recorder)
	if data["totp_required"] != true {
		t.Fatalf("totp_required missing: %v", data)
	}
	token, isString := data["pending_token"].(string)
	if !isString || len(token) != 64 {
		t.Fatalf("unexpected pending token %v", data["pending_token"])
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookie {
			t.Fatal("a session cookie was set although the second factor is still missing")
		}
	}
}

func TestTotpVerifyCompletesLogin(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	secret := enableTotp(t, service, database, userID)
	token := pendingTokenFor(t, service, "user@example.org", "secret-password")

	recorder, request := postJSON(t, `{"pending_token":"`+token+`","code":"`+totpCode(secret, 0)+`"}`)
	service.TotpVerify(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("verify returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if data := dataOf(t, recorder); data["totp_enabled"] != true {
		t.Fatalf("payload does not report 2FA as enabled: %v", data)
	}
	sessionID := sessionCookieOf(t, recorder).Value
	if resolved, ok := service.sessions.UserID(requestWithSession(sessionID)); !ok || resolved != userID {
		t.Fatalf("the new session does not resolve to user %d", userID)
	}
}

// The pending token is single use, so a captured token cannot be replayed.
func TestTotpVerifyConsumesPendingToken(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	secret := enableTotp(t, service, database, userID)
	token := pendingTokenFor(t, service, "user@example.org", "secret-password")

	recorder, request := postJSON(t, `{"pending_token":"`+token+`","code":"`+totpCode(secret, 0)+`"}`)
	service.TotpVerify(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("the first verify failed: %d", recorder.Code)
	}

	recorder, request = postJSON(t, `{"pending_token":"`+token+`","code":"`+totpCode(secret, 1)+`"}`)
	service.TotpVerify(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("the token was accepted twice: %d", recorder.Code)
	}
	if key := errorKeyOf(t, recorder); key != "error.totp_expired" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestTotpVerifyRejectsWrongCode(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	secret := enableTotp(t, service, database, userID)
	token := pendingTokenFor(t, service, "user@example.org", "secret-password")

	wrongCode := totpCode(secret, 500)
	recorder, request := postJSON(t, `{"pending_token":"`+token+`","code":"`+wrongCode+`"}`)
	service.TotpVerify(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong code was accepted: %d", recorder.Code)
	}
	if key := errorKeyOf(t, recorder); key != "error.totp_invalid" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestTotpVerifyRejectsUnknownToken(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	enableTotp(t, service, database, userID)

	recorder, request := postJSON(t, `{"pending_token":"does-not-exist","code":"123456"}`)
	service.TotpVerify(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("an unknown token was accepted: %d", recorder.Code)
	}
}

func TestTotpVerifyRejectsMissingFields(t *testing.T) {
	service, _ := newTestAuth(t)
	for _, body := range []string{`{"pending_token":"","code":"123456"}`, `{"pending_token":"abc","code":""}`, `{}`} {
		recorder, request := postJSON(t, body)
		service.TotpVerify(recorder, request)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body %s returned %d instead of 422", body, recorder.Code)
		}
	}
}

func TestTotpVerifyBlocksAfterTooManyFailures(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	enableTotp(t, service, database, userID)

	for attempt := 0; attempt < rateLimitMax; attempt++ {
		recorder, request := postJSON(t, `{"pending_token":"nope","code":"123456"}`)
		service.TotpVerify(recorder, request)
	}
	recorder, request := postJSON(t, `{"pending_token":"nope","code":"123456"}`)
	service.TotpVerify(recorder, request)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", recorder.Code)
	}
}

// The pending token survives a change of the user's TOTP state only as long as a
// secret is still configured.
func TestTotpVerifyRejectsUserWithoutSecret(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	enableTotp(t, service, database, userID)
	token := pendingTokenFor(t, service, "user@example.org", "secret-password")
	database.Exec("UPDATE users SET totp_secret = NULL WHERE id = ?", userID)

	recorder, request := postJSON(t, `{"pending_token":"`+token+`","code":"123456"}`)
	service.TotpVerify(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
	if key := errorKeyOf(t, recorder); key != "error.invalid_credentials" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestTotpSetupReturnsSecretAndURI(t *testing.T) {
	service, database := newTestAuth(t)
	insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")

	recorder := httptest.NewRecorder()
	service.TotpSetup(recorder, requestWithSession(sessionID))

	if recorder.Code != http.StatusOK {
		t.Fatalf("setup returned %d: %s", recorder.Code, recorder.Body.String())
	}
	data := dataOf(t, recorder)
	secret, isString := data["secret"].(string)
	if !isString || len(secret) != 20 {
		t.Fatalf("unexpected secret %v", data["secret"])
	}
	uri, isString := data["uri"].(string)
	if !isString || !strings.HasPrefix(uri, "otpauth://totp/MeshDepot%3Auser%40example.org?") {
		t.Fatalf("unexpected uri %q", uri)
	}
	if !strings.Contains(uri, "secret="+secret) || !strings.Contains(uri, "issuer=MeshDepot") {
		t.Fatalf("uri is missing secret or issuer: %q", uri)
	}
}

// Accounts without an e-mail address fall back to the user name in the label.
func TestTotpSetupUsesNameWhenEmailIsMissing(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada lovelace", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")
	database.Exec("UPDATE users SET email = NULL WHERE id = ?", userID)

	recorder := httptest.NewRecorder()
	service.TotpSetup(recorder, requestWithSession(sessionID))

	uri, _ := dataOf(t, recorder)["uri"].(string)
	if !strings.Contains(uri, "MeshDepot%3Aada%20lovelace") {
		t.Fatalf("the name was not used or the space was encoded as +: %q", uri)
	}
}

func TestTotpSetupRequiresSession(t *testing.T) {
	service, _ := newTestAuth(t)
	recorder := httptest.NewRecorder()
	service.TotpSetup(recorder, httptest.NewRequest(http.MethodPost, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

func TestTotpEnableStoresEncryptedSecret(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")

	setupRecorder := httptest.NewRecorder()
	service.TotpSetup(setupRecorder, requestWithSession(sessionID))
	secret, _ := dataOf(t, setupRecorder)["secret"].(string)

	enableRecorder := httptest.NewRecorder()
	enableRequest := requestWithSession(sessionID)
	enableRequest.Body = bodyReader(`{"code":"` + totpCode(secret, 0) + `"}`)
	service.TotpEnable(enableRecorder, enableRequest)

	if enableRecorder.Code != http.StatusOK {
		t.Fatalf("enable returned %d: %s", enableRecorder.Code, enableRecorder.Body.String())
	}
	var stored sql.NullString
	database.QueryRow("SELECT totp_secret FROM users WHERE id = ?", userID).Scan(&stored)
	if !stored.Valid || stored.String == "" {
		t.Fatal("no secret was stored")
	}
	if stored.String == secret {
		t.Fatal("the secret was stored in plain text")
	}
	decrypted, ok := service.crypto.Decrypt(stored.String, userID)
	if !ok || decrypted != secret {
		t.Fatalf("the stored secret does not decrypt back: %q/%v", decrypted, ok)
	}
	if _, stillPending := service.setup.Get("totp_setup:" + itoa(userID)); stillPending {
		t.Fatal("the setup entry was not consumed")
	}
}

func TestTotpEnableRejectsWrongCode(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")

	setupRecorder := httptest.NewRecorder()
	service.TotpSetup(setupRecorder, requestWithSession(sessionID))
	secret, _ := dataOf(t, setupRecorder)["secret"].(string)

	recorder := httptest.NewRecorder()
	request := requestWithSession(sessionID)
	request.Body = bodyReader(`{"code":"` + totpCode(secret, 500) + `"}`)
	service.TotpEnable(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
	if key := errorKeyOf(t, recorder); key != "error.totp_invalid" {
		t.Fatalf("unexpected error key %q", key)
	}
	var stored sql.NullString
	database.QueryRow("SELECT totp_secret FROM users WHERE id = ?", userID).Scan(&stored)
	if stored.Valid {
		t.Fatal("a secret was stored despite the wrong code")
	}
}

func TestTotpEnableWithoutSetupFails(t *testing.T) {
	service, database := newTestAuth(t)
	insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")

	recorder := httptest.NewRecorder()
	request := requestWithSession(sessionID)
	request.Body = bodyReader(`{"code":"123456"}`)
	service.TotpEnable(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
	if key := errorKeyOf(t, recorder); key != "error.totp_setup_expired" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestTotpDisableRequiresCorrectPassword(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")
	enableTotp(t, service, database, userID)

	rejected := httptest.NewRecorder()
	wrongPassword := requestWithSession(sessionID)
	wrongPassword.Body = bodyReader(`{"password":"wrong"}`)
	service.TotpDisable(rejected, wrongPassword)
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong password disabled 2FA: %d", rejected.Code)
	}

	accepted := httptest.NewRecorder()
	correctPassword := requestWithSession(sessionID)
	correctPassword.Body = bodyReader(`{"password":"secret-password"}`)
	service.TotpDisable(accepted, correctPassword)
	if accepted.Code != http.StatusOK {
		t.Fatalf("disable returned %d: %s", accepted.Code, accepted.Body.String())
	}

	var stored sql.NullString
	database.QueryRow("SELECT totp_secret FROM users WHERE id = ?", userID).Scan(&stored)
	if stored.Valid {
		t.Fatal("the secret survived TotpDisable")
	}
}

func TestTotpDisableBlocksAfterTooManyFailures(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")
	enableTotp(t, service, database, userID)

	for attempt := 0; attempt <= rateLimitMax; attempt++ {
		recorder := httptest.NewRecorder()
		request := requestWithSession(sessionID)
		request.Body = bodyReader(`{"password":"wrong"}`)
		service.TotpDisable(recorder, request)
		if attempt == rateLimitMax && recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 on attempt %d, got %d", attempt+1, recorder.Code)
		}
	}
}

func TestLogoutEndsSession(t *testing.T) {
	service, database := newTestAuth(t)
	insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")

	recorder := httptest.NewRecorder()
	service.Logout(recorder, requestWithSession(sessionID))

	if recorder.Code != http.StatusOK {
		t.Fatalf("logout returned %d", recorder.Code)
	}
	if _, ok := service.sessions.UserID(requestWithSession(sessionID)); ok {
		t.Fatal("the session survived the logout")
	}
}

func TestMeReturnsProfile(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	database.Exec("UPDATE users SET admin = 1, language = 'de', must_change_password = 1, avatar_path = '/data/avatar.png' WHERE id = ?", userID)
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")

	recorder := httptest.NewRecorder()
	service.Me(recorder, requestWithSession(sessionID))

	data := dataOf(t, recorder)
	if data["admin"] != true || data["must_change_password"] != true {
		t.Fatalf("flags were not mapped to booleans: %v", data)
	}
	if data["language"] != "de" {
		t.Fatalf("unexpected language %v", data["language"])
	}
	// The URL carries the public id, not the numeric one - it is the same
	// identifier every /users route expects.
	var publicID string
	if failure := database.QueryRow("SELECT public_id FROM users WHERE id = ?", userID).Scan(&publicID); failure != nil {
		t.Fatalf("read the public id: %v", failure)
	}
	if data["avatar_url"] != "/api/v1/users/"+publicID+"/avatar" {
		t.Fatalf("unexpected avatar url %v", data["avatar_url"])
	}
	if data["id"] != publicID {
		t.Fatalf("the payload carries the id %v, want the public id", data["id"])
	}
	if data["totp_enabled"] != false {
		t.Fatalf("2FA reported as enabled although no secret is set: %v", data)
	}
}

func TestMeRequiresSession(t *testing.T) {
	service, _ := newTestAuth(t)
	recorder := httptest.NewRecorder()
	service.Me(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

// A session that outlives its user row must not resolve.
func TestCurrentUserRejectsDeletedAccount(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")
	database.Exec("UPDATE users SET state = 'inactive' WHERE id = ?", userID)

	recorder := httptest.NewRecorder()
	service.Me(recorder, requestWithSession(sessionID))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("a deactivated account still answered: %d", recorder.Code)
	}
}

func TestPayloadDefaultsLanguageAndOmitsAvatar(t *testing.T) {
	data := payload(userRow{id: 7, name: "ada"})
	if data["language"] != "en" {
		t.Fatalf("expected the default language en, got %v", data["language"])
	}
	if data["avatar_url"] != nil || data["email"] != nil {
		t.Fatalf("empty fields were not reported as null: %v", data)
	}
	if data["admin"] != false || data["must_change_password"] != false || data["totp_enabled"] != false {
		t.Fatalf("flags default to true: %v", data)
	}
}

func TestRequirePassesUserIDIntoContext(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")

	var seenUserID int
	var seenOK bool
	handler := service.Require(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		seenUserID, seenOK = UserIDFromContext(request.Context())
		responseWriter.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, requestWithSession(sessionID))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("the handler was not reached: %d", recorder.Code)
	}
	if !seenOK || seenUserID != userID {
		t.Fatalf("expected user %d in the context, got %d/%v", userID, seenUserID, seenOK)
	}
}

func TestRequireRejectsWithoutSession(t *testing.T) {
	service, _ := newTestAuth(t)
	handler := service.Require(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		t.Fatal("the handler was reached without a session")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

// The account state is re-read on every request, so deactivating a user takes
// effect immediately instead of when their cookie expires.
func TestRequireRejectsDeactivatedUserWithValidSession(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	sessionID := loginAndGetSession(t, service, "user@example.org", "secret-password")
	database.Exec("UPDATE users SET state = 'inactive' WHERE id = ?", userID)

	handler := service.Require(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		t.Fatal("a deactivated user reached the handler")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, requestWithSession(sessionID))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

func TestRequireAdminAllowsOnlyAdmins(t *testing.T) {
	service, database := newTestAuth(t)
	adminID := insertUser(t, database, "admin@example.org", "admin", "secret-password")
	insertUser(t, database, "user@example.org", "ada", "secret-password")
	database.Exec("UPDATE users SET admin = 1 WHERE id = ?", adminID)

	handler := service.RequireAdmin(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusNoContent)
	}))

	adminSession := loginAndGetSession(t, service, "admin@example.org", "secret-password")
	adminRecorder := httptest.NewRecorder()
	handler.ServeHTTP(adminRecorder, requestWithSession(adminSession))
	if adminRecorder.Code != http.StatusNoContent {
		t.Fatalf("the admin was rejected: %d", adminRecorder.Code)
	}

	userSession := loginAndGetSession(t, service, "user@example.org", "secret-password")
	userRecorder := httptest.NewRecorder()
	handler.ServeHTTP(userRecorder, requestWithSession(userSession))
	if userRecorder.Code != http.StatusForbidden {
		t.Fatalf("a non-admin got through: %d", userRecorder.Code)
	}

	anonymousRecorder := httptest.NewRecorder()
	handler.ServeHTTP(anonymousRecorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if anonymousRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a session, got %d", anonymousRecorder.Code)
	}
}

func TestUserIDFromContextWithoutValue(t *testing.T) {
	if _, ok := UserIDFromContext(context.Background()); ok {
		t.Fatal("an empty context reported a user id")
	}
}

func TestRandomHexLengthAndUniqueness(t *testing.T) {
	first := randomHex(16)
	if len(first) != 32 {
		t.Fatalf("expected 32 characters for 16 bytes, got %d", len(first))
	}
	if first == randomHex(16) {
		t.Fatal("two random values are identical")
	}
}

// url.QueryEscape turns a space into '+', which breaks otpauth labels.
func TestRawurlencodeUsesPercentTwenty(t *testing.T) {
	if encoded := rawurlencode("MeshDepot:ada lovelace"); encoded != "MeshDepot%3Aada%20lovelace" {
		t.Fatalf("unexpected encoding %q", encoded)
	}
}

func TestAtoiFallsBackToZero(t *testing.T) {
	if atoi("42") != 42 {
		t.Fatal("a valid number was not parsed")
	}
	if atoi("not a number") != 0 {
		t.Fatal("an invalid number did not fall back to 0")
	}
}

// enableTotp activates 2FA for a user and returns the plain secret.
func enableTotp(t *testing.T, service *Auth, database *sql.DB, userID int) string {
	t.Helper()
	secret := generateTotpSecret()
	encrypted, failure := service.crypto.Encrypt(secret, userID)
	if failure != nil {
		t.Fatalf("encrypt secret: %v", failure)
	}
	if _, failure := database.Exec("UPDATE users SET totp_secret = ? WHERE id = ?", encrypted, userID); failure != nil {
		t.Fatalf("store secret: %v", failure)
	}
	return secret
}

// pendingTokenFor logs in and returns the pending token of the 2FA step.
func pendingTokenFor(t *testing.T, service *Auth, identifier, password string) string {
	t.Helper()
	recorder, request := postJSON(t, `{"email":"`+identifier+`","password":"`+password+`"}`)
	service.Login(recorder, request)
	token, isString := dataOf(t, recorder)["pending_token"].(string)
	if !isString {
		t.Fatalf("no pending token in the response: %s", recorder.Body.String())
	}
	return token
}

// bodyReader wraps a JSON string as a request body.
func bodyReader(body string) readCloser {
	return readCloser{strings.NewReader(body)}
}

type readCloser struct{ *strings.Reader }

func (readCloser) Close() error { return nil }

// The pending token expires after five minutes; an expired one must not unlock
// the account.
func TestPendingTokenExpires(t *testing.T) {
	service, database := newTestAuth(t)
	userID := insertUser(t, database, "user@example.org", "ada", "secret-password")
	secret := enableTotp(t, service, database, userID)
	token := pendingTokenFor(t, service, "user@example.org", "secret-password")

	service.pending.Set("totp_pending:"+token, itoa(userID), -time.Second)

	recorder, request := postJSON(t, `{"pending_token":"`+token+`","code":"`+totpCode(secret, 0)+`"}`)
	service.TotpVerify(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("an expired pending token was accepted: %d", recorder.Code)
	}
}
