package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"meshdepot/internal/db"
)

func newTestSessionManager(t *testing.T) (*SessionManager, int) {
	t.Helper()
	database, failure := db.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	if failure := db.InitSchema(database); failure != nil {
		t.Fatalf("init schema: %v", failure)
	}
	userID := insertUser(t, database, "session@example.org", "session", "secret-password")
	return NewSessionManager(database, false), userID
}

func TestStartForUserSetsCookieAndResolves(t *testing.T) {
	manager, userID := newTestSessionManager(t)
	recorder := httptest.NewRecorder()

	manager.StartForUser(recorder, userID, false)
	cookie := sessionCookieOf(t, recorder)

	if len(cookie.Value) != 64 {
		t.Fatalf("expected a 64 character session id, got %d", len(cookie.Value))
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie attributes are not hardened: %+v", cookie)
	}
	resolved, ok := manager.UserID(requestWithSession(cookie.Value))
	if !ok || resolved != userID {
		t.Fatalf("expected user %d, got %d/%v", userID, resolved, ok)
	}
}

// A temporary session must stay a browser session cookie (no Max-Age), a
// persistent one must outlive the browser.
func TestStartForUserMaxAgeDependsOnRemember(t *testing.T) {
	manager, userID := newTestSessionManager(t)

	temporary := httptest.NewRecorder()
	manager.StartForUser(temporary, userID, false)
	if maxAge := sessionCookieOf(t, temporary).MaxAge; maxAge != 0 {
		t.Fatalf("temporary session got Max-Age %d", maxAge)
	}

	persistent := httptest.NewRecorder()
	manager.StartForUser(persistent, userID, true)
	if maxAge := sessionCookieOf(t, persistent).MaxAge; maxAge != int(lifetimePersistent.Seconds()) {
		t.Fatalf("persistent session got Max-Age %d", maxAge)
	}
}

func TestStartForUserSetsSecureFlagOnHTTPS(t *testing.T) {
	manager, userID := newTestSessionManager(t)
	manager.secure = true
	recorder := httptest.NewRecorder()

	manager.StartForUser(recorder, userID, false)
	if !sessionCookieOf(t, recorder).Secure {
		t.Fatal("the Secure flag is missing in HTTPS mode")
	}
}

func TestUserIDWithoutCookie(t *testing.T) {
	manager, _ := newTestSessionManager(t)
	if _, ok := manager.UserID(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Fatal("a request without a cookie was accepted")
	}
}

func TestUserIDWithUnknownSession(t *testing.T) {
	manager, _ := newTestSessionManager(t)
	if _, ok := manager.UserID(requestWithSession("0123456789abcdef")); ok {
		t.Fatal("an unknown session id was accepted")
	}
}

// After a restart the in-memory cache is empty; the session must still resolve
// from the sessions table.
func TestUserIDReloadsFromDatabaseAfterCacheMiss(t *testing.T) {
	manager, userID := newTestSessionManager(t)
	recorder := httptest.NewRecorder()
	manager.StartForUser(recorder, userID, true)
	sessionID := sessionCookieOf(t, recorder).Value

	manager.mutex.Lock()
	manager.sessions = make(map[string]sessionData)
	manager.mutex.Unlock()

	resolved, ok := manager.UserID(requestWithSession(sessionID))
	if !ok || resolved != userID {
		t.Fatalf("session did not survive the cache flush: %d/%v", resolved, ok)
	}
	manager.mutex.Lock()
	_, cached := manager.sessions[sessionID]
	manager.mutex.Unlock()
	if !cached {
		t.Fatal("the session was not written back into the cache")
	}
}

func TestUserIDRejectsAndRemovesExpiredSession(t *testing.T) {
	manager, userID := newTestSessionManager(t)
	recorder := httptest.NewRecorder()
	manager.StartForUser(recorder, userID, false)
	sessionID := sessionCookieOf(t, recorder).Value

	manager.mutex.Lock()
	manager.sessions[sessionID] = sessionData{userID: userID, expires: time.Now().Add(-time.Hour)}
	manager.mutex.Unlock()

	if _, ok := manager.UserID(requestWithSession(sessionID)); ok {
		t.Fatal("an expired session was accepted")
	}
	var remaining int
	manager.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE id = ?", sessionID).Scan(&remaining)
	if remaining != 0 {
		t.Fatal("the expired session was not deleted from the database")
	}
}

// The sliding renewal only writes when the expiry moves by more than
// persistThreshold, otherwise every request would hit the single DB connection.
func TestUserIDSlidingRenewalRespectsThreshold(t *testing.T) {
	manager, userID := newTestSessionManager(t)
	recorder := httptest.NewRecorder()
	manager.StartForUser(recorder, userID, false)
	sessionID := sessionCookieOf(t, recorder).Value

	// A marker that the renewal can never produce on its own, so any write to
	// the row is visible even though the real expiry only moves by milliseconds.
	const marker = "2020-01-01 00:00:00"
	manager.db.Exec("UPDATE sessions SET expires_at = ? WHERE id = ?", marker, sessionID)

	manager.UserID(requestWithSession(sessionID))
	var expiryAfterFreshRead string
	manager.db.QueryRow("SELECT expires_at FROM sessions WHERE id = ?", sessionID).Scan(&expiryAfterFreshRead)
	if expiryAfterFreshRead != marker {
		t.Fatal("a session that was just created was already renewed in the database")
	}

	manager.mutex.Lock()
	manager.sessions[sessionID] = sessionData{userID: userID, expires: time.Now().Add(time.Hour)}
	manager.mutex.Unlock()

	manager.UserID(requestWithSession(sessionID))
	var expiryAfterAging string
	manager.db.QueryRow("SELECT expires_at FROM sessions WHERE id = ?", sessionID).Scan(&expiryAfterAging)
	if expiryAfterAging == marker {
		t.Fatal("an aged session was not renewed in the database")
	}
}

