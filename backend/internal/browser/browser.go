// Package browser runs rod-based headless Chromium sessions (replacement for
// the Node Playwright service). go-rod/stealth supplies the base evasions; the
// platform-specific flows (Cloudflare Turnstile, geetest) are built on top of it
// in the individual downloaders.
package browser

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/stealth"
)

// maxConcurrentBrowsers is how many Chromium instances may run at once. Each
// one costs a few hundred MB against the container's 2 GiB limit, and every
// WithPage call starts its own - without this ceiling, parallel downloads scale
// the memory use with the queue depth until the OOM killer intervenes.
const maxConcurrentBrowsers = 2

// slotWait is how long a caller waits for a free slot before giving up. Longer
// than a normal login flow takes, short enough that a wedged instance does not
// stall the whole queue.
const slotWait = 3 * time.Minute

// ErrBusy is returned when no browser slot became free in time.
var ErrBusy = errors.New("browser: all instances busy")

// Runner starts and stops browser instances, at most maxConcurrentBrowsers of
// them at a time. It deliberately does not keep instances around between calls:
// the logins it drives must not share cookies or fingerprints.
type Runner struct {
	chromiumBin string
	slots       chan struct{}
}

func New(chromiumBin string) *Runner {
	return &Runner{
		chromiumBin: chromiumBin,
		slots:       make(chan struct{}, maxConcurrentBrowsers),
	}
}

// WithPage starts a fresh headless browser with a stealth page, calls action and
// cleans up afterwards. Each call is its own browser subprocess - a crash only
// returns an error, it does not kill the app process. Callers block until a slot
// is free and get ErrBusy after slotWait.
func (runner *Runner) WithPage(timeout time.Duration, action func(*rod.Page) error) (failure error) {
	select {
	case runner.slots <- struct{}{}:
		defer func() { <-runner.slots }()
	case <-time.After(slotWait):
		return ErrBusy
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			failure = fmt.Errorf("browser panic: %v", recovered)
		}
	}()

	browserLauncher := launcher.New().
		Headless(true).
		Set("no-sandbox").
		Set("disable-setuid-sandbox").
		Set("disable-dev-shm-usage")
	if runner.chromiumBin != "" {
		browserLauncher = browserLauncher.Bin(runner.chromiumBin)
	}
	controlURL, failure := browserLauncher.Launch()
	if failure != nil {
		return fmt.Errorf("launch chromium: %w", failure)
	}
	defer browserLauncher.Cleanup()

	rodBrowser := rod.New().ControlURL(controlURL)
	if failure := rodBrowser.Connect(); failure != nil {
		return fmt.Errorf("connect: %w", failure)
	}
	defer rodBrowser.Close()

	page, failure := stealth.Page(rodBrowser)
	if failure != nil {
		return fmt.Errorf("stealth page: %w", failure)
	}
	defer page.Close()

	page = page.Timeout(timeout)
	return action(page)
}
