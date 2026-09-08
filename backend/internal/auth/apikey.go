package auth

// API keys: authentication for clients that cannot carry a session cookie.
//
// The browser extension is the reason this exists. Its requests to MeshDepot are
// cross-site, and the session cookie is SameSite=Strict, so it is not sent even
// when the member is signed in in the next tab. A key of its own is the honest
// answer; borrowing the session would mean weakening the cookie for everybody.
//
// Only the hash of a key is stored. The plaintext is shown once, at creation,
// and cannot be recovered afterwards - a key the server can show again is one it
// can also lose.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"

	"meshdepot/internal/httpx"
)

// KeyPrefix marks a MeshDepot key, so one found in a configuration file is
// recognisable for what it is.
const KeyPrefix = "mdp_"

// NewAPIKey returns a fresh key in plaintext. 32 random bytes: far beyond
// guessing, which is also why the stored hash needs no salt or key stretching -
// there is no low-entropy secret here to protect against a dictionary.
func NewAPIKey() (string, error) {
	raw := make([]byte, 32)
	if _, failure := rand.Read(raw); failure != nil {
		return "", failure
	}
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashAPIKey is the value stored in the database.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}

// APIKeyPreview is the recognisable head of a key, for telling two apart in a
// list. Short enough to be useless on its own.
func APIKeyPreview(key string) string {
	trimmed := strings.TrimSpace(key)
	if len(trimmed) <= len(KeyPrefix)+6 {
		return trimmed
	}
	return trimmed[:len(KeyPrefix)+6]
}

// bearerKey reads the key out of an Authorization header.
func bearerKey(request *http.Request) string {
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	const scheme = "Bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(header[len(scheme):])
}

// UserForAPIKey resolves a presented key to an active account, or 0.
//
// The lookup is by hash, and the hash is compared again in constant time. The
// index makes the first comparison a database matter and therefore timing-shaped;
// the second one is not, and costs nothing.
func UserForAPIKey(database *sql.DB, presented string) (userID int, publicID string) {
	presented = strings.TrimSpace(presented)
	if presented == "" || !strings.HasPrefix(presented, KeyPrefix) {
		return 0, ""
	}
	hash := HashAPIKey(presented)

	var identifier, owner int
	var storedHash, storedPublicID, state string
	// The expiry is compared in the query so a key that has run out is simply not
	// found - there is no state in which an expired key is "known but refused",
	// and nothing later can forget to check.
	failure := database.QueryRow(`
		SELECT k.id, k.user_id, k.key_hash, COALESCE(u.public_id, ''), u.state
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = ? AND k.revoked_at IS NULL
		  AND (k.expires_at IS NULL OR k.expires_at > CURRENT_TIMESTAMP) LIMIT 1`, hash).
		Scan(&identifier, &owner, &storedHash, &storedPublicID, &state)
	if failure != nil || state != "active" {
		return 0, ""
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hash)) != 1 {
		return 0, ""
	}
	// Recorded so a forgotten key can be recognised as still in use before
	// somebody revokes it and breaks something.
	_, _ = database.Exec("UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?", identifier)
	return owner, storedPublicID
}

// RequireAPIKey authenticates by API key alone and hands on the same identity
// the session middleware sets, so handlers behind it are written no differently.
//
// Session cookies are deliberately not accepted here: a route reachable with an
// ambient cookie is one a foreign page can trigger in a signed-in member's
// browser, and this route imports files.
func (service *Auth) RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		// The key rides in a header, so a plaintext connection hands it to
		// everyone on the way. Refused rather than accepted quietly, because a
		// key that has crossed the open internet in the clear should be treated
		// as spent - and nobody would know it had.
		//
		// The exception is a caller on this machine or this network, where a
		// self-hosted instance on plain HTTP is the ordinary arrangement and
		// there is no stretch of public wire to listen on.
		if !httpx.IsSecureConnection(request) && !httpx.IsLocalClient(request) {
			httpx.Error(responseWriter, http.StatusForbidden, "error.insecure_connection")
			return
		}

		userID, publicID := UserForAPIKey(service.db, bearerKey(request))
		if userID == 0 {
			httpx.Error(responseWriter, http.StatusUnauthorized, "error.unauthorized")
			return
		}
		next.ServeHTTP(responseWriter, withIdentity(request, userID, publicID))
	})
}
