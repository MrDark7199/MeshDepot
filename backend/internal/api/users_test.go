package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/storage"

	"meshdepot/internal/coerce"
)

// The share picker opens its list on click, before anything has been typed, so
// an empty query lists accounts instead of nothing.
func TestUsersSearchListsAccountsWithoutAQuery(t *testing.T) {
	testHarness := newHarness(t)

	for _, query := range []string{"", "%20%20"} {
		found := testHarness.asUser(http.MethodGet, "/api/v1/users/search?q="+query, nil).list(t)
		if len(found) == 0 {
			t.Fatalf("%q returned no users", query)
		}
	}
}

func TestUsersSearchFindsByNameAndEmail(t *testing.T) {
	testHarness := newHarness(t)

	byName := testHarness.asUser(http.MethodGet, "/api/v1/users/search?q=administrator", nil).list(t)
	if len(byName) != 1 {
		t.Fatalf("the name search returned %d users", len(byName))
	}
	if byName[0].(map[string]any)["email"] != "admin@example.org" {
		t.Fatalf("the wrong user was found: %v", byName[0])
	}

	byEmail := testHarness.asUser(http.MethodGet, "/api/v1/users/search?q=admin@example", nil).list(t)
	if len(byEmail) != 1 {
		t.Fatalf("the email search returned %d users", len(byEmail))
	}
}

// Sharing with yourself does nothing, so the picker must not offer it.
func TestUsersSearchLeavesTheCallerOut(t *testing.T) {
	testHarness := newHarness(t)

	for _, query := range []string{"", "user@example"} {
		for _, found := range testHarness.asUser(http.MethodGet, "/api/v1/users/search?q="+query, nil).list(t) {
			if found.(map[string]any)["email"] == "user@example.org" {
				t.Fatalf("%q offered the caller their own account", query)
			}
		}
	}
}

func TestUsersSearchSkipsInactiveAccounts(t *testing.T) {
	testHarness := newHarness(t)
	inactiveID := testHarness.createUser("gesperrt@example.org", "gesperrt", false)
	if _, failure := testHarness.database.Exec("UPDATE users SET state = 'inactive' WHERE id = ?", inactiveID); failure != nil {
		t.Fatalf("deactivate the user: %v", failure)
	}

	found := testHarness.asUser(http.MethodGet, "/api/v1/users/search?q=gesperrt", nil).list(t)

	if len(found) != 0 {
		t.Fatalf("%d inactive users were found", len(found))
	}
}

// The search term reaches a LIKE, so its wildcards have to be escaped -
// otherwise "50%" would match every account.
func TestUsersSearchEscapesTheWildcards(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.createUser("rabatt@example.org", "50% Rabatt", false)
	testHarness.createUser("fuenfzig@example.org", "5012 Muster", false)

	found := testHarness.asUser(http.MethodGet, "/api/v1/users/search?q=50%25", nil).list(t)

	if len(found) != 1 {
		t.Fatalf("%d users were found", len(found))
	}
	if found[0].(map[string]any)["name"] != "50% Rabatt" {
		t.Fatalf("the wrong user was found: %v", found[0])
	}
}

func TestUsersSearchStopsAtTwentyHits(t *testing.T) {
	testHarness := newHarness(t)
	for index := 0; index < 25; index++ {
		testHarness.createUser(fmt.Sprintf("kollege%d@example.org", index), fmt.Sprintf("Kollege %d", index), false)
	}

	found := testHarness.asUser(http.MethodGet, "/api/v1/users/search?q=Kollege", nil).list(t)

	if len(found) != 20 {
		t.Fatalf("%d users were returned", len(found))
	}
}

func TestUsersUpdateProfileWritesNameAndEmail(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/users/%s/profile", testHarness.publicID(testHarness.userID)),
		map[string]any{"name": "  Neuer Name  ", "email": "neu@example.org"})

	if answer.status != http.StatusOK {
		t.Fatalf("the update answered %d: %s", answer.status, answer.rawBody)
	}
	profile := answer.data(t)
	if profile["name"] != "Neuer Name" || profile["email"] != "neu@example.org" {
		t.Fatalf("the answer carries %v", profile)
	}
	if name := testHarness.scalar("SELECT name FROM users WHERE id = ?", testHarness.userID); name != "Neuer Name" {
		t.Fatalf("the stored name is %q", name)
	}
}

