package platforms

// Thangs downloader. The download flow is pure HTTP, no Tor. The login runs
// through the rod browser and captures the Authorization header of the
// thangs.com/api requests.

import (
	"encoding/json"
	"fmt"
	"html"
	"meshdepot/internal/coerce"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"meshdepot/internal/safego"
)

const chromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var (
	thangsModelIDPattern = regexp.MustCompile(`(?i)(?:model|3d-model)[^\d]*-?(\d+)(?:[^\d]|$)`)
	titlePattern         = regexp.MustCompile(`(?is)<title>([^<]+)</title>`)
	titleSuffixPattern   = regexp.MustCompile(`\s*[|\-].*$`)
)

type Thangs struct{ Deps }

func (thangs Thangs) Download(sourceURL string, owner Owner, progress func(step, label string, current, total int)) (Result, error) {
	// The numeric id is only for credentials; every path comes from owner.Layout.
	userID := owner.ID
	if progress == nil {
		progress = func(string, string, int, int) {}
	}

	match := thangsModelIDPattern.FindStringSubmatch(sourceURL)
	if match == nil {
		return Result{}, fmt.Errorf("Cannot extract Thangs model ID from URL: %s", sourceURL)
	}
	modelID := match[1]

	token := thangs.resolveToken(userID, progress)

	authHeaders := map[string]string{"User-Agent": chromeUserAgent}
	if token != "" {
		authHeaders["Authorization"] = token
	}

	progress("fetching_metadata", "", 0, 0)
	apiRaw, _ := directGet("https://thangs.com/api/files/"+modelID, authHeaders)
	var apiData struct {
		Results []map[string]any `json:"results"`
		Files   []map[string]any `json:"files"`
	}
	_ = json.Unmarshal(apiRaw, &apiData)

	htmlBody, _ := directGet(sourceURL, authHeaders)
	name := "Thangs #" + modelID
	if titleMatch := titlePattern.FindStringSubmatch(string(htmlBody)); titleMatch != nil {
		name = html.UnescapeString(strings.TrimSpace(titleSuffixPattern.ReplaceAllString(titleMatch[1], "")))
	}

	fileEntries := apiData.Results
	if len(fileEntries) == 0 {
		fileEntries = apiData.Files
	}

	// Neither a real title nor a file list - no model info found at all.
	if name == "Thangs #"+modelID && len(fileEntries) == 0 {
		return Result{}, MetadataError("Thangs model %s: no metadata or file list returned "+
			"(model may be private, deleted, or login required).", modelID)
	}

	tempDir, failure := makeTempDir(owner.Layout)
	if failure != nil {
		return Result{}, failure
	}

	var files []DownloadedFile
	total := len(fileEntries)
	progress("downloading_files", "", 0, total)
	for index, fileEntry := range fileEntries {
		progress("downloading_files", "", index+1, total)
		downloadURL := coerce.Text(fileEntry["downloadUrl"])
		if downloadURL == "" {
			downloadURL = coerce.Text(fileEntry["download_url"])
		}
		fileName := coerce.Text(fileEntry["filename"])
		if fileName == "" {
			fileName = coerce.Text(fileEntry["name"])
		}
		if fileName == "" {
			fileName = fmt.Sprintf("file_%d.stl", len(files))
		}
		if downloadURL == "" {
			continue
		}

		tempPath := filepath.Join(tempDir, sanitizeFileName(fileName))
		if !downloadFileTo(downloadClient(), downloadURL, tempPath, authHeaders) {
			continue
		}
		files = append(files, DownloadedFile{TempPath: tempPath, Name: fileName})
	}

	if len(files) == 0 {
		if token == "" {
			return Result{}, fmt.Errorf("error.thangs_no_token:Thangs requires authentication for downloads. " +
				"Get your token via F12 → Network → any thangs.com API request → Authorization header. " +
				"Then add it in Account Settings → Platforms → Thangs.")
		}
		return Result{}, FilesError("Could not download files from Thangs model %s", modelID)
	}

	return Result{
		Name:     name,
		SourceID: modelID,
		Files:    files,
	}, nil
}

func (thangs Thangs) Validate(credentials Credentials) bool {
	return credentials.Email != "" && credentials.Password != "" && thangs.autoLogin(credentials.Email, credentials.Password) != ""
}

