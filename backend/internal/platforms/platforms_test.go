package platforms

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWaitForResolverAcceptsHealthyResolver(t *testing.T) {
	t.Setenv("PLAYWRIGHT_TOKEN", "shared-secret")

	var seenAuth string
	var seenPath string
	resolver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenAuth = request.Header.Get("Authorization")
		seenPath = request.URL.Path
		writer.WriteHeader(http.StatusOK)
	}))
	defer resolver.Close()

	if failure := WaitForResolver(resolver.URL, 2*time.Second); failure != nil {
		t.Fatalf("healthy resolver rejected: %v", failure)
	}
	if seenPath != "/health" {
		t.Errorf("probed %q, want /health", seenPath)
	}
	if seenAuth != "Bearer shared-secret" {
		t.Errorf("Authorization = %q, want the bearer token", seenAuth)
	}
}

func TestWaitForResolverTrimsTrailingSlash(t *testing.T) {
	t.Setenv("PLAYWRIGHT_TOKEN", "")

	var seenPath string
	resolver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenPath = request.URL.Path
		writer.WriteHeader(http.StatusOK)
	}))
	defer resolver.Close()

	if failure := WaitForResolver(resolver.URL+"/", 2*time.Second); failure != nil {
		t.Fatalf("healthy resolver rejected: %v", failure)
	}
	if seenPath != "/health" {
		t.Errorf("probed %q, want /health", seenPath)
	}
}

// A wrong token must not burn the whole startup timeout.
func TestWaitForResolverFailsFastOnBadToken(t *testing.T) {
	t.Setenv("PLAYWRIGHT_TOKEN", "wrong")

	attempts := 0
	resolver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer resolver.Close()

	started := time.Now()
	failure := WaitForResolver(resolver.URL, 10*time.Second)
	if failure == nil {
		t.Fatal("401 accepted, want an error")
	}
	if !strings.Contains(failure.Error(), "401") {
		t.Errorf("error %q does not mention the 401", failure)
	}
	if attempts != 1 {
		t.Errorf("probed %d times, want 1 (no retry on 401)", attempts)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("took %s, want an immediate give-up", elapsed)
	}
}

// An empty URL is a configuration mistake, not a resolver that is still booting.
func TestWaitForResolverRejectsEmptyURL(t *testing.T) {
	started := time.Now()
	failure := WaitForResolver("   ", 10*time.Second)
	if failure == nil {
		t.Fatal("empty URL accepted, want an error")
	}
	if !strings.Contains(failure.Error(), "PLAYWRIGHT_URL") {
		t.Errorf("error %q does not name the variable at fault", failure)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("took %s, want an immediate give-up", elapsed)
	}
}

func TestWaitForResolverTimesOutWhenNothingAnswers(t *testing.T) {
	t.Setenv("PLAYWRIGHT_TOKEN", "")

	// Bind and close, so the port is almost certainly refusing connections.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	started := time.Now()
	failure := WaitForResolver(deadURL, 1500*time.Millisecond)
	if failure == nil {
		t.Fatal("dead resolver accepted, want an error")
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond {
		t.Errorf("gave up after %s, want it to keep retrying until the timeout", elapsed)
	}
}

// A resolver that is still booting answers 5xx for a moment, then comes good.
func TestWaitForResolverRetriesUntilResolverIsUp(t *testing.T) {
	t.Setenv("PLAYWRIGHT_TOKEN", "")

	attempts := 0
	resolver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer resolver.Close()

	if failure := WaitForResolver(resolver.URL, 5*time.Second); failure != nil {
		t.Fatalf("resolver that came up late was rejected: %v", failure)
	}
	if attempts != 3 {
		t.Errorf("probed %d times, want 3", attempts)
	}
}