// The display language belongs to the account, not to the browser: kept only in
// localStorage, the same account answered in a different language on the next
// device, and a fresh one started in whatever the last visitor had picked.
func TestUsersUpdateProfileStoresTheLanguage(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/profile", testHarness.publicID(testHarness.userID))

	// A seeded account starts in English, whatever the browser prefers.
	if language := testHarness.scalar("SELECT language FROM users WHERE id = ?", testHarness.userID); language != "en" {
		t.Fatalf("a fresh account starts in %q", language)
	}

	answer := testHarness.asUser(http.MethodPut, path, map[string]any{"language": "de"})

	if answer.status != http.StatusOK {
		t.Fatalf("the update answered %d: %s", answer.status, answer.rawBody)
	}
	if answer.data(t)["language"] != "de" {
		t.Fatalf("the answer carries %v", answer.data(t))
	}
	if language := testHarness.scalar("SELECT language FROM users WHERE id = ?", testHarness.userID); language != "de" {
		t.Fatalf("the stored language is %q", language)
	}

	rejected := testHarness.asUser(http.MethodPut, path, map[string]any{"language": "kl"})
	if rejected.status != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown language answered %d", rejected.status)
	}
	if language := testHarness.scalar("SELECT language FROM users WHERE id = ?", testHarness.userID); language != "de" {
		t.Fatalf("the rejected language was stored anyway: %q", language)
	}
}

// The stylesheet belongs to the account: kept in the browser alone it was gone
// after every reload and never reached a second device.
func TestUsersUpdateProfileStoresTheCustomCSS(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/profile", testHarness.publicID(testHarness.userID))
	css := ":root { --accent: #e91e63 !important; }"

	answer := testHarness.asUser(http.MethodPut, path, map[string]any{"custom_css": css})

	if answer.status != http.StatusOK {
		t.Fatalf("the update answered %d: %s", answer.status, answer.rawBody)
	}
	if answer.data(t)["custom_css"] != css {
		t.Fatalf("the answer carries %v", answer.data(t)["custom_css"])
	}
	if stored := testHarness.scalar("SELECT custom_css FROM users WHERE id = ?", testHarness.userID); stored != css {
		t.Fatalf("the stored stylesheet is %q", stored)
	}

	// And it comes back with the session, which is what lets the browser apply
	// it before the first paint.
	me := testHarness.asUser(http.MethodGet, "/api/v1/auth/me", nil).data(t)
	if me["custom_css"] != css {
		t.Fatalf("/auth/me carries %v", me["custom_css"])
	}

	// Clearing it is a legitimate value, not a missing field.
	testHarness.asUser(http.MethodPut, path, map[string]any{"custom_css": ""})
	if stored := testHarness.scalar("SELECT custom_css FROM users WHERE id = ?", testHarness.userID); stored != "" {
		t.Fatalf("the cleared stylesheet is still %q", stored)
	}
}

// The column travels in every /auth/me response, so it cannot be unbounded.
func TestUsersUpdateProfileRefusesAnOversizedCustomCSS(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/profile", testHarness.publicID(testHarness.userID))

	answer := testHarness.asUser(http.MethodPut, path,
		map[string]any{"custom_css": strings.Repeat("a", maxCustomCSSBytes+1)})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an oversized stylesheet answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.custom_css_too_long" {
		t.Fatalf("unexpected error key %q", key)
	}
	if stored := testHarness.scalar("SELECT custom_css FROM users WHERE id = ?", testHarness.userID); stored != "" {
		t.Fatal("the refused stylesheet was stored anyway")
	}
}

func TestUsersUpdateProfileRejectsAnInvalidEmail(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/users/%s/profile", testHarness.publicID(testHarness.userID)),
		map[string]any{"email": "kein-email"})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an invalid email answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.invalid_email" {
		t.Fatalf("unexpected error key %q", key)
	}
	if email := testHarness.scalar("SELECT email FROM users WHERE id = ?", testHarness.userID); email != "user@example.org" {
		t.Fatalf("the email was changed to %q", email)
	}
}

