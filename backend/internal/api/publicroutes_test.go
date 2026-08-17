package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// selfScopedRoutes are the routes whose {id} is the addressed account. They all
// go through requireSelf, so one table covers the lot.
func selfScopedRoutes(id string) []struct{ method, path string } {
	return []struct{ method, path string }{
		{http.MethodGet, "/api/v1/users/" + id + "/notifications"},
		{http.MethodPost, "/api/v1/users/" + id + "/notifications/read-all"},
		{http.MethodDelete, "/api/v1/users/" + id + "/notifications"},
		{http.MethodGet, "/api/v1/users/" + id + "/notification-prefs"},
		{http.MethodPut, "/api/v1/users/" + id + "/notification-prefs"},
		{http.MethodPut, "/api/v1/users/" + id + "/profile"},
		{http.MethodPost, "/api/v1/users/" + id + "/change-password"},
		{http.MethodPost, "/api/v1/users/" + id + "/force-password"},
		{http.MethodGet, "/api/v1/users/" + id + "/stats"},
		{http.MethodDelete, "/api/v1/users/" + id + "/avatar"},
		{http.MethodGet, "/api/v1/users/" + id + "/platform-accounts"},
		{http.MethodPost, "/api/v1/users/" + id + "/platform-accounts/sync-all"},
	}
}

// The numeric id is what the routes used to take. It must not address an
// account any more, or the change bought nothing.
func TestSelfScopedRoutesRejectTheNumericID(t *testing.T) {
	testHarness := newHarness(t)

	for _, route := range selfScopedRoutes(fmt.Sprintf("%d", testHarness.userID)) {
		answer := testHarness.asUser(route.method, route.path, map[string]any{})
		if answer.status != http.StatusNotFound {
			t.Errorf("%s %s answered %d, want 404", route.method, route.path, answer.status)
		}
	}
}

func TestSelfScopedRoutesRejectAForeignPublicID(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.publicID(testHarness.adminID)

	for _, route := range selfScopedRoutes(foreign) {
		answer := testHarness.asUser(route.method, route.path, map[string]any{})
		if answer.status != http.StatusForbidden {
			t.Errorf("%s %s answered %d, want 403", route.method, route.path, answer.status)
		}
	}
}

// The own id has to keep working - a 404 everywhere would pass the two tests
// above without the routes being usable at all.
func TestSelfScopedRoutesAcceptTheOwnPublicID(t *testing.T) {
	testHarness := newHarness(t)
	own := testHarness.publicID(testHarness.userID)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/users/" + own + "/notifications"},
		{http.MethodGet, "/api/v1/users/" + own + "/notification-prefs"},
		{http.MethodGet, "/api/v1/users/" + own + "/stats"},
		{http.MethodGet, "/api/v1/users/" + own + "/platform-accounts"},
	} {
		answer := testHarness.asUser(route.method, route.path, nil)
		if answer.status != http.StatusOK {
			t.Errorf("%s %s answered %d: %s", route.method, route.path, answer.status, answer.rawBody)
		}
	}
}

func TestAdminRoutesAddressUsersByPublicID(t *testing.T) {
	testHarness := newHarness(t)
	target := testHarness.publicID(testHarness.userID)
	numeric := fmt.Sprintf("%d", testHarness.userID)

	cases := []struct {
		method, suffix string
		body           any
	}{
		{http.MethodPut, "", map[string]any{"name": "Umbenannt"}},
		{http.MethodPost, "/reset-password", map[string]any{"new_password": "a-long-enough-password"}},
		{http.MethodGet, "/detail", nil},
	}
	for _, testCase := range cases {
		answer := testHarness.asAdmin(testCase.method, "/api/v1/admin/users/"+target+testCase.suffix, testCase.body)
		if answer.status != http.StatusOK {
			t.Errorf("%s %s answered %d: %s", testCase.method, testCase.suffix, answer.status, answer.rawBody)
		}
		numericAnswer := testHarness.asAdmin(testCase.method, "/api/v1/admin/users/"+numeric+testCase.suffix, testCase.body)
		if numericAnswer.status != http.StatusNotFound {
			t.Errorf("%s %s with the numeric id answered %d, want 404", testCase.method, testCase.suffix, numericAnswer.status)
		}
	}
}

