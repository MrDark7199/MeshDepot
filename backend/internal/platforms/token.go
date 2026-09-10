package platforms

import (
	"database/sql"

	"meshdepot/internal/dbutil"

	"meshdepot/internal/logx"
)

// platformAccount holds the decrypted contents of one platform_accounts row.
// Absent, empty or undecryptable fields come back as "".
type platformAccount struct {
	Token    string
	Username string // login e-mail for most platforms
	Password string
	TOTP     string
	Expires  string // token_expires_at as stored (SQLite datetime text)
}

// loadAccount reads and decrypts a user's account row. The username falls back
// to the raw column when decryption fails, since rows written before the field
// was encrypted still hold plaintext; the secrets get no such fallback, where a
// ciphertext used as a password would only produce a confusing login failure.
func (deps Deps) loadAccount(userID int, platform string) platformAccount {
	var token, username, password, totp, expires sql.NullString
	_ = deps.DB.QueryRow(
		`SELECT token, username, password_encrypted, totp_secret, token_expires_at
		 FROM platform_accounts WHERE user_id = ? AND platform = ? LIMIT 1`,
		userID, platform,
	).Scan(&token, &username, &password, &totp, &expires)

	account := platformAccount{
		Token:    deps.decryptSecret(token, userID, platform, "token"),
		Password: deps.decryptSecret(password, userID, platform, "password"),
		TOTP:     deps.decryptSecret(totp, userID, platform, "TOTP secret"),
		Expires:  expires.String,
	}
	// The one field with a plaintext fallback, so a failed decrypt stays quiet.
	if account.Username = deps.decryptField(username, userID); account.Username == "" {
		account.Username = username.String
	}
	return account
}

func (deps Deps) decryptField(column sql.NullString, userID int) string {
	if !column.Valid || column.String == "" {
		return ""
	}
	if decrypted, ok := deps.Crypto.Decrypt(column.String, userID); ok {
		return decrypted
	}
	return ""
}

// decryptSecret is decryptField for the fields an auto-login depends on, and says
// so when one cannot be read. Otherwise the failure is invisible: the value comes
// back as "", hasLogin reports no credentials, and the download fails with "add
// your credentials" for an account that has them. The usual cause is a changed
// APP_KEY, and nothing here can recover the row - but the log points at the key.
func (deps Deps) decryptSecret(column sql.NullString, userID int, platform, field string) string {
	if !column.Valid || column.String == "" {
		return ""
	}
	if decrypted, ok := deps.Crypto.Decrypt(column.String, userID); ok {
		return decrypted
	}
	logx.Errorf("[platforms] %s: the stored %s of user %d cannot be decrypted - APP_KEY most likely differs from "+
		"the one it was saved with. The account now reads as if nothing had been entered; re-enter the credentials to fix it.",
		platform, field, userID)
	return ""
}

// hasLogin reports whether the credentials are usable for an auto-login. "***"
// is the placeholder the frontend sends for an unchanged password.
func (account platformAccount) hasLogin() bool {
	return account.Username != "" && account.Password != "" && account.Password != "***"
}

// resolveToken returns the stored token while it is valid, otherwise a fresh one
// from the platform's auto-login, persisted with the lifetime from the platform
// table. progress may be nil, as the library sync has no SSE channel.
func (deps Deps) resolveToken(userID int, platform string, progress func(step, label string, current, total int)) (token string, loginFailed bool) {
	return deps.refreshToken(deps.loadAccount(userID, platform), userID, platform, progress)
}

// refreshToken is resolveToken for callers that already hold the account row.
// loginFailed separates "no credentials stored" from "the login was rejected", so
// callers can say which to fix; the previous token is returned unchanged then,
// since it is still the best guess and anonymous platforms stay readable.
func (deps Deps) refreshToken(
	account platformAccount,
	userID int,
	platform string,
	progress func(step, label string, current, total int),
) (token string, loginFailed bool) {
	expired := (account.Expires != "" && tokenExpired(account.Expires)) || jwtExpired(account.Token)
	if account.Token != "" && !expired {
		return account.Token, false
	}
	entry, known := ByName(platform)
	if !known || entry.autoLogin == nil || !account.hasLogin() {
		return account.Token, false
	}
	if progress != nil {
		progress("authenticating", "", 0, 0)
	}
	newToken := entry.autoLogin(deps, Credentials{Email: account.Username, Password: account.Password, TOTP: account.TOTP})
	if newToken == "" {
		return account.Token, true
	}
	if entry.TokenExpiry != "" {
		deps.persistToken(userID, platform, newToken, entry.TokenExpiry)
	}
	return newToken, false
}

// persistToken stores a refreshed token encrypted, with an expiry relative to now
// (a SQLite date modifier such as "+28 days").
func (deps Deps) persistToken(userID int, platform, token, expiresExpr string) {
	if encrypted, failure := deps.Crypto.Encrypt(token, userID); failure == nil {
		dbutil.ExecLogged(deps.DB,
			"UPDATE platform_accounts SET token = ?, token_expires_at = datetime('now', ?), updated_at = CURRENT_TIMESTAMP WHERE user_id = ? AND platform = ?",
			encrypted, expiresExpr, userID, platform)
	}
}

// forceLogin fetches and persists a fresh token whatever the stored one looks
// like, for the 401 retry path where the platform rejected an unexpired token.
func (deps Deps) forceLogin(userID int, platform string) string {
	account := deps.loadAccount(userID, platform)
	account.Token, account.Expires = "", ""
	token, _ := deps.refreshToken(account, userID, platform, nil)
	return token
}
