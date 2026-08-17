// Package platforms defines the common interface of the 6 platform downloaders
// and the saving of a download result into the library. The concrete downloaders
// reach their platform over plain HTTP, through Tor, or through a headless
// browser, depending on what that platform's anti-bot measures allow.
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

// applyPlaywrightAuth attaches the shared bearer token to a request bound for
// the Firefox resolver. The container entrypoint generates one at startup and
// exports it to both processes, so it is normally always set; an unset token
// stays a no-op for callers pointing PLAYWRIGHT_URL at their own resolver.
func applyPlaywrightAuth(request *http.Request) {
	if token := os.Getenv("PLAYWRIGHT_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}

const resolverProbeInterval = 500 * time.Millisecond

// WaitForResolver blocks until the Firefox resolver answers on /health, or until
// timeout expires. The entrypoint starts it moments before the app, so a short
// wait is normal - node has to load Playwright first.
func WaitForResolver(baseURL string, timeout time.Duration) error {
	// Empty means someone overrode the image's PLAYWRIGHT_URL with nothing.
	// Say so straight away instead of spending the whole timeout on it.
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

// Credentials bundles the access data to be checked (validator).
type Credentials struct {
	Email    string
	Password string
	Token    string
	TOTP     string
}

// Validator is implemented by downloaders that can check entered credentials
// without saving. Downloaders without a validator are considered "save-only".
type Validator interface {
	Validate(credentials Credentials) bool
}

// ReasonValidator additionally returns an i18n error key instead of only a bool,
// so the frontend can distinguish which input was wrong (token vs. username). A
// return of "" means valid.
type ReasonValidator interface {
	ValidateReason(credentials Credentials) string
}

type Platform struct {
	Name   string // internal key; also the value in designs.source_platform
	Label  string // display name
	Domain string // canonical hostname, used for URL detection

	// NeedsCredentials marks platforms whose downloads fail without a stored
	// token or login, so the API can reject a queue request early.
	NeedsCredentials bool

	// TokenExpiry is the SQLite date modifier applied to a token obtained by
	// auto-login. Empty means the platform has no auto-login.
	TokenExpiry string

	// autoLogin obtains a fresh token from stored credentials; nil for the
	// platforms whose token can only be entered by hand. Set exactly when
	// TokenExpiry is - resolveToken persists what this returns.
	autoLogin func(Deps, Credentials) string

	// newDownloader builds the downloader; nil means "not (yet) implemented".
	newDownloader func(Deps) Downloader
}

// All is the single source of truth for the supported platforms. Domains,
// labels, credential requirements, token lifetimes and the registry all derive
// from this table - previously each of those lived in its own hardcoded list,
// and adding a platform meant finding all of them.
//
// Two lists outside this package must be kept in sync manually; the schema's
// CHECK constraints (db/schema.sql) and the frontend constants
// (frontend/src/constants/platforms.ts). TestPlatformTableMatchesExternalLists
// fails when they drift apart.
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

// ByName looks a platform up by its internal key.
func ByName(name string) (Platform, bool) {
	for _, platform := range All {
		if platform.Name == name {
			return platform, true
		}
	}
	return Platform{}, false
}

// Label returns the display name of a platform, or the raw key for unknown ones
// (e.g. "manual", or a platform removed from the table while rows still exist).
func Label(name string) string {
	if platform, ok := ByName(name); ok {
		return platform.Label
	}
	return name
}

// NeedsCredentials reports whether downloads from this platform require stored
// credentials.
func NeedsCredentials(name string) bool {
	platform, ok := ByName(name)
	return ok && platform.NeedsCredentials
}

// DetectPlatform recognizes the platform from the URL's actual hostname (or "").
//
// The hostname is matched exactly (domain or subdomain) - NOT via a substring
// search. A substring check treats a URL like `http://attacker.example/?x=cults3d.com`
// as cults3d and hands the raw URL to the downloader, which then dereferences it
// server-side (SSRF) - for Thangs even with the user's auth token attached. By
// anchoring on the parsed host, only URLs genuinely served by the platform reach
// a downloader.
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

// hostOf extracts the lower-cased hostname from a URL. A missing scheme is
// tolerated (users paste "www.printables.com/model/1"): the string is retried
// with an "https://" prefix. Returns "" when no host can be determined.
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

// DownloadedFile references a downloaded temp file + target name.
type DownloadedFile struct {
	TempPath string // path of the downloaded file (removed after saving).
	Name     string // original/target name (may contain subfolders).
}

// Result is the outcome of a platform download.
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

// Owner is the account a download runs for. It carries both identities on
// purpose: the numeric id is what crypto.Encrypt derives the credential key
// from (internal/crypto), so it has to stay numeric, while every storage path is
// addressed by the public id. It is resolved once where a job is claimed - the
// database runs on a single connection (SetMaxOpenConns(1)), which makes a
// lookup inside one of the open transactions in save.go a deadlock.
type Owner struct {
	ID     int
	Layout storage.UserLayout
}

// Downloader downloads a design from a platform. progress can be used for SSE
// progress (step label, current/total).
type Downloader interface {
	Download(sourceURL string, owner Owner, progress func(step, label string, current, total int)) (Result, error)
}

// Registry holds the downloaders per platform.
type Registry map[string]Downloader

// Get returns the downloader of a platform (or nil).
func (registry Registry) Get(platform string) Downloader {
	if registry == nil {
		return nil
	}
	return registry[platform]
}

// New builds the registry from the platform table - a platform is available
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