// The avatar endpoint is public, which is what made the numeric id walkable
// from outside without any session at all.
func TestAvatarIsServedByPublicIDOnly(t *testing.T) {
	testHarness := newHarness(t)
	own := testHarness.publicID(testHarness.userID)
	testHarness.uploadAsUser(http.MethodPost, "/api/v1/users/"+own+"/avatar", "avatar", "bild.png", pngBytes)

	if answer := testHarness.anonymous(http.MethodGet, "/api/v1/users/"+own+"/avatar", nil); answer.status != http.StatusOK {
		t.Fatalf("the public id answered %d", answer.status)
	}
	numeric := fmt.Sprintf("/api/v1/users/%d/avatar", testHarness.userID)
	if answer := testHarness.anonymous(http.MethodGet, numeric, nil); answer.status != http.StatusNotFound {
		t.Fatalf("the numeric id answered %d, want 404", answer.status)
	}
}

// hexID matches the shape of a public id.
var hexID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// The routes only address accounts opaquely if the bodies stop handing the
// numeric id out. SELECT * and SELECT d.* carry user_id along by construction,
// so this walks whole responses rather than trusting the queries.
func TestResponsesNeverCarryANumericUserID(t *testing.T) {
	testHarness := newHarness(t)
	own := testHarness.publicID(testHarness.userID)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Inhalt")
	testHarness.insertCollection(testHarness.userID, "Sammlung")
	testHarness.shareDesign(designID, testHarness.userID, testHarness.adminID)

	endpoints := []string{
		"/api/v1/auth/me",
		"/api/v1/designs",
		fmt.Sprintf("/api/v1/designs/%s", testHarness.designPID(designID)),
		fmt.Sprintf("/api/v1/designs/%s/shares", testHarness.designPID(designID)),
		"/api/v1/collections",
		"/api/v1/users/" + own + "/notification-prefs",
		"/api/v1/users/" + own + "/platform-accounts",
		"/api/v1/users/" + own + "/stats",
	}
	for _, endpoint := range endpoints {
		answer := testHarness.asUser(http.MethodGet, endpoint, nil)
		if answer.status != http.StatusOK {
			t.Errorf("%s answered %d: %s", endpoint, answer.status, answer.rawBody)
			continue
		}
		assertNoInternalIDs(t, endpoint, answer.rawBody)
	}
	for _, endpoint := range []string{"/api/v1/admin/users", "/api/v1/admin/stats", "/api/v1/admin/users/" + own + "/detail"} {
		answer := testHarness.asAdmin(http.MethodGet, endpoint, nil)
		if answer.status != http.StatusOK {
			t.Errorf("%s answered %d: %s", endpoint, answer.status, answer.rawBody)
			continue
		}
		assertNoInternalIDs(t, endpoint, answer.rawBody)
	}
}