// A field that is not in the body stays untouched. The name column is NOT NULL,
// so a cleared name becomes the empty string; a cleared email becomes NULL.
func TestUsersUpdateProfileWritesOnlyTheSubmittedFields(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/profile", testHarness.publicID(testHarness.userID))

	answer := testHarness.asUser(http.MethodPut, path, map[string]any{"name": "   "})

	if answer.status != http.StatusOK {
		t.Fatalf("a blank name answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM users WHERE id = ? AND name = ''", testHarness.userID); count != 1 {
		t.Fatal("the blank name was not stored as an empty string")
	}
	if email := testHarness.scalar("SELECT email FROM users WHERE id = ?", testHarness.userID); email != "user@example.org" {
		t.Fatalf("the email was changed to %q", email)
	}

	testHarness.asUser(http.MethodPut, path, map[string]any{"email": ""})

	if count := testHarness.count("SELECT COUNT(*) FROM users WHERE id = ? AND email IS NULL", testHarness.userID); count != 1 {
		t.Fatal("the blank email was not stored as NULL")
	}
}

func TestUsersUpdateProfileLeavesEverythingAloneOnAnEmptyBody(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPut, fmt.Sprintf("/api/v1/users/%s/profile", testHarness.publicID(testHarness.userID)),
		map[string]any{})

	if answer.status != http.StatusOK {
		t.Fatalf("an empty body answered %d: %s", answer.status, answer.rawBody)
	}
	if name := testHarness.scalar("SELECT name FROM users WHERE id = ?", testHarness.userID); name != "member" {
		t.Fatalf("the name became %q", name)
	}
}

func TestUsersProfileRoutesRefuseAForeignUserID(t *testing.T) {
	testHarness := newHarness(t)

	calls := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/profile"},
		{http.MethodPost, "/change-password"},
		{http.MethodPost, "/force-password"},
		{http.MethodPost, "/avatar"},
		{http.MethodDelete, "/avatar"},
		{http.MethodGet, "/stats"},
	}

	for _, call := range calls {
		path := fmt.Sprintf("/api/v1/users/%s%s", testHarness.publicID(testHarness.adminID), call.path)
		answer := testHarness.asUser(call.method, path, map[string]any{})
		if answer.status != http.StatusForbidden {
			t.Fatalf("%s %s answered %d", call.method, path, answer.status)
		}
	}
	if name := testHarness.scalar("SELECT name FROM users WHERE id = ?", testHarness.adminID); name != "administrator" {
		t.Fatalf("the foreign profile was changed to %q", name)
	}
}

func TestUsersChangePasswordReplacesTheHash(t *testing.T) {
	testHarness := newHarness(t)
	oldHash := testHarness.scalar("SELECT hash FROM users WHERE id = ?", testHarness.userID)

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/users/%s/change-password", testHarness.publicID(testHarness.userID)),
		map[string]any{"current_password": testPassword, "new_password": "ein neues passwort"})

	if answer.status != http.StatusOK {
		t.Fatalf("the change answered %d: %s", answer.status, answer.rawBody)
	}
	if newHash := testHarness.scalar("SELECT hash FROM users WHERE id = ?", testHarness.userID); newHash == oldHash {
		t.Fatal("the hash was not replaced")
	}
	if testHarness.count("SELECT COUNT(*) FROM users WHERE id = ? AND must_change_password = 0", testHarness.userID) != 1 {
		t.Fatal("must_change_password is still set")
	}
}

