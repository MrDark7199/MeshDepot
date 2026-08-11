package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestHealthNeedsNoSession(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.anonymous(http.MethodGet, "/api/v1/health", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("health returned %d: %s", answer.status, answer.rawBody)
	}
	if status := answer.data(t)["status"]; status != "ok" {
		t.Fatalf("unexpected status %v", status)
	}
}

// Every protected route has to answer 401 without a session - a route that was
// wired without the middleware would be an open door.
func TestProtectedRoutesRejectAnonymousCallers(t *testing.T) {
	testHarness := newHarness(t)

	protected := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/tags"},
		{http.MethodPost, "/api/v1/tags"},
		{http.MethodDelete, "/api/v1/tags/1"},
		{http.MethodGet, "/api/v1/designs"},
		{http.MethodPost, "/api/v1/designs"},
		{http.MethodGet, "/api/v1/designs/1"},
		{http.MethodPut, "/api/v1/designs/1"},
		{http.MethodDelete, "/api/v1/designs/1"},
		{http.MethodGet, "/api/v1/collections"},
		{http.MethodPost, "/api/v1/collections"},
		{http.MethodPost, "/api/v1/download"},
		{http.MethodGet, "/api/v1/download/queue"},
		{http.MethodGet, "/api/v1/designs/1/files"},
		{http.MethodGet, "/api/v1/designs/1/shares"},
		{http.MethodGet, "/api/v1/users/1/notifications"},
		{http.MethodGet, "/api/v1/users/1/stats"},
		{http.MethodGet, "/api/v1/users/1/platform-accounts"},
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodGet, "/api/v1/admin/stats"},
		{http.MethodGet, "/api/v1/admin/settings"},
		{http.MethodGet, "/api/v1/settings/public"},
	}

	for _, route := range protected {
		answer := testHarness.anonymous(route.method, route.path, nil)
		if answer.status != http.StatusUnauthorized {
			t.Fatalf("%s %s answered %d without a session", route.method, route.path, answer.status)
		}
	}
}

// Admin routes must not open up for an ordinary session either.
func TestAdminRoutesRejectOrdinaryUsers(t *testing.T) {
	testHarness := newHarness(t)

	adminOnly := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodPost, "/api/v1/admin/users"},
		{http.MethodPut, "/api/v1/admin/users/1"},
		{http.MethodDelete, "/api/v1/admin/users/1"},
		{http.MethodGet, "/api/v1/admin/stats"},
		{http.MethodGet, "/api/v1/admin/health"},
		{http.MethodGet, "/api/v1/admin/settings"},
		{http.MethodPut, "/api/v1/admin/settings"},
		{http.MethodPost, "/api/v1/admin/library-sync/run"},
		{http.MethodPost, "/api/v1/admin/translations/backfill"},
	}

	for _, route := range adminOnly {
		answer := testHarness.asUser(route.method, route.path, nil)
		if answer.status != http.StatusForbidden {
			t.Fatalf("%s %s answered %d for a non-admin", route.method, route.path, answer.status)
		}
	}
}

// A path outside /api falls through to the embedded SPA.
func TestUnknownPathFallsThroughToTheSPA(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.anonymous(http.MethodGet, "/designs/42", nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the SPA fallback answered %d", answer.status)
	}
	if !strings.HasPrefix(answer.recorder.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("unexpected content type %q", answer.recorder.Header().Get("Content-Type"))
	}
}

// Without APP_URL no CORS headers are sent at all: the SPA shares the origin and
// needs none, and a permissive default would let any site read authenticated
// responses.
func TestCorsStaysSilentWithoutAnAppURL(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.do(request{
		method:  http.MethodGet,
		path:    "/api/v1/health",
		headers: map[string]string{"Origin": "https://evil.example"},
	})

	if origin := answer.recorder.Header().Get("Access-Control-Allow-Origin"); origin != "" {
		t.Fatalf("an origin was allowed: %q", origin)
	}
	if vary := answer.recorder.Header().Get("Vary"); vary != "" {
		t.Fatalf("Vary was set although CORS is off: %q", vary)
	}
}

func TestCorsAllowsOnlyTheConfiguredOrigin(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.server.Cfg.AppURL = "https://depot.example/"
	testHarness.router = testHarness.server.Router()

	allowed := testHarness.do(request{
		method:  http.MethodGet,
		path:    "/api/v1/health",
		headers: map[string]string{"Origin": "https://depot.example"},
	})
	if origin := allowed.recorder.Header().Get("Access-Control-Allow-Origin"); origin != "https://depot.example" {
		t.Fatalf("the configured origin was not allowed: %q", origin)
	}
	if allowed.recorder.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("credentials were not allowed for the configured origin")
	}

	rejected := testHarness.do(request{
		method:  http.MethodGet,
		path:    "/api/v1/health",
		headers: map[string]string{"Origin": "https://evil.example"},
	})
	if origin := rejected.recorder.Header().Get("Access-Control-Allow-Origin"); origin != "" {
		t.Fatalf("a foreign origin was allowed: %q", origin)
	}
	// The answer differs per origin, so caches must not share it.
	if vary := rejected.recorder.Header().Get("Vary"); !strings.Contains(vary, "Origin") {
		t.Fatalf("Vary does not name Origin: %q", vary)
	}
}

func TestPreflightIsAnsweredWithoutReachingTheHandler(t *testing.T) {
	testHarness := newHarness(t)
	testHarness.server.Cfg.AppURL = "https://depot.example"
	testHarness.router = testHarness.server.Router()

	answer := testHarness.do(request{
		method:  http.MethodOptions,
		path:    "/api/v1/designs",
		headers: map[string]string{"Origin": "https://depot.example"},
	})

	if answer.status != http.StatusNoContent {
		t.Fatalf("the preflight answered %d", answer.status)
	}
	if answer.rawBody != "" {
		t.Fatalf("the preflight carried a body: %q", answer.rawBody)
	}
	if methods := answer.recorder.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "DELETE") {
		t.Fatalf("unexpected allowed methods %q", methods)
	}
}

// The session is the only credential; a request without the cookie must not be
// authenticated by anything else it carries.
func TestSessionCookieIsRequiredEvenWithOtherCredentials(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.do(request{
		method: http.MethodGet,
		path:   "/api/v1/designs",
		headers: map[string]string{
			"Authorization": "Bearer " + testHarness.userToken,
			"X-User-Id":     "1",
		},
	})

	if answer.status != http.StatusUnauthorized {
		t.Fatalf("a request without the cookie answered %d", answer.status)
	}
}