// resolveToken returns the stored token, or a fresh one from the browser login
// when it is missing or expired, persisted for 60 days.
func (thangs Thangs) resolveToken(userID int, progress func(string, string, int, int)) string {
	token, _ := thangs.Deps.resolveToken(userID, "thangs", progress)
	return token
}

// autoLogin returns the captured token including the "Bearer " prefix, or "".
func (thangs Thangs) autoLogin(email, password string) string {
	if thangs.Browser == nil {
		return ""
	}
	var (
		mutex    sync.Mutex
		captured string
	)
	setToken := func(value string) {
		mutex.Lock()
		if captured == "" && len(value) > 20 {
			captured = value
		}
		mutex.Unlock()
	}
	getToken := func() string {
		mutex.Lock()
		defer mutex.Unlock()
		return captured
	}

	_ = thangs.Browser.WithPage(90*time.Second, func(page *rod.Page) error {
		_ = proto.NetworkEnable{}.Call(page)
		_ = proto.NetworkSetUserAgentOverride{UserAgent: chromeUserAgent}.Call(page)

		// Own goroutine: WithPage's recover does not reach it.
		safego.Go("thangs.header-sniffer", page.EachEvent(func(event *proto.NetworkRequestWillBeSent) {
			if !strings.Contains(event.Request.URL, "thangs.com/api") {
				return
			}
			for key, value := range event.Request.Headers {
				if strings.EqualFold(key, "authorization") {
					setToken(value.Str())
				}
			}
		}))

		// Thangs abolished its /login page - it now lands on a designer profile. The
		// login is a header button that opens a modal, so load the home page and click.
		if failure := page.Navigate("https://thangs.com/"); failure != nil {
			return nil
		}
		_ = page.WaitLoad()

		// The visible "Log in" button has no href and only opens the modal.
		// Social-login buttons are excluded so no OAuth popup opens.
		_, _ = page.Eval(`() => {
			const els = [...document.querySelectorAll('a,button')];
			const el = els.find(e => {
				const t = (e.textContent || '').trim();
				return /^(log\s*in|sign\s*in)$/i.test(t) && e.offsetParent !== null;
			});
			if (el) el.click();
			return !!el;
		}`)

		// Wait for the modal: the email field appears.
		emailElement, failure := page.Timeout(15 * time.Second).Element(`input[type="email"], input[name="email"], input[placeholder*="email" i]`)
		if failure != nil {
			return nil
		}
		_ = emailElement.Input(email)
		if passwordElement, failure := page.Element(`input[type="password"], input[name="password"]`); failure == nil {
			_ = passwordElement.Input(password)
		}
		// The submit button inside the password form, not the social-login ones.
		_, _ = page.Eval(`() => {
			const pw = document.querySelector('input[type="password"]');
			const form = pw && pw.closest('form');
			let btn = form && (form.querySelector('button[type="submit"]') || form.querySelector('button'));
			if (!btn) {
				btn = [...document.querySelectorAll('button')].find(b => /^log\s*in$/i.test((b.textContent||'').trim()));
			}
			if (btn) btn.click();
			return !!btn;
		}`)

		// Let it settle: the modal closes and the API requests we read start.
		page.Timeout(15 * time.Second).WaitNavigation(proto.PageLifecycleEventNameNetworkIdle)()
		time.Sleep(3 * time.Second)

		// Fallback 1: token from localStorage.
		if getToken() == "" {
			if storageValue, failure := page.Eval(`() => { for (const k of Object.keys(localStorage)) { const v = localStorage.getItem(k); if (v && (k.toLowerCase().includes('token') || k.toLowerCase().includes('auth'))) return v; } return null; }`); failure == nil {
				if value := storageValue.Value.Str(); value != "" {
					if !strings.HasPrefix(value, "Bearer ") {
						value = "Bearer " + value
					}
					setToken(value)
				}
			}
		}

		// Fallback 2: trigger an authenticated API request.
		if getToken() == "" {
			_ = page.Navigate("https://thangs.com/api/v2/me")
			_ = page.WaitLoad()
			time.Sleep(2 * time.Second)
		}
		return nil
	})

	return getToken()
}