// Changing the password invalidates every session of the user, including the
// one that made the request - but the answer carries a fresh cookie so the
// current device stays logged in.
func TestUsersChangePasswordRotatesTheSession(t *testing.T) {
	testHarness := newHarness(t)
	oldToken := testHarness.userToken

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/users/%s/change-password", testHarness.publicID(testHarness.userID)),
		map[string]any{"current_password": testPassword, "new_password": "ein neues passwort"})

	newToken := ""
	for _, cookie := range answer.recorder.Result().Cookies() {
		if cookie.Name == "PHPSESSID" {
			newToken = cookie.Value
		}
	}
	if newToken == "" || newToken == oldToken {
		t.Fatalf("no fresh session cookie was issued (%q)", newToken)
	}

	withOldToken := testHarness.do(request{method: http.MethodGet, path: "/api/v1/designs", token: oldToken})
	if withOldToken.status != http.StatusUnauthorized {
		t.Fatalf("the old session still answered %d", withOldToken.status)
	}
	withNewToken := testHarness.do(request{method: http.MethodGet, path: "/api/v1/designs", token: newToken})
	if withNewToken.status != http.StatusOK {
		t.Fatalf("the fresh session answered %d", withNewToken.status)
	}
}

func TestUsersChangePasswordRejectsTheWrongCurrentPassword(t *testing.T) {
	testHarness := newHarness(t)
	oldHash := testHarness.scalar("SELECT hash FROM users WHERE id = ?", testHarness.userID)

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/users/%s/change-password", testHarness.publicID(testHarness.userID)),
		map[string]any{"current_password": "falsches passwort", "new_password": "ein neues passwort"})

	// 422, not 401: the session is valid, only the supplied password is wrong.
	// A 401 tears the session down in the browser.
	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("the wrong password answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.wrong_password" {
		t.Fatalf("unexpected error key %q", key)
	}
	if hash := testHarness.scalar("SELECT hash FROM users WHERE id = ?", testHarness.userID); hash != oldHash {
		t.Fatal("the hash was replaced anyway")
	}
}

func TestUsersChangePasswordChecksBothFields(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/change-password", testHarness.publicID(testHarness.userID))

	cases := []struct {
		body     map[string]any
		errorKey string
	}{
		{map[string]any{"new_password": "ein neues passwort"}, "error.credentials_required"},
		{map[string]any{"current_password": testPassword}, "error.credentials_required"},
		{map[string]any{"current_password": testPassword, "new_password": "kurz"}, "error.password_too_short"},
	}

	for _, testCase := range cases {
		answer := testHarness.asUser(http.MethodPost, path, testCase.body)
		if answer.status != http.StatusUnprocessableEntity {
			t.Fatalf("%v answered %d", testCase.body, answer.status)
		}
		if key := answer.errorKey(t); key != testCase.errorKey {
			t.Fatalf("%v produced the error key %q", testCase.body, key)
		}
	}
}

// The forced change is the way out of must_change_password, so it works without
// the old password - but the length rule still applies.
func TestUsersForcePasswordSetsThePasswordWithoutTheOldOne(t *testing.T) {
	testHarness := newHarness(t)
	if _, failure := testHarness.database.Exec("UPDATE users SET must_change_password = 1 WHERE id = ?", testHarness.userID); failure != nil {
		t.Fatalf("set must_change_password: %v", failure)
	}

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/users/%s/force-password", testHarness.publicID(testHarness.userID)),
		map[string]any{"new_password": "ein neues passwort"})

	if answer.status != http.StatusOK {
		t.Fatalf("the forced change answered %d: %s", answer.status, answer.rawBody)
	}
	if testHarness.count("SELECT COUNT(*) FROM users WHERE id = ? AND must_change_password = 0", testHarness.userID) != 1 {
		t.Fatal("must_change_password is still set")
	}
}

func TestUsersForcePasswordRejectsAShortPassword(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodPost, fmt.Sprintf("/api/v1/users/%s/force-password", testHarness.publicID(testHarness.userID)),
		map[string]any{"new_password": "kurz"})

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a short password answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.password_too_short" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestUsersUploadAvatarStoresTheImage(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.uploadAsUser(http.MethodPost, fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID)),
		"avatar", "profil.png", pngBytes)

	if answer.status != http.StatusOK {
		t.Fatalf("the upload answered %d: %s", answer.status, answer.rawBody)
	}
	expectedURL := fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID))
	if answer.data(t)["avatar_url"] != expectedURL {
		t.Fatalf("the avatar url is %v", answer.data(t)["avatar_url"])
	}
	storedPath := testHarness.scalar("SELECT avatar_path FROM users WHERE id = ?", testHarness.userID)
	if filepath.IsAbs(storedPath) {
		t.Fatalf("the avatar path was stored absolute: %q", storedPath)
	}
	layout := storage.New(testHarness.dataRoot)
	if filepath.Dir(layout.Abs(storedPath)) != filepath.Clean(testHarness.userLayout(testHarness.userID).Avatar()) {
		t.Fatalf("the avatar was stored outside the avatar directory: %q", storedPath)
	}
	if filepath.Ext(storedPath) != ".png" {
		t.Fatalf("the extension comes from the content, not the filename: %q", storedPath)
	}
	if _, failure := os.Stat(layout.Abs(storedPath)); failure != nil {
		t.Fatalf("the file is missing: %v", failure)
	}
}

