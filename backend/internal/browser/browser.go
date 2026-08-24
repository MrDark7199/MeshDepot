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
	"github.com/go-rod/rod/lib/proto"
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

	// OnRequest, when set, is called with the URL of every request a page issues.
	// It exists so outbound traffic can be counted without this package knowing
	// anything about platforms or the database. Called from the page's event
	// goroutine, so the callback has to be safe for concurrent use.
	OnRequest func(rawURL string)
}

func New(chromiumBin string) *Runner {
	return &Runner{
		chromiumBin: chromiumBin,
		slots:       make(chan struct{}, maxConcurrentBrowsers),
	}
}

// newLauncher builds the launcher configuration every browser in this package
// starts from. The sandbox switches are needed because the container runs as
// root, and its /dev/shm is too small for Chromium's default use of it.
func (runner *Runner) newLauncher() *launcher.Launcher {
	browserLauncher := launcher.New().
		Headless(true).
		Set("no-sandbox").
		Set("disable-setuid-sandbox").
		Set("disable-dev-shm-usage")
	if runner.chromiumBin != "" {
		browserLauncher = browserLauncher.Bin(runner.chromiumBin)
	}
	return browserLauncher
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

	browserLauncher := runner.newLauncher()
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

	// Report every request the page issues. A single model page pulls dozens of
	// subresources, which is exactly the volume the per-job cooldown never saw.
	if runner.OnRequest != nil {
		if (proto.NetworkEnable{}).Call(page) == nil {
			go page.EachEvent(func(event *proto.NetworkRequestWillBeSent) {
				runner.OnRequest(event.Request.URL)
			})()
		}
	}

	page = page.Timeout(timeout)
	return action(page)
}

// Session is a long-lived browser + stealth page kept open across calls, for the
// rare flow that benefits from REUSING cookies (MakerWorld's geetest cookies are
// domain-wide, so one session can resolve many downloads without reloading a
// page each time). This is the deliberate exception to WithPage's "fresh every
// time" rule, so the caller owns the lifecycle and MUST call Close - a Session
// holds one browser slot until then. No per-page timeout is set here; callers
// scope each operation with page.Timeout(...).
type Session struct {
	page     *rod.Page
	browser  *rod.Browser
	launcher *launcher.Launcher
	release  func()
	closed   bool
}

// OpenSession launches a browser + stealth page and returns it as a Session. It
// blocks for a free slot (ErrBusy after slotWait). The caller must Close it.
func (runner *Runner) OpenSession() (session *Session, failure error) {
	select {
	case runner.slots <- struct{}{}:
	case <-time.After(slotWait):
		return nil, ErrBusy
	}
	release := func() { <-runner.slots }
	defer func() {
		if failure != nil {
			release()
		}
	}()

	browserLauncher := runner.newLauncher().
		// Anti-detection hardening for the MakerWorld GeeTest flow, which is the
		// only caller of OpenSession. Blink's AutomationControlled feature is what
		// exposes navigator.webdriver at engine level - go-rod/stealth only patches
		// that property from JavaScript, which a fingerprinter can detect. Deleting
		// enable-automation (rod sets it by default) removes the "controlled by
		// automated software" infobar and the bot flags that come with it. The new
		// headless mode runs the same code path as headful Chrome rather than the
		// old, separately maintained one.
		Set("disable-blink-features", "AutomationControlled").
		Delete("enable-automation").
		HeadlessNew(true)
	controlURL, failure := browserLauncher.Launch()
	if failure != nil {
		return nil, fmt.Errorf("launch chromium: %w", failure)
	}
	rodBrowser := rod.New().ControlURL(controlURL)
	if failure = rodBrowser.Connect(); failure != nil {
		browserLauncher.Cleanup()
		return nil, fmt.Errorf("connect: %w", failure)
	}
	page, failure := stealth.Page(rodBrowser)
	if failure != nil {
		rodBrowser.Close()
		browserLauncher.Cleanup()
		return nil, fmt.Errorf("stealth page: %w", failure)
	}
	if runner.OnRequest != nil {
		if (proto.NetworkEnable{}).Call(page) == nil {
			go page.EachEvent(func(event *proto.NetworkRequestWillBeSent) {
				runner.OnRequest(event.Request.URL)
			})()
		}
	}
	return &Session{page: page, browser: rodBrowser, launcher: browserLauncher, release: release}, nil
}

// Page returns the session's live page. Scope each operation with
// page.Timeout(...) - the page itself carries no deadline.
func (session *Session) Page() *rod.Page { return session.page }

// Close tears the session down and frees its slot. Safe to call more than once;
// a dead page/browser only produces a recovered panic, never a crash.
func (session *Session) Close() {
	if session == nil || session.closed {
		return
	}
	session.closed = true
	func() { defer func() { _ = recover() }(); session.page.Close() }()
	func() { defer func() { _ = recover() }(); session.browser.Close() }()
	session.launcher.Cleanup()
	session.release()
}