// assertNoInternalIDs walks a decoded body and reports every internal key and
// every user id that arrived as a number.
func assertNoInternalIDs(t *testing.T, endpoint, body string) {
	t.Helper()
	var decoded any
	if failure := json.Unmarshal([]byte(body), &decoded); failure != nil {
		t.Errorf("%s: undecodable body: %v", endpoint, failure)
		return
	}
	forbidden := []string{"user_id", "owner_user_id", "shared_with_user_id", "hash", "totp_secret", "avatar_path"}

	var walk func(value any, path string)
	walk = func(value any, path string) {
		switch typed := value.(type) {
		case map[string]any:
			for key, nested := range typed {
				for _, name := range forbidden {
					if key == name {
						t.Errorf("%s carries %s at %s", endpoint, key, path)
					}
				}
				// A user object is the one carrying an email key next to a name -
				// designs and collections have a name but never an email. Its id has
				// to be the public one.
				_, hasEmail := typed["email"]
				_, hasName := typed["name"]
				if key == "id" && hasEmail && hasName {
					if text, ok := nested.(string); !ok || !hexID.MatchString(text) {
						t.Errorf("%s: the user id at %s is %v, want a 32-character public id", endpoint, path, nested)
					}
				}
				walk(nested, path+"/"+key)
			}
		case []any:
			for index, nested := range typed {
				walk(nested, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	walk(decoded, "")
	if strings.Contains(body, `"user_id"`) {
		t.Errorf("%s still contains a user_id key", endpoint)
	}
}

// The blocking modal only covers the screen; the session stays valid. Anyone
// closing it - or skipping the UI - could keep using the API with the seeded
// admin/admin credentials, so the middleware has to hold the door.
func TestAForcedPasswordChangeBlocksEverythingElse(t *testing.T) {
	testHarness := newHarness(t)
	own := testHarness.publicID(testHarness.userID)
	if _, failure := testHarness.database.Exec(
		"UPDATE users SET must_change_password = 1 WHERE id = ?", testHarness.userID); failure != nil {
		t.Fatalf("set the flag: %v", failure)
	}

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/designs"},
		{http.MethodGet, "/api/v1/collections"},
		{http.MethodGet, "/api/v1/users/" + own + "/stats"},
		{http.MethodPost, "/api/v1/designs"},
	} {
		answer := testHarness.asUser(route.method, route.path, map[string]any{"name": "x"})
		if answer.status != http.StatusForbidden {
			t.Errorf("%s %s answered %d, want 403", route.method, route.path, answer.status)
		}
		if answer.status == http.StatusForbidden && answer.errorKey(t) != "error.password_change_required" {
			t.Errorf("%s %s reported %q", route.method, route.path, answer.errorKey(t))
		}
	}

	// The way out has to stay open, and it has to clear the block.
	answer := testHarness.asUser(http.MethodPost, "/api/v1/users/"+own+"/force-password",
		map[string]any{"new_password": "a-brand-new-password"})
	if answer.status != http.StatusOK {
		t.Fatalf("setting the password answered %d: %s", answer.status, answer.rawBody)
	}
	// Setting a password rotates the session; follow the new cookie the way a
	// browser does, or the next call looks like a logout.
	if rotated := answer.sessionCookie(); rotated != "" {
		testHarness.userToken = rotated
	}
	if answer := testHarness.asUser(http.MethodGet, "/api/v1/designs", nil); answer.status != http.StatusOK {
		t.Fatalf("the library is still blocked after the change: %d", answer.status)
	}
}

// A wrong current password is a form error, not a dead session. As a 401 it
// tripped the frontend's global logout, so a typo threw the user out and looked
// like the change had gone through.
func TestAWrongCurrentPasswordIsNotAnAuthenticationFailure(t *testing.T) {
	testHarness := newHarness(t)
	own := testHarness.publicID(testHarness.userID)

	answer := testHarness.asUser(http.MethodPost, "/api/v1/users/"+own+"/change-password",
		map[string]any{"current_password": "not the password", "new_password": "a-brand-new-password"})

	if answer.status == http.StatusUnauthorized {
		t.Fatal("a wrong password answered 401, which logs the session out in the browser")
	}
	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("answered %d, want 422", answer.status)
	}
	if answer.errorKey(t) != "error.wrong_password" {
		t.Fatalf("reported %q", answer.errorKey(t))
	}
	// The old password still has to work - nothing may have changed.
	if token := testHarness.login("user@example.org"); token == "" {
		t.Fatal("the account cannot log in with its unchanged password")
	}
}

// Changing a password must not throw the user off the device they are sitting
// at. The session is replaced rather than merely kept: every other session of
// the account dies with the old password, and the current one continues on a
// fresh id that comes back with the response.
func TestChangingThePasswordKeepsTheCurrentSessionAlive(t *testing.T) {
	testHarness := newHarness(t)
	own := testHarness.publicID(testHarness.userID)
	otherDevice := testHarness.login("user@example.org")

	answer := testHarness.asUser(http.MethodPost, "/api/v1/users/"+own+"/change-password",
		map[string]any{"current_password": testPassword, "new_password": "a-brand-new-password"})
	if answer.status != http.StatusOK {
		t.Fatalf("the change answered %d: %s", answer.status, answer.rawBody)
	}
	rotated := answer.sessionCookie()
	if rotated == "" {
		t.Fatal("no new session came back - the current device would be logged out")
	}

	current := testHarness.do(request{method: http.MethodGet, path: "/api/v1/designs", token: rotated})
	if current.status != http.StatusOK {
		t.Fatalf("the current device answered %d after the change", current.status)
	}
	// The other device held a session that was established with the old
	// password; it has to be gone.
	other := testHarness.do(request{method: http.MethodGet, path: "/api/v1/designs", token: otherDevice})
	if other.status != http.StatusUnauthorized {
		t.Fatalf("a session from before the change still answers %d", other.status)
	}
}