func TestDestroyClearsCookieAndRow(t *testing.T) {
	manager, userID := newTestSessionManager(t)
	startRecorder := httptest.NewRecorder()
	manager.StartForUser(startRecorder, userID, false)
	sessionID := sessionCookieOf(t, startRecorder).Value

	destroyRecorder := httptest.NewRecorder()
	manager.Destroy(destroyRecorder, requestWithSession(sessionID))

	if maxAge := sessionCookieOf(t, destroyRecorder).MaxAge; maxAge != -1 {
		t.Fatalf("expected the cookie to be cleared with Max-Age -1, got %d", maxAge)
	}
	if _, ok := manager.UserID(requestWithSession(sessionID)); ok {
		t.Fatal("the session still resolves after Destroy")
	}
	var remaining int
	manager.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE id = ?", sessionID).Scan(&remaining)
	if remaining != 0 {
		t.Fatal("the session row survived Destroy")
	}
}

func TestDestroyWithoutCookieStillClears(t *testing.T) {
	manager, _ := newTestSessionManager(t)
	recorder := httptest.NewRecorder()
	manager.Destroy(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if maxAge := sessionCookieOf(t, recorder).MaxAge; maxAge != -1 {
		t.Fatalf("expected Max-Age -1, got %d", maxAge)
	}
}

// Password change, deactivation and admin reset all rely on this: no session
// established before the change may survive it.
func TestDeleteAllForUserInvalidatesEverySession(t *testing.T) {
	manager, userID := newTestSessionManager(t)
	otherUserID := insertUser(t, manager.db, "other@example.org", "other", "secret-password")

	var sessionIDs []string
	for round := 0; round < 3; round++ {
		recorder := httptest.NewRecorder()
		manager.StartForUser(recorder, userID, round%2 == 0)
		sessionIDs = append(sessionIDs, sessionCookieOf(t, recorder).Value)
	}
	otherRecorder := httptest.NewRecorder()
	manager.StartForUser(otherRecorder, otherUserID, false)
	otherSessionID := sessionCookieOf(t, otherRecorder).Value

	manager.DeleteAllForUser(userID)

	for _, sessionID := range sessionIDs {
		if _, ok := manager.UserID(requestWithSession(sessionID)); ok {
			t.Fatalf("session %s survived DeleteAllForUser", sessionID)
		}
	}
	if _, ok := manager.UserID(requestWithSession(otherSessionID)); !ok {
		t.Fatal("the session of a different user was deleted too")
	}
}

func TestBoolInt(t *testing.T) {
	if boolInt(true) != 1 || boolInt(false) != 0 {
		t.Fatal("boolInt does not map to 1/0")
	}
}

// expires_at used to be written as RFC3339 while every other column used the
// SQLite layout. Rows from that era must stay readable.
func TestParseDBTimeAcceptsBothLayouts(t *testing.T) {
	sqliteStyle, ok := parseDBTime("2026-07-27 12:34:56")
	if !ok {
		t.Fatal("the SQLite layout was rejected")
	}
	rfcStyle, ok := parseDBTime("2026-07-27T12:34:56Z")
	if !ok {
		t.Fatal("the RFC3339 layout was rejected")
	}
	if !sqliteStyle.Equal(rfcStyle) {
		t.Fatalf("the two layouts parsed to different instants: %s vs %s", sqliteStyle, rfcStyle)
	}
	if _, ok := parseDBTime("not a timestamp"); ok {
		t.Fatal("an unparsable value was accepted")
	}
}

func TestFormatDBTimeIsUTCAndRoundTrips(t *testing.T) {
	location := time.FixedZone("UTC+5", 5*3600)
	moment := time.Date(2026, 7, 27, 17, 34, 56, 0, location)

	formatted := formatDBTime(moment)
	if formatted != "2026-07-27 12:34:56" {
		t.Fatalf("expected the UTC representation, got %q", formatted)
	}
	parsed, ok := parseDBTime(formatted)
	if !ok || !parsed.Equal(moment.UTC()) {
		t.Fatalf("round trip lost the instant: %s vs %s", parsed, moment.UTC())
	}
}

// A session row whose timestamp cannot be parsed must not resolve to a user.
func TestLoadFromDatabaseRejectsUnparsableExpiry(t *testing.T) {
	manager, userID := newTestSessionManager(t)
	manager.db.Exec(
		"INSERT INTO sessions (id, user_id, persistent, expires_at) VALUES (?, ?, 0, ?)",
		"broken", userID, "whenever",
	)
	if _, ok := manager.loadFromDB("broken"); ok {
		t.Fatal("a row with an unparsable expiry was accepted")
	}
}

func TestNewIDIsUniqueAndHex(t *testing.T) {
	seen := make(map[string]bool)
	for round := 0; round < 100; round++ {
		id := newID()
		if len(id) != 64 {
			t.Fatalf("expected 64 hex characters, got %d", len(id))
		}
		if seen[id] {
			t.Fatal("newID returned a duplicate")
		}
		seen[id] = true
	}
}
