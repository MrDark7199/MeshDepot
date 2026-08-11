package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"meshdepot/internal/dbutil"
	"net/http"
	"sync"
	"time"

	"meshdepot/internal/safego"
)

// sessionCookie is the cookie name (compatible with the previous PHP session).
const sessionCookie = "PHPSESSID"

// Lifetimes: persistent sessions ("stay logged in") last a long time and slide
// on activity; temporary sessions are a browser session cookie with a short
// idle timeout and disappear when the browser closes.
const (
	lifetimePersistent = 365 * 24 * time.Hour
	lifetimeTemporary  = 24 * time.Hour
	// persistThreshold limits DB writes during the sliding renewal: the expiry
	// is only written forward if it would move by more than this amount (instead
	// of on every request).
	persistThreshold = 5 * time.Minute
)

// sessionData holds the state of a session (also as an in-memory DB cache).
type sessionData struct {
	userID     int
	expires    time.Time
	persistent bool
}

// SessionManager stores sessions in the SQLite DB and thus survives restarts.
// An in-memory map serves as a cache in front of the DB.
type SessionManager struct {
	mutex    sync.Mutex
	db       *sql.DB
	sessions map[string]sessionData
	secure   bool
}

// NewSessionManager creates a session manager. secure=true sets the Secure flag
// on the cookie (for HTTPS operation).
func NewSessionManager(db *sql.DB, secure bool) *SessionManager {
	manager := &SessionManager{db: db, sessions: make(map[string]sessionData), secure: secure}
	safego.Go("session-sweeper", manager.sweep)
	return manager
}

// newID generates a random 32-byte session ID (hex).
func newID() string {
	randomBytes := make([]byte, 32)
	_, _ = rand.Read(randomBytes)
	return hex.EncodeToString(randomBytes)
}

// StartForUser creates a fresh session for userID, persists it and sets the
// cookie. remember=true → long-lived, persistent session ("stay logged in");
// otherwise a short-lived browser session cookie.
func (manager *SessionManager) StartForUser(responseWriter http.ResponseWriter, userID int, remember bool) {
	id := newID()
	lifetime := lifetimeTemporary
	if remember {
		lifetime = lifetimePersistent
	}
	expiresAt := time.Now().Add(lifetime)

	manager.mutex.Lock()
	manager.sessions[id] = sessionData{userID: userID, expires: expiresAt, persistent: remember}
	manager.mutex.Unlock()
	dbutil.ExecLogged(manager.db,
		"INSERT INTO sessions (id, user_id, persistent, expires_at) VALUES (?, ?, ?, ?)",
		id, userID, boolInt(remember), formatDBTime(expiresAt),
	)

	cookie := &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   manager.secure,
		SameSite: http.SameSiteStrictMode,
	}
	// Only persistent sessions get a Max-Age - temporary ones remain a session
	// cookie that expires when the browser closes.
	if remember {
		cookie.MaxAge = int(lifetime.Seconds())
	}
	http.SetCookie(responseWriter, cookie)
}

// UserID returns the user ID of the current session (or ok=false). On a cache
// miss (e.g. after a restart) it is reloaded from the DB.
func (manager *SessionManager) UserID(request *http.Request) (int, bool) {
	cookie, failure := request.Cookie(sessionCookie)
	if failure != nil {
		return 0, false
	}
	id := cookie.Value

	manager.mutex.Lock()
	session, ok := manager.sessions[id]
	manager.mutex.Unlock()
	if !ok {
		session, ok = manager.loadFromDB(id)
		if !ok {
			return 0, false
		}
		manager.mutex.Lock()
		manager.sessions[id] = session
		manager.mutex.Unlock()
	}

	if time.Now().After(session.expires) {
		manager.remove(id)
		return 0, false
	}

	// Sliding renewal - write the DB only on meaningful progress.
	lifetime := lifetimeTemporary
	if session.persistent {
		lifetime = lifetimePersistent
	}
	newExpiry := time.Now().Add(lifetime)
	if newExpiry.Sub(session.expires) > persistThreshold {
		session.expires = newExpiry
		manager.mutex.Lock()
		manager.sessions[id] = session
		manager.mutex.Unlock()
		dbutil.ExecLogged(manager.db, "UPDATE sessions SET expires_at=? WHERE id=?", formatDBTime(newExpiry), id)
	}
	return session.userID, true
}

// loadFromDB loads a session from the DB (cache-miss path).
func (manager *SessionManager) loadFromDB(id string) (sessionData, bool) {
	var userID, persistent int
	var expiresRaw string
	if failure := manager.db.QueryRow(
		"SELECT user_id, persistent, expires_at FROM sessions WHERE id=?", id,
	).Scan(&userID, &persistent, &expiresRaw); failure != nil {
		return sessionData{}, false
	}
	expiresAt, ok := parseDBTime(expiresRaw)
	if !ok {
		return sessionData{}, false
	}
	return sessionData{userID: userID, expires: expiresAt, persistent: persistent == 1}, true
}

// remove deletes a session from cache and DB.
func (manager *SessionManager) remove(id string) {
	manager.mutex.Lock()
	delete(manager.sessions, id)
	manager.mutex.Unlock()
	dbutil.ExecLogged(manager.db, "DELETE FROM sessions WHERE id=?", id)
}

// Destroy ends the current session and clears the cookie.
func (manager *SessionManager) Destroy(responseWriter http.ResponseWriter, request *http.Request) {
	if cookie, failure := request.Cookie(sessionCookie); failure == nil {
		manager.remove(cookie.Value)
	}
	http.SetCookie(responseWriter, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: manager.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

// DeleteAllForUser invalidates every session of a user, in both the cache and
// the DB. Used when an account is deactivated, deleted, or has its password
// reset/changed, so no previously established session survives the change.
func (manager *SessionManager) DeleteAllForUser(userID int) {
	manager.mutex.Lock()
	for id, session := range manager.sessions {
		if session.userID == userID {
			delete(manager.sessions, id)
		}
	}
	manager.mutex.Unlock()
	dbutil.ExecLogged(manager.db, "DELETE FROM sessions WHERE user_id = ?", userID)
}

// sweep periodically removes expired sessions from DB and cache.
func (manager *SessionManager) sweep() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		dbutil.ExecLogged(manager.db, "DELETE FROM sessions WHERE expires_at < ?", formatDBTime(now))
		manager.mutex.Lock()
		for id, session := range manager.sessions {
			if now.After(session.expires) {
				delete(manager.sessions, id)
			}
		}
		manager.mutex.Unlock()
	}
}

// boolInt converts a bool to 0/1 for storage in SQLite.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// sqliteTimeLayout is the format every other time column in the schema uses
// (CURRENT_TIMESTAMP). sessions.expires_at used to be written as RFC3339, so a
// datetime() comparison across both formats silently produced wrong results -
// it only worked because sweep() formatted the same way.
const sqliteTimeLayout = "2006-01-02 15:04:05"

// formatDBTime renders a timestamp in UTC in the schema's format.
func formatDBTime(moment time.Time) string {
	return moment.UTC().Format(sqliteTimeLayout)
}

// parseDBTime reads a timestamp column. RFC3339 is still accepted so rows
// written before the format change stay readable.
func parseDBTime(raw string) (time.Time, bool) {
	if moment, failure := time.Parse(sqliteTimeLayout, raw); failure == nil {
		return moment.UTC(), true
	}
	if moment, failure := time.Parse(time.RFC3339, raw); failure == nil {
		return moment.UTC(), true
	}
	return time.Time{}, false
}
