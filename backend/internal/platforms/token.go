package platforms

import (
	"database/sql"

	"meshdepot/internal/dbutil"
)

// platformAccount holds the decrypted contents of one platform_accounts row.
// Fields that are absent, empty or undecryptable come back as "".
type platformAccount struct {
	Token    string
	Username string // login e-mail for most platforms
	Password string
	TOTP     string
	Expires  string // token_expires_at as stored (SQLite datetime text)
}

// loadAccount reads a user's account row for a platform and decrypts it.
//
// The username falls back to the raw column value when decryption fails: rows
// written before the field was encrypted still hold plaintext. Token, password
// and TOTP secret get no such fallback - a ciphertext used as a password would
// only produce a confusing login failure.
func (deps Deps) loadAccount(userID int, platform string) platformAccount {
	var token, username, password, totp, expires sql.NullString
	_ = deps.DB.QueryRow(
		`SELECT token, username, password_encrypted, totp_secret, token_expires_at
		 FROM platform_accounts WHERE user_id = ? AND platform = ? LIMIT 1`,
		userID, platform,
	).Scan(&token, &username, &password, &totp, &expires)

	account := platformAccount{
		Token:    deps.decryptField(token, userID),
		Password: deps.decryptField(password, userID),
		TOTP:     deps.decryptField(totp, userID),
		Expires:  expires.String,
	}
	if account.Username = deps.decryptField(username, userID); account.Username == "" {
		account.Username = username.String
	}
	return account
}

// decryptField decrypts one nullable column, "" when empty or undecryptable.
func (deps Deps) decryptField(column sql.NullString, userID int) string {
	if !column.Valid || column.String == "" {
		return ""
	}
	if decrypted, ok := deps.Crypto.Decrypt(column.String, userID); ok {
		return decrypted
	}
	return ""
}

// hasLogin reports whether the stored credentials are usable for an auto-login.
// "***" is the placeholder the frontend sends back for an unchanged password;
// it must never be tried as one.
func (account platformAccount) hasLogin() bool {
	return account.Username != "" && account.Password != "" && account.Password != "***"
}

// resolveToken returns a usable access token for a platform: the stored one
// while it is still valid, otherwise a fresh one from the platform's auto-login
// (when credentials are on file), persisted encrypted with the lifetime from
// the platform table.
//
// This used to exist four times over - once per downloader plus one in the
// library sync - differing only in the platform constant, the expiry and
// whether the TOTP secret was read. progress may be nil (the library sync has
// no SSE channel).
func (deps Deps) resolveToken(userID int, platform string, progress func(step, label string, current, total int)) (token string, loginFailed bool) {
	return deps.refreshToken(deps.loadAccount(userID, platform), userID, platform, progress)
}

// refreshToken is resolveToken for callers that already hold the account row
// (the library sync also needs the username from it) - it must not be read
// twice.
//
// loginFailed distinguishes "no credentials stored" from "the login was tried
// and rejected", so callers can tell the user which one to fix. The previous
// token is returned unchanged in that case - an expired token is still the best
// available guess, and platforms that read anonymously stay readable.
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

// persistToken stores a refreshed token encrypted, with an expiry relative to
// now (expiresExpr is a SQLite date modifier such as "+28 days").
func (deps Deps) persistToken(userID int, platform, token, expiresExpr string) {
	if encrypted, failure := deps.Crypto.Encrypt(token, userID); failure == nil {
		dbutil.ExecLogged(deps.DB,
			"UPDATE platform_accounts SET token = ?, token_expires_at = datetime('now', ?), updated_at = CURRENT_TIMESTAMP WHERE user_id = ? AND platform = ?",
			encrypted, expiresExpr, userID, platform)
	}
}

// forceLogin obtains a fresh token from the stored credentials regardless of
// whether the stored one still looks valid, and persists it. Used on the 401
// retry path: the platform has rejected a token that had not expired yet.
func (deps Deps) forceLogin(userID int, platform string) string {
	account := deps.loadAccount(userID, platform)
	account.Token, account.Expires = "", ""
	token, _ := deps.refreshToken(account, userID, platform, nil)
	return token
}
