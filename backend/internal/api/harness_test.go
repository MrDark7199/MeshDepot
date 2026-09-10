package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"meshdepot/internal/auth"
	"meshdepot/internal/config"
	"meshdepot/internal/crypto"
	"meshdepot/internal/db"
	"meshdepot/internal/health"
	"meshdepot/internal/platforms"
	"meshdepot/internal/publicid"
	"meshdepot/internal/storage"

	"golang.org/x/crypto/bcrypt"
)

// testKey is 32 bytes so the AES-256 setup succeeds; it never leaves the test.
const testKey = "meshdepot-test-key-32-characters"

// harness bundles a running API server with a real SQLite behind it and the
// session cookie of a logged-in user. Everything goes through server.Router(), so
// routing, middleware and CORS are part of every test.
type harness struct {
	t          *testing.T
	server     *Server
	router     http.Handler
	database   *sql.DB
	adminID    int
	userID     int
	adminToken string
	userToken  string
	dataRoot   string
}

// newHarness replaces the seeded admin password with a known one and logs in both
// an admin and an ordinary user.
func newHarness(t *testing.T) *harness {
	t.Helper()
	directory := t.TempDir()

	// The suite serves its files from 127.0.0.1, which is what the download guard
	// refuses. Lifted for the duration; the guard has its own test.
	platforms.AllowPrivateDownloadsForTest = true
	t.Cleanup(func() { platforms.AllowPrivateDownloadsForTest = false })

	database, failure := db.Open(filepath.Join(directory, "meshdepot.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	if failure := db.InitSchema(database); failure != nil {
		t.Fatalf("init schema: %v", failure)
	}
	// The schema ships with translation on, and DesignsUpdate builds its own service
	// from the database - a test that renames a design would call Google.
	if _, failure := database.Exec("UPDATE app_settings SET value = '0' WHERE key = 'translation_enabled'"); failure != nil {
		t.Fatalf("disable translation: %v", failure)
	}

	cryptoHelper := crypto.New(testKey)
	authService := auth.New(database, cryptoHelper, false)
	configuration := config.Config{
		AppKey:       testKey,
		BasePathData: directory,
	}
	server := New(database, authService, cryptoHelper, configuration, platforms.Registry{}, platforms.Deps{
		DB: database, Crypto: cryptoHelper, Cfg: configuration,
	})

	// The loops do not run here, but the health endpoint reads their heartbeats.
	// Tests about dead loops overwrite this deliberately.
	server.Health.Register(health.DownloadWorker, 2*time.Second)
	server.Health.Register(health.SyncWorker, 5*time.Second)
	server.Health.Register(health.SchedulerForce, 30*time.Second)
	server.Health.Register(health.SchedulerAuto, 10*time.Minute)

	testHarness := &harness{
		t:        t,
		server:   server,
		router:   server.Router(),
		database: database,
		dataRoot: directory,
	}

	testHarness.adminID = testHarness.createUser("admin@example.org", "administrator", true)
	testHarness.userID = testHarness.createUser("user@example.org", "member", false)
	testHarness.adminToken = testHarness.login("admin@example.org")
	testHarness.userToken = testHarness.login("user@example.org")
	return testHarness
}

const testPassword = "correct horse battery staple"

func (testHarness *harness) createUser(email, name string, admin bool) int {
	testHarness.t.Helper()
	hash := hashForTest(testHarness.t, testPassword)
	adminFlag := 0
	if admin {
		adminFlag = 1
	}
	result, failure := testHarness.database.Exec(
		"INSERT INTO users (email, hash, name, state, admin, public_id) VALUES (?, ?, ?, 'active', ?, ?)",
		email, hash, name, adminFlag, publicid.New(),
	)
	if failure != nil {
		testHarness.t.Fatalf("create user %s: %v", email, failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func (testHarness *harness) login(email string) string {
	testHarness.t.Helper()
	response := testHarness.do(request{
		method: http.MethodPost,
		path:   "/api/v1/auth/login",
		body:   map[string]any{"email": email, "password": testPassword},
	})
	if response.status != http.StatusOK {
		testHarness.t.Fatalf("login %s failed: %d %s", email, response.status, response.rawBody)
	}
	for _, cookie := range response.recorder.Result().Cookies() {
		if cookie.Name == "PHPSESSID" {
			return cookie.Value
		}
	}
	testHarness.t.Fatalf("login %s returned no session cookie", email)
	return ""
}

type request struct {
	method string
	path   string
	// body is encoded as JSON unless rawBody is set.
	body     any
	rawBody  io.Reader
	token    string
	headers  map[string]string
	noCookie bool
	// remoteAddr overrides the peer address: httptest hands out a public one, and
	// routes that refuse plaintext from outside the network judge by exactly that.
	remoteAddr string
}

type response struct {
	status   int
	rawBody  string
	recorder *httptest.ResponseRecorder
	decoded  map[string]any
}

func (answer response) data(t *testing.T) map[string]any {
	t.Helper()
	value, ok := answer.decoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("the response carries no data object: %s", answer.rawBody)
	}
	return value
}

func (answer response) list(t *testing.T) []any {
	t.Helper()
	value, ok := answer.decoded["data"].([]any)
	if !ok {
		t.Fatalf("the response carries no data array: %s", answer.rawBody)
	}
	return value
}

func (answer response) errorKey(t *testing.T) string {
	t.Helper()
	value, ok := answer.decoded["error"].(string)
	if !ok {
		t.Fatalf("the response carries no error key: %s", answer.rawBody)
	}
	return value
}

func (testHarness *harness) do(call request) response {
	testHarness.t.Helper()

	var body io.Reader = call.rawBody
	if body == nil && call.body != nil {
		encoded, failure := json.Marshal(call.body)
		if failure != nil {
			testHarness.t.Fatalf("encode body: %v", failure)
		}
		body = bytes.NewReader(encoded)
	}

	httpRequest := httptest.NewRequest(call.method, call.path, body)
	httpRequest.RemoteAddr = "127.0.0.1:54321"
	if call.remoteAddr != "" {
		httpRequest.RemoteAddr = call.remoteAddr
	}
	if call.body != nil && call.rawBody == nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	for key, value := range call.headers {
		httpRequest.Header.Set(key, value)
	}
	if !call.noCookie && call.token != "" {
		httpRequest.AddCookie(&http.Cookie{Name: "PHPSESSID", Value: call.token})
	}

	recorder := httptest.NewRecorder()
	testHarness.router.ServeHTTP(recorder, httpRequest)

	answer := response{status: recorder.Code, rawBody: recorder.Body.String(), recorder: recorder}
	if strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		json.Unmarshal(recorder.Body.Bytes(), &answer.decoded)
	}
	return answer
}

// sessionCookie returns the session a response issued, or "". Changing a password
// rotates it, which a browser follows by itself and a test has to do explicitly.
func (answer response) sessionCookie() string {
	for _, cookie := range answer.recorder.Result().Cookies() {
		if cookie.Name == "PHPSESSID" && cookie.Value != "" {
			return cookie.Value
		}
	}
	return ""
}

func (testHarness *harness) asAdmin(method, path string, body any) response {
	testHarness.t.Helper()
	return testHarness.do(request{method: method, path: path, body: body, token: testHarness.adminToken})
}

func (testHarness *harness) asUser(method, path string, body any) response {
	testHarness.t.Helper()
	return testHarness.do(request{method: method, path: path, body: body, token: testHarness.userToken})
}

func (testHarness *harness) anonymous(method, path string, body any) response {
	testHarness.t.Helper()
	return testHarness.do(request{method: method, path: path, body: body})
}

// insertRow returns the new row id, for rows a test must create exactly as a
// non-HTTP writer does.
func (testHarness *harness) insertRow(query string, args ...any) int {
	testHarness.t.Helper()
	result, failure := testHarness.database.Exec(query, args...)
	if failure != nil {
		testHarness.t.Fatalf("insert: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func (testHarness *harness) insertDesign(ownerID int, name string) int {
	testHarness.t.Helper()
	result, failure := testHarness.database.Exec(
		"INSERT INTO designs (user_id, public_id, name, source_platform) VALUES (?, ?, ?, 'manual')", ownerID, publicid.New(), name,
	)
	if failure != nil {
		testHarness.t.Fatalf("insert design: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

// designPID is the outward id every route addresses a design by. Tests keep
// working with the rowid, which is what the fixtures and DB assertions use.
func (testHarness *harness) designPID(designID int) string {
	testHarness.t.Helper()
	publicID, found, failure := publicid.OfIn(testHarness.database, "designs", designID)
	if failure != nil {
		testHarness.t.Fatalf("read the design public id: %v", failure)
	}
	if !found {
		// A design the test never created: the id should still reach the route and be
		// answered there with a 404.
		return publicid.New()
	}
	return publicID
}

func (testHarness *harness) insertCollection(ownerID int, name string) int {
	testHarness.t.Helper()
	result, failure := testHarness.database.Exec(
		"INSERT INTO collections (user_id, name) VALUES (?, ?)", ownerID, name,
	)
	if failure != nil {
		testHarness.t.Fatalf("insert collection: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func (testHarness *harness) count(query string, args ...any) int {
	testHarness.t.Helper()
	var result int
	if failure := testHarness.database.QueryRow(query, args...).Scan(&result); failure != nil {
		testHarness.t.Fatalf("count query failed: %v", failure)
	}
	return result
}

func (testHarness *harness) scalarInt(query string, args ...any) int {
	testHarness.t.Helper()
	var value int
	if failure := testHarness.database.QueryRow(query, args...).Scan(&value); failure != nil {
		testHarness.t.Fatalf("integer query failed: %v", failure)
	}
	return value
}

func (testHarness *harness) setDesignFields(designID int, fields map[string]any) {
	testHarness.t.Helper()
	for column, value := range fields {
		if _, failure := testHarness.database.Exec("UPDATE designs SET "+column+" = ? WHERE id = ?", value, designID); failure != nil {
			testHarness.t.Fatalf("set %s on design %d: %v", column, designID, failure)
		}
	}
}

// publicID is the name of a user's storage directory and their id in the routes.
func (testHarness *harness) publicID(userID int) string {
	testHarness.t.Helper()
	return testHarness.scalar("SELECT public_id FROM users WHERE id = ?", userID)
}

func (testHarness *harness) userLayout(userID int) storage.UserLayout {
	testHarness.t.Helper()
	return storage.New(testHarness.dataRoot).User(testHarness.publicID(userID))
}

// storedFile resolves a path column against the data root: the columns hold the
// relative form, so opening the raw value would test the working directory.
func (testHarness *harness) storedFile(query string, args ...any) string {
	testHarness.t.Helper()
	return storage.New(testHarness.dataRoot).Abs(testHarness.scalar(query, args...))
}

func (testHarness *harness) scalar(query string, args ...any) string {
	testHarness.t.Helper()
	var value sql.NullString
	failure := testHarness.database.QueryRow(query, args...).Scan(&value)
	if failure == sql.ErrNoRows {
		return ""
	}
	if failure != nil {
		testHarness.t.Fatalf("scalar query failed: %v", failure)
	}
	return value.String
}

func hashForTest(t *testing.T, password string) string {
	t.Helper()
	hash, failure := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if failure != nil {
		t.Fatalf("hash password: %v", failure)
	}
	return string(hash)
}

func (testHarness *harness) attachTag(designID, tagID int) {
	testHarness.t.Helper()
	_, failure := testHarness.database.Exec(
		"INSERT INTO design_tags (design_id, tag_id) VALUES (?, ?)", designID, tagID,
	)
	if failure != nil {
		testHarness.t.Fatalf("attach tag: %v", failure)
	}
}

func (testHarness *harness) insertDesignImage(designID int, path string, sortOrder int) int {
	testHarness.t.Helper()
	result, failure := testHarness.database.Exec(
		"INSERT INTO design_images (design_id, path, sort_order) VALUES (?, ?, ?)", designID, path, sortOrder,
	)
	if failure != nil {
		testHarness.t.Fatalf("insert design image: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func (testHarness *harness) shareDesign(designID, ownerID, recipientID int) {
	testHarness.t.Helper()
	_, failure := testHarness.database.Exec(
		"INSERT INTO design_shares (design_id, owner_user_id, shared_with_user_id) VALUES (?, ?, ?)",
		designID, ownerID, recipientID,
	)
	if failure != nil {
		testHarness.t.Fatalf("share design: %v", failure)
	}
}

func (testHarness *harness) addToCollection(designID, collectionID int) {
	testHarness.t.Helper()
	_, failure := testHarness.database.Exec(
		"INSERT INTO design_collections (design_id, collection_id) VALUES (?, ?)", designID, collectionID,
	)
	if failure != nil {
		testHarness.t.Fatalf("add design to collection: %v", failure)
	}
}

func (testHarness *harness) insertTag(ownerID int, name, source string) int {
	testHarness.t.Helper()
	result, failure := testHarness.database.Exec(
		"INSERT INTO tags (user_id, name, color, source) VALUES (?, ?, '#457b9d', ?)", ownerID, name, source,
	)
	if failure != nil {
		testHarness.t.Fatalf("insert tag: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

// pngBytes is the PNG signature plus padding. http.DetectContentType looks at the
// signature only, so this covers every upload path without a real image.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 600)...)

func (testHarness *harness) uploadAsUser(method, path, field, filename string, content []byte) response {
	testHarness.t.Helper()
	body, contentType := multipartBody(testHarness.t, field, filename, content)
	return testHarness.do(request{
		method:  method,
		path:    path,
		rawBody: body,
		headers: map[string]string{"Content-Type": contentType},
		token:   testHarness.userToken,
	})
}

func multipartBody(t *testing.T, field, filename string, content []byte) (io.Reader, string) {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	part, failure := writer.CreateFormFile(field, filename)
	if failure != nil {
		t.Fatalf("create form file: %v", failure)
	}
	if _, failure := part.Write(content); failure != nil {
		t.Fatalf("write form file: %v", failure)
	}
	if failure := writer.Close(); failure != nil {
		t.Fatalf("close multipart writer: %v", failure)
	}
	return buffer, writer.FormDataContentType()
}

// insertFileVersion creates a current version with one entry, which is what the
// statistics and file endpoints read.
func (testHarness *harness) insertFileVersion(designID, sizeBytes int, filename string) int {
	testHarness.t.Helper()
	result, failure := testHarness.database.Exec(
		"INSERT INTO design_files (design_id, filename, path, size_bytes) VALUES (?, ?, ?, ?)",
		designID, filename, filepath.Join(testHarness.dataRoot, filename), sizeBytes,
	)
	if failure != nil {
		testHarness.t.Fatalf("insert file version: %v", failure)
	}
	versionID, _ := result.LastInsertId()
	_, failure = testHarness.database.Exec(
		"INSERT INTO design_file_entries (design_file_id, filename, path, size_bytes) VALUES (?, ?, ?, ?)",
		versionID, filename, filepath.Join(testHarness.dataRoot, filename), sizeBytes,
	)
	if failure != nil {
		testHarness.t.Fatalf("insert file entry: %v", failure)
	}
	return int(versionID)
}

// insertPlatformAccount writes an empty token or username as NULL, which is what
// the account form leaves behind when only one field is filled in.
func (testHarness *harness) insertPlatformAccount(ownerID int, platform, token, username string) {
	testHarness.t.Helper()
	_, failure := testHarness.database.Exec(
		"INSERT INTO platform_accounts (user_id, platform, token, username) VALUES (?, ?, ?, ?)",
		ownerID, platform, nullIfEmpty(token), nullIfEmpty(username),
	)
	if failure != nil {
		testHarness.t.Fatalf("insert platform account: %v", failure)
	}
}

func (testHarness *harness) insertShareLink(designID, ownerID int) string {
	testHarness.t.Helper()
	token := publicid.New()
	if _, failure := testHarness.database.Exec(
		"INSERT INTO design_share_links (design_id, user_id, token) VALUES (?, ?, ?)", designID, ownerID, token); failure != nil {
		testHarness.t.Fatalf("insert share link: %v", failure)
	}
	return token
}

// clearUpdateAllCooldown lets a test trigger a second run without waiting out the
// ten minutes.
func (testHarness *harness) clearUpdateAllCooldown(ownerID int) {
	testHarness.t.Helper()
	if _, failure := testHarness.database.Exec("UPDATE users SET last_update_all_at = NULL WHERE id = ?", ownerID); failure != nil {
		testHarness.t.Fatalf("clear the update-all cooldown: %v", failure)
	}
}

// insertDownloadJob takes doneAtExpression as SQL such as datetime('now','-2
// days'); empty leaves NULL. Not a bound parameter, or SQLite would compare the
// literal string instead of evaluating it.
func (testHarness *harness) insertDownloadJob(ownerID int, sourceURL, status, doneAtExpression string) int {
	testHarness.t.Helper()
	return testHarness.insertDownloadJobRow(ownerID, sourceURL, "printables", status, doneAtExpression)
}

// insertDownloadJobForPlatform creates an entry for a platform whose credentials
// the handler under test checks.
func (testHarness *harness) insertDownloadJobForPlatform(ownerID int, sourceURL, platform, status string) int {
	testHarness.t.Helper()
	return testHarness.insertDownloadJobRow(ownerID, sourceURL, platform, status, "datetime('now')")
}

func (testHarness *harness) insertDownloadJobRow(ownerID int, sourceURL, platform, status, doneAtExpression string) int {
	testHarness.t.Helper()
	if doneAtExpression == "" {
		doneAtExpression = "NULL"
	}
	result, failure := testHarness.database.Exec(
		"INSERT INTO download_queue (user_id, source_url, platform, status, done_at) VALUES (?, ?, ?, ?, "+doneAtExpression+")",
		ownerID, sourceURL, platform, status,
	)
	if failure != nil {
		testHarness.t.Fatalf("insert download job: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

// insertSyncJob: doneAtExpression follows insertDownloadJob's rule.
func (testHarness *harness) insertSyncJob(ownerID, designID int, status, doneAtExpression string) int {
	testHarness.t.Helper()
	if doneAtExpression == "" {
		doneAtExpression = "NULL"
	}
	result, failure := testHarness.database.Exec(
		"INSERT INTO sync_queue (user_id, design_id, status, done_at) VALUES (?, ?, ?, "+doneAtExpression+")",
		ownerID, designID, status,
	)
	if failure != nil {
		testHarness.t.Fatalf("insert sync job: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

// insertNotification: readAtExpression follows the same rule.
func (testHarness *harness) insertNotification(ownerID int, notificationType, title, readAtExpression string) int {
	testHarness.t.Helper()
	if readAtExpression == "" {
		readAtExpression = "NULL"
	}
	result, failure := testHarness.database.Exec(
		"INSERT INTO notifications (user_id, type, title, read_at) VALUES (?, ?, ?, "+readAtExpression+")",
		ownerID, notificationType, title,
	)
	if failure != nil {
		testHarness.t.Fatalf("insert notification: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}
