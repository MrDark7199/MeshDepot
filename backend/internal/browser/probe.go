package browser

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// probeTimeout: Chromium answers --version in a few dozen milliseconds, and the
// health endpoint must not hang on a wedged install.
const probeTimeout = 5 * time.Second

// probeTTL caches the result. The admin info page probes on every refresh click,
// and each probe forks a process.
const probeTTL = time.Minute

var ErrNoBinary = errors.New("browser: no chromium binary configured")

type probe struct {
	version string
	failure error
	at      time.Time
}

var (
	probeMutex sync.Mutex
	probes     = map[string]probe{}
	probeClock = time.Now
)

// Probe starts the binary with --version and returns what it printed. The
// existence of the file says nothing: a missing shared library or a stale Xvfb
// lock leaves it in place and still fails every download. Results are cached for
// probeTTL, failures included.
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

func runVersion(chromiumBin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	// Output(), not CombinedOutput(): in a container Chromium writes fontconfig and
	// dbus warnings to stderr even on a good run.
	output, failure := exec.CommandContext(ctx, chromiumBin, "--version").Output()
	if failure != nil {
		// The stderr text is the diagnosis; the bare exit status names nothing.
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

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
