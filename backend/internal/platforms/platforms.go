// Package platforms defines the common interface of the platform downloaders and
// the saving of a result into the library. The downloaders reach their platform
// over plain HTTP, through Tor, or through a headless browser.
package platforms

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"meshdepot/internal/storage"
)

// applyPlaywrightAuth attaches the shared bearer token for the Firefox resolver.
// The entrypoint generates one at startup, so an unset token is a no-op for
// callers pointing PLAYWRIGHT_URL at their own resolver.
func applyPlaywrightAuth(request *http.Request) {
	if token := os.Getenv("PLAYWRIGHT_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}

const resolverProbeInterval = 500 * time.Millisecond

// WaitForResolver blocks until the Firefox resolver answers on /health. The
// entrypoint starts it moments before the app, and node has to load Playwright
// first, so a short wait is normal.
func WaitForResolver(baseURL string, timeout time.Duration) error {
	// Empty means someone overrode PLAYWRIGHT_URL with nothing; say so rather than
	// spending the whole timeout on it.
	if strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("no resolver URL configured (PLAYWRIGHT_URL is empty)")
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/health"
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)

	var lastFailure error
	for {
		request, failure := http.NewRequest(http.MethodGet, endpoint, nil)
		if failure != nil {
			return fmt.Errorf("bad resolver URL %q: %w", baseURL, failure)
		}
		applyPlaywrightAuth(request)

		response, failure := client.Do(request)
		if failure == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
			// Retrying cannot fix a token mismatch.
			if response.StatusCode == http.StatusUnauthorized {
				return fmt.Errorf("resolver rejected our token (HTTP 401) - PLAYWRIGHT_TOKEN mismatch")
			}
			lastFailure = fmt.Errorf("resolver answered HTTP %d", response.StatusCode)
		} else {
			lastFailure = failure
		}

		if time.Now().After(deadline.Add(-resolverProbeInterval)) {
			return fmt.Errorf("no answer from %s within %s: %w", endpoint, timeout, lastFailure)
		}
		time.Sleep(resolverProbeInterval)
	}
}

type Credentials struct {
	Email    string
	Password string
	Token    string
	TOTP     string
}

// Validator is implemented by downloaders that can check credentials without
// saving. The rest are save-only.
type Validator interface {
	Validate(credentials Credentials) bool
}

// ReasonValidator returns an i18n key instead of a bool, so the frontend can say
// which input was wrong. "" means valid.
type ReasonValidator interface {
	ValidateReason(credentials Credentials) string
}

type Platform struct {
	Name   string // internal key; also the value in designs.source_platform
	Label  string // display name
	Domain string // canonical hostname, used for URL detection

	// NeedsCredentials marks platforms whose downloads fail without a stored token,
	// so the API can reject a queue request early.
	NeedsCredentials bool

	// TokenExpiry is the SQLite date modifier applied to an auto-login token. Empty
	// means the platform has no auto-login.
	TokenExpiry string

	// autoLogin is nil for platforms whose token can only be entered by hand, and
	// set exactly when TokenExpiry is.
	autoLogin func(Deps, Credentials) string

	// newDownloader is nil for a platform that is not implemented.
	newDownloader func(Deps) Downloader
}