// The extension follows the sniffed content type, not the submitted filename -
// a .png name on HTML bytes must not produce a .png file.
func TestUsersUploadAvatarRejectsANonImage(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.uploadAsUser(http.MethodPost, fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID)),
		"avatar", "profil.png", []byte("<html><script>alert(1)</script></html>"))

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an html upload answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.invalid_image_type" {
		t.Fatalf("unexpected error key %q", key)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM users WHERE id = ? AND avatar_path IS NULL", testHarness.userID); count != 1 {
		t.Fatal("an avatar path was stored anyway")
	}
}

func TestUsersUploadAvatarNeedsTheAvatarField(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.uploadAsUser(http.MethodPost, fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID)),
		"bild", "profil.png", pngBytes)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("the wrong field name answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.upload_failed" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestUsersUploadAvatarRemovesThePreviousFile(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID))

	testHarness.uploadAsUser(http.MethodPost, path, "avatar", "erst.png", pngBytes)
	firstPath := testHarness.scalar("SELECT avatar_path FROM users WHERE id = ?", testHarness.userID)
	testHarness.uploadAsUser(http.MethodPost, path, "avatar", "zweit.png", pngBytes)
	secondPath := testHarness.scalar("SELECT avatar_path FROM users WHERE id = ?", testHarness.userID)

	if firstPath == secondPath {
		t.Fatal("the second upload reused the file name")
	}
	if _, failure := os.Stat(firstPath); failure == nil {
		t.Fatal("the previous avatar is still on disk")
	}
	entries, failure := os.ReadDir(testHarness.userLayout(testHarness.userID).Avatar())
	if failure != nil {
		t.Fatalf("read the avatar directory: %v", failure)
	}
	if len(entries) != 1 {
		t.Fatalf("%d files are lying in the avatar directory", len(entries))
	}
}

func TestUsersDeleteAvatarRemovesFileAndColumn(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID))
	testHarness.uploadAsUser(http.MethodPost, path, "avatar", "profil.png", pngBytes)
	storedPath := testHarness.scalar("SELECT avatar_path FROM users WHERE id = ?", testHarness.userID)

	answer := testHarness.asUser(http.MethodDelete, path, nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if _, failure := os.Stat(storedPath); failure == nil {
		t.Fatal("the file is still on disk")
	}
	if count := testHarness.count("SELECT COUNT(*) FROM users WHERE id = ? AND avatar_path IS NULL", testHarness.userID); count != 1 {
		t.Fatal("the column still holds a path")
	}
}

func TestUsersDeleteAvatarWorksWithoutAnAvatar(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID)), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
}

// The avatar is the one image route without a session: it is embedded in pages
// that other users see.
func TestUsersServeAvatarIsPublic(t *testing.T) {
	testHarness := newHarness(t)
	path := fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID))
	testHarness.uploadAsUser(http.MethodPost, path, "avatar", "profil.png", pngBytes)

	answer := testHarness.anonymous(http.MethodGet, path, nil)

	if answer.status != http.StatusOK {
		t.Fatalf("serving answered %d: %s", answer.status, answer.rawBody)
	}
	headers := answer.recorder.Header()
	if headers.Get("Content-Type") != "image/png" {
		t.Fatalf("the content type is %q", headers.Get("Content-Type"))
	}
	if headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("the nosniff header is missing")
	}
	if headers.Get("Cache-Control") != "public, max-age=3600" {
		t.Fatalf("the cache header is %q", headers.Get("Cache-Control"))
	}
}

