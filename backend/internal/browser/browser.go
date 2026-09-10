// Package browser runs rod-based headless Chromium sessions. go-rod/stealth
// supplies the base evasions; the platform-specific flows are built on top of it
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

// maxConcurrentBrowsers bounds the memory: each instance costs a few hundred MB
// against the container's 2 GiB, and every WithPage call starts its own.
const maxConcurrentBrowsers = 2

// slotWait is longer than a normal login flow takes and short enough that a
// wedged instance does not stall the whole queue.
const slotWait = 3 * time.Minute

var ErrBusy = errors.New("browser: all instances busy")

// Runner starts and stops browser instances, at most maxConcurrentBrowsers at a
// time. It keeps none around between calls: the logins it drives must not share
// cookies or fingerprints.
type Runner struct {
	chromiumBin string
	slots       chan struct{}

	// OnRequest is called with the URL of every request a page issues, so outbound
	// traffic can be counted without this package knowing about platforms. Called
	// from the page's event goroutine, so it must be safe for concurrent use.
	OnRequest func(rawURL string)
}

func New(chromiumBin string) *Runner {
	return &Runner{
		chromiumBin: chromiumBin,
		slots:       make(chan struct{}, maxConcurrentBrowsers),
	}
}

// newLauncher: the sandbox switches are needed because the container runs as
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

// WithPage starts a fresh browser with a stealth page, calls action and cleans
// up. Each call is its own subprocess, so a crash returns an error rather than
// killing the app. Callers block for a slot and get ErrBusy after slotWait.
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

	// A single model page pulls dozens of subresources, which is exactly the volume
	// the per-job cooldown never saw.
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

// Session is a browser and page kept open across calls, for the rare flow that
// benefits from reusing cookies - MakerWorld's GeeTest cookies are domain-wide,
// so one session resolves many downloads. The deliberate exception to WithPage's
// "fresh every time", so the caller owns the lifecycle and must Close it, and
// scopes each operation with page.Timeout(...).
type Session struct {
	page     *rod.Page
	browser  *rod.Browser
	launcher *launcher.Launcher
	release  func()
	closed   bool
}

// OpenSession blocks for a free slot (ErrBusy after slotWait). The caller must
// Close it.
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
		// Anti-detection for the MakerWorld GeeTest flow, the only caller of
		// OpenSession. Blink's AutomationControlled is what exposes navigator.webdriver
		// at engine level, which stealth only patches from JavaScript; deleting
		// enable-automation removes the infobar and its bot flags; and the new headless
		// mode runs the same code path as headful Chrome.
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

// Page carries no deadline - scope each operation with page.Timeout(...).
func (session *Session) Page() *rod.Page { return session.page }

// Close is safe to call more than once; a dead page only produces a recovered
// panic.
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