// All is the single source of truth: domains, labels, credential requirements,
// token lifetimes and the registry all derive from this table.
//
// Two lists outside this package are kept in sync by hand - the schema's CHECK
// constraints and frontend/src/constants/platforms.ts.
// TestPlatformTableMatchesExternalLists fails when they drift apart.
var All = []Platform{
	{Name: "thingiverse", Label: "Thingiverse", Domain: "thingiverse.com", NeedsCredentials: true,
		newDownloader: func(deps Deps) Downloader { return Thingiverse{deps} }},
	{Name: "printables", Label: "Printables", Domain: "printables.com", NeedsCredentials: true, TokenExpiry: "+28 days",
		newDownloader: func(deps Deps) Downloader { return Printables{deps} },
		autoLogin: func(_ Deps, credentials Credentials) string {
			return autoLoginPrintables(credentials.Email, credentials.Password)
		}},
	{Name: "makerworld", Label: "MakerWorld", Domain: "makerworld.com", NeedsCredentials: true, TokenExpiry: "+85 days",
		newDownloader: func(deps Deps) Downloader { return Makerworld{deps} },
		autoLogin: func(_ Deps, credentials Credentials) string {
			return makerworldAutoLogin(credentials.Email, credentials.Password, credentials.TOTP)
		}},
	{Name: "thangs", Label: "Thangs", Domain: "thangs.com", NeedsCredentials: true, TokenExpiry: "+60 days",
		newDownloader: func(deps Deps) Downloader { return Thangs{deps} },
		autoLogin: func(deps Deps, credentials Credentials) string {
			return Thangs{deps}.autoLogin(credentials.Email, credentials.Password)
		}},
	{Name: "cults3d", Label: "Cults3D", Domain: "cults3d.com", NeedsCredentials: true,
		newDownloader: func(deps Deps) Downloader { return Cults3D{deps} }},
	{Name: "myminifactory", Label: "MyMiniFactory", Domain: "myminifactory.com", NeedsCredentials: true,
		newDownloader: func(deps Deps) Downloader { return MyMiniFactory{deps} }},
}

func ByName(name string) (Platform, bool) {
	for _, platform := range All {
		if platform.Name == name {
			return platform, true
		}
	}
	return Platform{}, false
}

// Label returns the display name, or the raw key for unknown platforms such as
// "manual".
func Label(name string) string {
	if platform, ok := ByName(name); ok {
		return platform.Label
	}
	return name
}

// NeedsCredentials reports whether downloads from this platform need credentials.
func NeedsCredentials(name string) bool {
	platform, ok := ByName(name)
	return ok && platform.NeedsCredentials
}

// DetectPlatform matches the parsed hostname exactly, never as a substring: a
// substring check treats http://attacker.example/?x=cults3d.com as cults3d and
// hands the raw URL to the downloader, which dereferences it server-side - for
// Thangs with the user's auth token attached.
func DetectPlatform(rawURL string) string {
	host := hostOf(rawURL)
	if host == "" {
		return ""
	}
	for _, platform := range All {
		if host == platform.Domain || strings.HasSuffix(host, "."+platform.Domain) {
			return platform.Name
		}
	}
	return ""
}

// hostOf tolerates a missing scheme, since users paste "www.printables.com/…",
// and returns "" when no host can be determined.
func hostOf(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	parsed, failure := url.Parse(rawURL)
	if failure != nil || parsed.Host == "" {
		if parsed, failure = url.Parse("https://" + rawURL); failure != nil {
			return ""
		}
	}
	return strings.ToLower(parsed.Hostname())
}

type DownloadedFile struct {
	TempPath string // path of the downloaded file (removed after saving).
	Name     string // original/target name (may contain subfolders).
}

type Result struct {
	Name        string
	Description string
	Author      string
	SourceID    string
	CoverPath   string
	Files       []DownloadedFile
	Tags        []string
	AllImages   []string // image paths staged in the user's temp directory; SaveDownload/AddImages move them into the design
}

// Owner is the account a download runs for. It carries both identities: the
// numeric id is what crypto.Encrypt derives the credential key from, while every
// storage path is addressed by the public id. Resolved once where a job is
// claimed, because with SetMaxOpenConns(1) a lookup inside one of the open
// transactions in save.go is a deadlock.
type Owner struct {
	ID     int
	Layout storage.UserLayout
}

// Downloader downloads a design. progress feeds the SSE display (step label,
// current/total).
type Downloader interface {
	Download(sourceURL string, owner Owner, progress func(step, label string, current, total int)) (Result, error)
}

type Registry map[string]Downloader

func (registry Registry) Get(platform string) Downloader {
	if registry == nil {
		return nil
	}
	return registry[platform]
}

// New builds the registry from the platform table: a platform is available
// exactly when its entry has a constructor.
func New(deps Deps) Registry {
	registry := Registry{}
	for _, platform := range All {
		if platform.newDownloader != nil {
			registry[platform.Name] = platform.newDownloader(deps)
		}
	}
	return registry
}
