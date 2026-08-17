package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeChromium writes an executable stub that behaves like the real binary for
// --version. Starting the actual Chromium is not an option in a test.
func fakeChromium(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chromium")
	if failure := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); failure != nil {
		t.Fatalf("write stub: %v", failure)
	}
	return path
}

// resetProbeCache clears the package-level cache so tests do not see each
// other's results (paths differ per TempDir, but the clock does not).
func resetProbeCache(t *testing.T) {
	t.Helper()
	probeMutex.Lock()
	probes = map[string]probe{}
	probeMutex.Unlock()
	previous := probeClock
	t.Cleanup(func() {
		probeMutex.Lock()
		probes = map[string]probe{}
		probeClock = previous
		probeMutex.Unlock()
	})
}

func TestProbeReturnsTheVersionLine(t *testing.T) {
	resetProbeCache(t)
	binary := fakeChromium(t, `echo "Chromium 120.0.6099.109"`)

	version, failure := Probe(binary)

	if failure != nil {
		t.Fatalf("the probe failed: %v", failure)
	}
	if version != "Chromium 120.0.6099.109" {
		t.Fatalf("the version reads %q", version)
	}
}

// A binary that is present but does not start is exactly the case the file
// check misses.
func TestProbeReportsABinaryThatDoesNotStart(t *testing.T) {
	resetProbeCache(t)
	binary := fakeChromium(t, `echo "error while loading shared libraries: libnss3.so" >&2; exit 127`)

	version, failure := Probe(binary)

	if failure == nil {
		t.Fatalf("a broken binary was reported as fine (%q)", version)
	}
	if !strings.Contains(failure.Error(), "libnss3.so") {
		t.Fatalf("the reason is missing from the error: %v", failure)
	}
}

func TestProbeWithoutABinary(t *testing.T) {
	resetProbeCache(t)

	if _, failure := Probe("   "); failure != ErrNoBinary {
		t.Fatalf("an empty path answered %v", failure)
	}
}

func TestProbeMissingBinary(t *testing.T) {
	resetProbeCache(t)

	if _, failure := Probe(filepath.Join(t.TempDir(), "not-there")); failure == nil {
		t.Fatal("a missing binary was reported as fine")
	}
}

// The info page probes on every refresh click; each probe forks a process, so
// repeated calls must be served from the cache until it expires.
func TestProbeCachesUntilTheTTLExpires(t *testing.T) {
	resetProbeCache(t)
	counter := filepath.Join(t.TempDir(), "calls")
	binary := fakeChromium(t, `echo x >> `+counter+`; echo "Chromium 1.2.3"`)
	now := time.Now()
	probeClock = func() time.Time { return now }

	for round := 0; round < 3; round++ {
		if _, failure := Probe(binary); failure != nil {
			t.Fatalf("round %d failed: %v", round, failure)
		}
	}
	if calls := countLines(t, counter); calls != 1 {
		t.Fatalf("the binary was started %d times despite the cache", calls)
	}

	now = now.Add(probeTTL + time.Second)
	if _, failure := Probe(binary); failure != nil {
		t.Fatalf("the probe after the TTL failed: %v", failure)
	}
	if calls := countLines(t, counter); calls != 2 {
		t.Fatalf("after the TTL the binary was started %d times in total", calls)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	content, failure := os.ReadFile(path)
	if failure != nil {
		t.Fatalf("read %s: %v", path, failure)
	}
	return len(strings.Fields(string(content)))
}