func TestUsersServeAvatarIsNotFoundWithoutAnAvatar(t *testing.T) {
	testHarness := newHarness(t)

	cases := []string{
		fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID)),
		"/api/v1/users/98765/avatar",
		"/api/v1/users/abc/avatar",
	}

	for _, path := range cases {
		answer := testHarness.anonymous(http.MethodGet, path, nil)
		if answer.status != http.StatusNotFound {
			t.Fatalf("%s answered %d", path, answer.status)
		}
	}
}

// A path that no longer holds an image - or never did - must not be handed
// back with a guessed content type.
func TestUsersServeAvatarRefusesAFileThatIsNotAnImage(t *testing.T) {
	testHarness := newHarness(t)
	if failure := os.MkdirAll(testHarness.userLayout(testHarness.userID).Avatar(), 0o775); failure != nil {
		t.Fatalf("create the avatar directory: %v", failure)
	}
	textPath := filepath.Join(testHarness.userLayout(testHarness.userID).Avatar(), "avatar.png")
	if failure := os.WriteFile(textPath, []byte("<html>kein bild</html>"), 0o644); failure != nil {
		t.Fatalf("write the file: %v", failure)
	}
	if _, failure := testHarness.database.Exec("UPDATE users SET avatar_path = ? WHERE id = ?", textPath, testHarness.userID); failure != nil {
		t.Fatalf("store the path: %v", failure)
	}

	answer := testHarness.anonymous(http.MethodGet, fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID)), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a non-image answered %d", answer.status)
	}
}

func TestUsersStatsCountsTheOwnLibrary(t *testing.T) {
	testHarness := newHarness(t)
	firstDesign := testHarness.insertDesign(testHarness.userID, "Erstes")
	secondDesign := testHarness.insertDesign(testHarness.userID, "Zweites")
	testHarness.setDesignFields(firstDesign, map[string]any{
		"source_url": printablesURL, "source_platform": "printables",
	})
	testHarness.setDesignFields(secondDesign, map[string]any{"source_platform": "thingiverse"})
	testHarness.insertTag(testHarness.userID, "funktional", "manual")
	testHarness.insertCollection(testHarness.userID, "Lampen")
	testHarness.insertFileVersion(firstDesign, 2048, "modell.zip")

	foreignDesign := testHarness.insertDesign(testHarness.adminID, "Fremd")
	testHarness.insertTag(testHarness.adminID, "fremd", "manual")
	testHarness.insertCollection(testHarness.adminID, "Fremd")
	testHarness.insertFileVersion(foreignDesign, 999999, "fremd.zip")

	stats := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/users/%s/stats", testHarness.publicID(testHarness.userID)), nil).data(t)

	expected := map[string]int{
		"design_count": 2, "tag_count": 1, "collection_count": 1,
		"synced_count": 1, "entry_count": 1, "used_bytes": 2048,
	}
	for key, want := range expected {
		if got := coerce.Int(stats[key]); got != want {
			t.Fatalf("%s is %d, expected %d", key, got, want)
		}
	}
	platforms := stats["platforms"].([]any)
	if len(platforms) != 2 {
		t.Fatalf("%d platforms were counted", len(platforms))
	}
	if stats["newest_design_at"] == nil {
		t.Fatal("newest_design_at is missing")
	}
}

func TestUsersStatsAreZeroForAnEmptyLibrary(t *testing.T) {
	testHarness := newHarness(t)

	stats := testHarness.asUser(http.MethodGet, fmt.Sprintf("/api/v1/users/%s/stats", testHarness.publicID(testHarness.userID)), nil).data(t)

	for _, key := range []string{"design_count", "tag_count", "collection_count", "synced_count", "entry_count", "used_bytes"} {
		if got := coerce.Int(stats[key]); got != 0 {
			t.Fatalf("%s is %d in an empty library", key, got)
		}
	}
	if platforms := stats["platforms"].([]any); len(platforms) != 0 {
		t.Fatalf("%d platforms were counted", len(platforms))
	}
	if stats["newest_design_at"] != nil {
		t.Fatalf("newest_design_at is %v", stats["newest_design_at"])
	}
}
