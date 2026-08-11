package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexForUnknownRoutes(t *testing.T) {
	handler := Handler()

	for _, path := range []string{"/", "/designs", "/designs/42/versions", "/does-not-exist"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		if recorder.Code != http.StatusOK {
			t.Fatalf("%s returned %d", path, recorder.Code)
		}
		if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
			t.Fatalf("%s returned content type %q", path, contentType)
		}
		if !strings.Contains(recorder.Body.String(), "<") {
			t.Fatalf("%s did not return markup: %q", path, recorder.Body)
		}
	}
}

// Deep links must not be cached, otherwise a browser keeps serving the SPA shell
// of an older deployment.
func TestHandlerNeverCachesTheFallback(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/designs", nil))

	if cacheControl := recorder.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Fatalf("unexpected cache header %q", cacheControl)
	}
}

// An unknown API path must be a 404, not the SPA shell - otherwise a fetch()
// gets HTML where it expects JSON.
func TestHandlerDoesNotAnswerAPIPaths(t *testing.T) {
	handler := Handler()

	for _, path := range []string{"/api/", "/api/v1/designs", "/api/v1/does-not-exist"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d", path, recorder.Code)
		}
		if recorder.Header().Get("Content-Security-Policy") != "" {
			t.Fatalf("%s got the SPA headers", path)
		}
	}
}

// A path that exists in the bundle goes through http.FileServer, which sends
// /index.html on to the canonical "/" - the cache header proves that branch ran
// instead of the SPA fallback.
func TestHandlerServesExistingFileThroughFileServer(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/index.html", nil))

	if recorder.Code != http.StatusMovedPermanently {
		t.Fatalf("index.html returned %d", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "./" {
		t.Fatalf("unexpected redirect target %q", location)
	}
	if cacheControl := recorder.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Fatalf("unexpected cache header %q", cacheControl)
	}
}

func TestHandlerSendsSecurityHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	policy := recorder.Header().Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'self'",
		"script-src 'self' 'wasm-unsafe-eval'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"img-src 'self' data: blob:",
	} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("the policy is missing %q: %s", directive, policy)
		}
	}
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("X-Content-Type-Options is missing")
	}
	if recorder.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("X-Frame-Options is missing")
	}
	if recorder.Header().Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
		t.Fatal("Referrer-Policy is missing")
	}
}

// The CSP must not allow eval or a foreign origin to sneak back in.
func TestSecurityPolicyStaysStrict(t *testing.T) {
	recorder := httptest.NewRecorder()
	setSecurityHeaders(recorder)

	policy := recorder.Header().Get("Content-Security-Policy")
	if strings.Contains(policy, "'unsafe-eval'") {
		t.Fatalf("the policy allows eval: %s", policy)
	}
	if strings.Contains(policy, "http://") || strings.Contains(policy, "https://") {
		t.Fatalf("the policy names a foreign origin: %s", policy)
	}
	if strings.Contains(policy, "script-src") && strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("inline script is allowed: %s", policy)
	}
}

func TestSetCachePerAssetType(t *testing.T) {
	neverCached := []string{"index.html", "assets/app.js", "assets/app.css"}
	for _, path := range neverCached {
		recorder := httptest.NewRecorder()
		setCache(recorder, path)
		if cacheControl := recorder.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
			t.Fatalf("%s got %q", path, cacheControl)
		}
	}

	longCached := []string{"fonts/inter.woff2", "fonts/inter.woff", "logo.png", "icon.svg", "favicon.ico"}
	for _, path := range longCached {
		recorder := httptest.NewRecorder()
		setCache(recorder, path)
		if cacheControl := recorder.Header().Get("Cache-Control"); cacheControl != "public, max-age=604800" {
			t.Fatalf("%s got %q", path, cacheControl)
		}
	}

	recorder := httptest.NewRecorder()
	setCache(recorder, "assets/model.glb")
	if cacheControl := recorder.Header().Get("Cache-Control"); cacheControl != "" {
		t.Fatalf("an unknown asset type got %q", cacheControl)
	}
}

func TestMustReadPanicsOnMissingFile(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a missing file did not panic")
		}
	}()
	mustRead(distFS, "dist/does-not-exist.html")
}
