package auth

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/crypto"
	"meshdepot/internal/db"
	"meshdepot/internal/publicid"
)

const testAppKey = "test-app-key-with-at-least-32-characters"

// newTestAuth builds an Auth service on a throwaway SQLite database. bcrypt is
// the slowest part of these tests, so callers share one service per test rather
// than one per case.
func newTestAuth(t *testing.T) (*Auth, *sql.DB) {
	t.Helper()
	database, failure := db.Open(filepath.Join(t.TempDir(), "auth.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	if failure := db.InitSchema(database); failure != nil {
		t.Fatalf("init schema: %v", failure)
	}
	return New(database, crypto.New(testAppKey), false), database
}

// insertUser creates an active user and returns its id.
func insertUser(t *testing.T, database *sql.DB, email, name, password string) int {
	t.Helper()
	hash, failure := HashPassword(password)
	if failure != nil {
		t.Fatalf("hash password: %v", failure)
	}
	result, failure := database.Exec(
		"INSERT INTO users (email, hash, name, state, public_id) VALUES (?, ?, ?, 'active', ?)",
		email, hash, name, publicid.New(),
	)
	if failure != nil {
		t.Fatalf("insert user: %v", failure)
	}
	id, failure := result.LastInsertId()
	if failure != nil {
		t.Fatalf("last insert id: %v", failure)
	}
	return int(id)
}

// postJSON builds a POST request with a JSON body and a recorder for it.
func postJSON(t *testing.T, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	request.RemoteAddr = "192.0.2.10:1234"
	return httptest.NewRecorder(), request
}

// decodeEnvelope reads the {success,message,data} response envelope.
func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope map[string]any
	if failure := json.Unmarshal(recorder.Body.Bytes(), &envelope); failure != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), failure)
	}
	return envelope
}

// dataOf reads the "data" object out of a success envelope.
func dataOf(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	envelope := decodeEnvelope(t, recorder)
	data, isObject := envelope["data"].(map[string]any)
	if !isObject {
		t.Fatalf("response has no data object: %s", recorder.Body.String())
	}
	return data
}

// errorKeyOf reads the "error" key out of an error envelope.
func errorKeyOf(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	envelope := decodeEnvelope(t, recorder)
	key, isString := envelope["error"].(string)
	if !isString {
		t.Fatalf("response has no error key: %s", recorder.Body.String())
	}
	return key
}

// sessionCookieOf extracts the session cookie the handler set on the response.
func sessionCookieOf(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}
	t.Fatalf("no %s cookie in response", sessionCookie)
	return nil
}

// requestWithSession builds a GET request carrying the given session id.
func requestWithSession(sessionID string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionID})
	return request
}

// loginAndGetSession performs a full login and returns the session id.
func loginAndGetSession(t *testing.T, service *Auth, identifier, password string) string {
	t.Helper()
	recorder, request := postJSON(t, `{"email":"`+identifier+`","password":"`+password+`"}`)
	service.Login(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", recorder.Code, recorder.Body.String())
	}
	return sessionCookieOf(t, recorder).Value
}
