package browser

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// probeTimeout bounds the version call. Chromium answers --version in a few
// dozen milliseconds; anything slower is a wedged install, and the health
// endpoint must not hang on it.
const probeTimeout = 5 * time.Second

// probeTTL is how long a result is reused. The admin info page probes on every
// refresh click, and each probe forks a process - without the cache, holding the
// button down would fork one per click.
const probeTTL = time.Minute

// ErrNoBinary is returned when no Chromium path is configured at all.
var ErrNoBinary = errors.New("browser: no chromium binary configured")

type probe struct {
	version string
	failure error
	at      time.Time
}

var (
	probeMutex sync.Mutex
	probes     = map[string]probe{}
	// probeClock is the clock of the cache, replaced in tests.
	probeClock = time.Now
)

// Probe starts the Chromium binary with --version and returns what it printed
// ("Chromium 120.0.6099.109").
//
// The existence of the file says nothing: a missing shared library, a broken
// sandbox or a stale Xvfb lock all leave the binary in place and still make
// every download fail. Only starting it tells them apart. Results are cached
// for probeTTL, failures included - a broken install must not be re-forked on
// every page refresh either.
func Probe(chromiumBin string) (string, error) {
	if strings.TrimSpace(chromiumBin) == "" {
		return "", ErrNoBinary
	}

	probeMutex.Lock()
	cached, known := probes[chromiumBin]
	probeMutex.Unlock()
	if known && probeClock().Sub(cached.at) < probeTTL {
		return cached.version, cached.failure
	}

	version, failure := runVersion(chromiumBin)

	probeMutex.Lock()
	probes[chromiumBin] = probe{version: version, failure: failure, at: probeClock()}
	probeMutex.Unlock()
	return version, failure
}

// runVersion executes the binary once, uncached.
func runVersion(chromiumBin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	// Output(), not CombinedOutput(): in a container Chromium writes fontconfig
	// and dbus warnings to stderr even on a good run, and mixing them in would
	// bury the version line.
	output, failure := exec.CommandContext(ctx, chromiumBin, "--version").Output()
	if failure != nil {
		// The stderr text is the diagnosis ("error while loading shared
		// libraries: …"); the bare exit status would not name anything.
		var exitFailure *exec.ExitError
		if errors.As(failure, &exitFailure) && len(exitFailure.Stderr) > 0 {
			return "", errors.New(failure.Error() + ": " + firstLine(string(exitFailure.Stderr)))
		}
		return "", failure
	}
	version := firstLine(string(output))
	if version == "" {
		return "", errors.New("browser: --version printed nothing")
	}
	return version, nil
}

// firstLine reduces a multi-line output to its first non-empty line.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
