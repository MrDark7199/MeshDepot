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

// sessionCookie keeps the name of the previous PHP session.
const sessionCookie = "PHPSESSID"

// Persistent sessions ("stay logged in") last a long time and slide on activity;
// temporary ones are a browser session cookie with a short idle timeout.
const (
	lifetimePersistent = 365 * 24 * time.Hour
	lifetimeTemporary  = 24 * time.Hour
	// persistThreshold limits DB writes during the sliding renewal: the expiry only
	// moves when it would move by more than this.
	persistThreshold = 5 * time.Minute
)

type sessionData struct {
	userID     int
	expires    time.Time
	persistent bool
}

// SessionManager stores sessions in SQLite and so survives restarts, with an
// in-memory map as a cache in front.
type SessionManager struct {
	mutex    sync.Mutex
	db       *sql.DB
	sessions map[string]sessionData
	secure   bool
}

// NewSessionManager: secure=true sets the Secure flag on the cookie.
func NewSessionManager(db *sql.DB, secure bool) *SessionManager {
	manager := &SessionManager{db: db, sessions: make(map[string]sessionData), secure: secure}
	safego.Go("session-sweeper", manager.sweep)
	return manager
}

func newID() string {
	randomBytes := make([]byte, 32)
	_, _ = rand.Read(randomBytes)
	return hex.EncodeToString(randomBytes)
}

// StartForUser persists a fresh session and sets the cookie. remember=true is a
// long-lived session, otherwise a browser session cookie.
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
	// Only persistent sessions get a Max-Age.
	if remember {
		cookie.MaxAge = int(lifetime.Seconds())
	}
	http.SetCookie(responseWriter, cookie)
}

// UserID reloads from the DB on a cache miss, after a restart for instance.
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

func (manager *SessionManager) remove(id string) {
	manager.mutex.Lock()
	delete(manager.sessions, id)
	manager.mutex.Unlock()
	dbutil.ExecLogged(manager.db, "DELETE FROM sessions WHERE id=?", id)
}

func (manager *SessionManager) Destroy(responseWriter http.ResponseWriter, request *http.Request) {
	if cookie, failure := request.Cookie(sessionCookie); failure == nil {
		manager.remove(cookie.Value)
	}
	http.SetCookie(responseWriter, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: manager.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

// DeleteAllForUser invalidates every session of a user in cache and DB, so none
// survives a deactivation, deletion or password change.
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

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// sqliteTimeLayout is the format every other time column uses.
// sessions.expires_at was written as RFC3339, so a datetime() comparison across
// both formats silently produced wrong results.
const sqliteTimeLayout = "2006-01-02 15:04:05"

func formatDBTime(moment time.Time) string {
	return moment.UTC().Format(sqliteTimeLayout)
}

// parseDBTime still accepts RFC3339, so rows written before the format change
// stay readable.
func parseDBTime(raw string) (time.Time, bool) {
	if moment, failure := time.Parse(sqliteTimeLayout, raw); failure == nil {
		return moment.UTC(), true
	}
	if moment, failure := time.Parse(time.RFC3339, raw); failure == nil {
		return moment.UTC(), true
	}
	return time.Time{}, false
}
