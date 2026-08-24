package platforms

// MakerWorld downloader. Login runs purely over the Bambu Lab API (optional TOTP
// 2FA, no browser, no Tor). Metadata comes from the public design service. The
// f3mf download URLs are geetest-gated and are therefore fetched by the
// Playwright/Firefox sidecar (POST /resolve-makerworld), which holds a warm
// browser with the GeeTest cookies; the presigned CDN URLs it returns are then
// loaded directly by the server. No browser is driven from this process.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

var (
	modelIDPattern   = regexp.MustCompile(`(?i)models/(\d+)`)
	profileIDPattern = regexp.MustCompile(`(?i)profileId-(\d+)`)

	// tokenCookiePattern picks the bearer token out of a Set-Cookie line.
	tokenCookiePattern = regexp.MustCompile(`(?i)^token=([^;]+)`)

	// Bambu hosts. Variables rather than constants so tests can point the login
	// flow at a local server instead of the live service.
	bambuAPIHost = "https://api.bambulab.com"
	bambuWebHost = "https://bambulab.com"
)

// bambuBrowserUserAgent is the UA sent to the website host, matching the TLS
// fingerprint the browser session impersonates.
const bambuBrowserUserAgent = "Mozilla/5.0 (X11; Linux x86_64; rv:117.0) Gecko/20100101 Firefox/117.0"

// Makerworld implements Downloader for makerworld.com.
type Makerworld struct{ Deps }

// makerWorldInstance is a downloadable instance of a MakerWorld design.
type makerWorldInstance struct {
	id    string
	title string
}

// resolvedFile is a presigned download URL resolved by the sidecar.
type resolvedFile struct {
	name string
	url  string
}

// Download loads a MakerWorld model (login via Bambu API, files via browser).
func (makerworld Makerworld) Download(sourceURL string, owner Owner, progress func(step, label string, current, total int)) (Result, error) {
	// The numeric id is only for credentials; every path comes from owner.Layout.
	userID := owner.ID
	if progress == nil {
		progress = func(string, string, int, int) {}
	}

	match := modelIDPattern.FindStringSubmatch(sourceURL)
	if match == nil {
		return Result{}, fmt.Errorf("error.unsupported_url:Cannot extract MakerWorld model ID from URL: %s", sourceURL)
	}
	modelID := match[1]
	instanceID := ""
	if profileMatch := profileIDPattern.FindStringSubmatch(sourceURL); profileMatch != nil {
		instanceID = profileMatch[1]
	}

	token, loginFailed := makerworld.resolveToken(userID, progress)
	if loginFailed {
		return Result{}, fmt.Errorf("error.makerworld_auth_failed:MakerWorld login failed. " +
			"Check your credentials in Account Settings → Platforms → MakerWorld.")
	}
	if token == "" {
		return Result{}, fmt.Errorf("error.makerworld_no_token:MakerWorld requires authentication. " +
			"Add your Bambu Lab credentials in Account Settings → Platforms → MakerWorld.")
	}

	authHeaders := map[string]string{
		"Authorization": "Bearer " + token,
		"User-Agent":    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Referer":       "https://makerworld.com/",
	}

	// ── Metadata (not captcha-gated) with retry ────────────────────────────────
	progress("fetching_metadata", "", 0, 0)
	var meta struct {
		Title         string `json:"title"`
		Name          string `json:"name"`
		Summary       string `json:"summary"`
		DesignCreator struct {
			Name string `json:"name"`
		} `json:"designCreator"`
		CoverURL  string `json:"coverUrl"`
		Instances []struct {
			ID    json.Number `json:"id"`
			Title string      `json:"title"`
		} `json:"instances"`
		// Free-text tags plus the fixed category tree ("Sculptures", "Art"); both
		// are worth keeping, the categories are what the site itself filters by.
		Tags       []string `json:"tags"`
		Categories []struct {
			Name string `json:"name"`
		} `json:"categories"`
	}
	for attempt := 1; attempt <= 3; attempt++ {
		raw, _ := directGet("https://makerworld.com/api/v1/design-service/design/"+modelID, authHeaders)
		if json.Unmarshal(raw, &meta) == nil && (len(meta.Instances) > 0 || meta.Title != "" || meta.Name != "") {
			break
		}
	}

	// No model info at all → no point going into the file phase.
	if meta.Title == "" && meta.Name == "" && len(meta.Instances) == 0 {
		return Result{}, MetadataError("MakerWorld model %s: no metadata/instances returned "+
			"(model may be private, deleted, or login required).", modelID)
	}

	name := meta.Title
	if name == "" {
		name = meta.Name
	}
	if name == "" {
		name = "MakerWorld #" + modelID
	}

	// Choose instances: filter by profileId, otherwise all; fallback to the first.
	var instances []makerWorldInstance
	for _, instance := range meta.Instances {
		id := instance.ID.String()
		if instanceID != "" && id != instanceID {
			continue
		}
		instances = append(instances, makerWorldInstance{id: id, title: instance.Title})
	}
	if len(instances) == 0 && len(meta.Instances) > 0 {
		first := meta.Instances[0]
		instances = []makerWorldInstance{{id: first.ID.String(), title: first.Title}}
	}

	tempDir, failure := makeTempDir(owner.Layout)
	if failure != nil {
		return Result{}, failure
	}

	// ── Fetch f3mf presigned URLs from the browser context, then load directly ──
	progress("downloading_files", "", 0, len(instances))
	resolved, captcha := makerworld.resolvePresigned(modelID, instances, token)
	var files []DownloadedFile
	for index, resolvedFileEntry := range resolved {
		progress("downloading_files", "", index+1, len(resolved))
		if resolvedFileEntry.url == "" {
			continue
		}
		destination := filepath.Join(tempDir, resolvedFileEntry.name)
		if written, status := streamDownload(resolvedFileEntry.url, destination, nil); status < 400 && written >= minValidFileBytes {
			files = append(files, DownloadedFile{TempPath: destination, Name: resolvedFileEntry.name})
		}
	}

	if len(files) == 0 {
		if captcha {
			return Result{}, FilesError("error.makerworld_captcha:MakerWorld blocked the download of model %s with an anti-bot "+
				"captcha (GeeTest, HTTP 418). This is triggered by rapid/bulk downloads and usually clears after a cooldown. "+
				"Will be retried automatically.", modelID)
		}
		return Result{}, FilesError("error.makerworld_download_failed:Could not download files from MakerWorld model %s "+
			"(browser blocked or returned no files). Will be retried automatically.", modelID)
	}

	var coverPath string
	var allImages []string
	if meta.CoverURL != "" {
		progress("downloading_images", "", 0, 1)
		if storedPath := downloadCover(owner.Layout, meta.CoverURL); storedPath != "" {
			coverPath = storedPath
			allImages = []string{storedPath}
		}
	}

	tags := meta.Tags
	for _, category := range meta.Categories {
		tags = append(tags, category.Name)
	}

	return Result{
		Name:        name,
		Description: meta.Summary,
		Author:      meta.DesignCreator.Name,
		SourceID:    modelID,
		CoverPath:   coverPath,
		AllImages:   allImages,
		Tags:        uniqueStrings(tags),
		Files:       files,
	}, nil
}

// Validate checks email/password (+optional TOTP) via Bambu login.
func (makerworld Makerworld) Validate(credentials Credentials) bool {
	return makerworld.ValidateReason(credentials) == ""
}

// ValidateReason checks the credentials and returns a precise i18n key that
// distinguishes whether email/password or the 2FA seed is wrong ("" = ok).
func (makerworld Makerworld) ValidateReason(credentials Credentials) string {
	if credentials.Email == "" || credentials.Password == "" {
		return "error.makerworld_invalid_credentials"
	}
	_, reason := makerworldLogin(credentials.Email, credentials.Password, credentials.TOTP)
	return reason
}

// resolveToken returns a valid Bambu token: stored (decrypted) or, if
// missing/expired and credentials are on file, freshly fetched via Bambu login
// (then persisted encrypted + valid for 85 days). loginFailed=true when a login
// attempt failed.
func (makerworld Makerworld) resolveToken(userID int, progress func(string, string, int, int)) (token string, loginFailed bool) {
	return makerworld.Deps.resolveToken(userID, "makerworld", progress)
}

// makerworldLogin logs in to the Bambu Lab API (optional TOTP 2FA) and returns
// the bearer token plus an i18n reason key that, on failure, distinguishes
// whether email/password or the 2FA seed is wrong ("" = success).
func makerworldLogin(email, password, totpSecret string) (token, reason string) {
	// Deliberately do NOT set Accept-Encoding - Go only decompresses gzip when it
	// adds the header itself (equivalent to CURLOPT_ENCODING => '').
	orcaHeaders := map[string]string{
		"Content-Type":      "application/json",
		"User-Agent":        "bambu_network_agent/01.09.05.01",
		"X-BBL-Client-Name": "OrcaSlicer",
		"X-BBL-Client-Type": "slicer",
	}

	// Step 1: initial login (api.bambulab.com - not CF-protected).
	loginBody, _ := json.Marshal(map[string]string{"account": email, "password": password, "apiError": ""})
	loginRaw, _ := directPost(bambuAPIHost+"/v1/user-service/user/login", string(loginBody), orcaHeaders)
	var loginData struct {
		AccessToken string `json:"accessToken"`
		LoginType   string `json:"loginType"`
		TfaKey      string `json:"tfaKey"`
	}
	_ = json.Unmarshal(loginRaw, &loginData)
	if loginData.AccessToken != "" {
		return loginData.AccessToken, ""
	}
	// No token and no tfa flow → Bambu rejected email/password.
	if loginData.TfaKey == "" || loginData.LoginType == "" {
		return "", "error.makerworld_invalid_credentials"
	}
	// tfa flow present, but not the supported authenticator type (e.g. loginType
	// "verifyCode" = email code) → not supported.
	if loginData.LoginType != "tfa" {
		return "", "error.makerworld_2fa_unsupported"
	}
	// Password is correct (Bambu requires 2FA), but no 2FA seed is on file.
	if totpSecret == "" {
		return "", "error.makerworld_totp_required"
	}

	// Step 2: TOTP verification on bambulab.com (website host). It is protected by
	// Cloudflare "Managed Challenge" (403 "Just a moment" for the Go standard
	// client). A client with a real Firefox TLS fingerprint (JA3) passes the
	// challenge - hence the browser session instead of rawRequest.
	session, failure := newBrowserSession()
	if failure != nil {
		return "", "error.makerworld_login_unavailable"
	}

	// Step 2a: CSRF handshake. The endpoint uses a double-submit scheme - this GET
	// sets the bbl_csrf_token cookie and the POST below has to repeat its value in
	// the X-BBL-CSRF-Token header. Without the pair it answers 403 "CSRF error"
	// and never looks at the code.
	session.do(fhttp.MethodGet, bambuWebHost+"/api/csrf", "", map[string]string{
		"User-Agent": bambuBrowserUserAgent,
		"Accept":     "*/*",
		"Referer":    bambuWebHost + "/en/sign-in",
	})
	csrfToken := session.cookie(bambuWebHost, "bbl_csrf_token")
	if csrfToken == "" {
		return "", "error.makerworld_login_unavailable"
	}

	// Step 2b: the verification itself, cookie carried by the session's jar.
	code := generateTotp(totpSecret)
	tfaBody, _ := json.Marshal(map[string]string{"tfaKey": loginData.TfaKey, "tfaCode": code})
	tfaHeaders := map[string]string{
		"Content-Type":     "application/json",
		"User-Agent":       bambuBrowserUserAgent,
		"Accept":           "*/*",
		"Origin":           bambuWebHost,
		"Referer":          bambuWebHost + "/en/sign-in",
		"X-BBL-CSRF-Token": csrfToken,
	}
	status, setCookies, responseBody := session.do(fhttp.MethodPost, bambuWebHost+"/api/sign-in/tfa", string(tfaBody), tfaHeaders)

	// Prefer the token from the Set-Cookie "token=".
	for _, setCookie := range setCookies {
		if cookieMatch := tokenCookiePattern.FindStringSubmatch(strings.TrimSpace(setCookie)); cookieMatch != nil {
			return strings.TrimSpace(cookieMatch[1]), ""
		}
	}
	// Fallback: token in the body.
	var tfaData struct {
		Token       string `json:"token"`
		AccessToken string `json:"accessToken"`
	}
	_ = json.Unmarshal(responseBody, &tfaData)
	if tfaData.Token != "" {
		return tfaData.Token, ""
	}
	if tfaData.AccessToken != "" {
		return tfaData.AccessToken, ""
	}
	// No token. Only an app-level rejection means the seed is wrong; a transport
	// failure, a broken handshake or a rate limit are our problem, not the user's,
	// and must not send them hunting for a bad seed. Anything unknown therefore
	// falls through to "blocked" rather than blaming the seed.
	switch {
	case status == 0:
		return "", "error.makerworld_login_unavailable"
	case status == 429:
		return "", "error.makerworld_rate_limited"
	case status == 400 || status == 401:
		return "", "error.makerworld_invalid_totp"
	default:
		return "", "error.makerworld_login_blocked"
	}
}

// makerworldAutoLogin returns only the token (reason discarded) - for token
// acquisition during download.
func makerworldAutoLogin(email, password, totpSecret string) string {
	token, _ := makerworldLogin(email, password, totpSecret)
	return token
}

// mwSidecarTimeout covers a cold sidecar: the first call there starts Firefox
// and does the GeeTest warm-up visit before it can answer at all.
const mwSidecarTimeout = 180 * time.Second

// mwSidecarResponse is what POST /resolve-makerworld answers with. Status and
// body are the f3mf call's own, passed through unchanged so the captcha
// detection below stays the same one the in-process browser used; url is the
// sidecar's parsed convenience field.
type mwSidecarResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	URL    string `json:"url"`

	// BotWall is set when the sidecar was served a Cloudflare bot-verification
	// page ("Just a moment") instead of MakerWorld. Reported, never solved.
	BotWall bool `json:"botWall"`
}

// mwResolveOne asks the sidecar for one instance's presigned f3mf URL. The
// browser lives in the sidecar, which keeps a warm Firefox with the domain-wide
// GeeTest cookies, so consecutive instances cost one navigation each instead of
// a browser start. reachable=false means the sidecar itself did not answer,
// which is retryable; a captcha is not.
func (makerworld Makerworld) mwResolveOne(modelID, instanceID, token string) (parsed mwSidecarResponse, reachable bool) {
	requestBody, _ := json.Marshal(map[string]any{
		"modelID":    modelID,
		"instanceID": instanceID,
		"token":      token,
	})
	endpoint := strings.TrimRight(makerworld.Cfg.PlaywrightURL, "/") + "/resolve-makerworld"
	request, failure := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if failure != nil {
		return mwSidecarResponse{}, false
	}
	request.Header.Set("Content-Type", "application/json")
	applyPlaywrightAuth(request)

	client := &http.Client{Timeout: mwSidecarTimeout}
	response, failure := client.Do(request)
	if failure != nil {
		log.Printf("[mw-dl] model=%s instance=%s: sidecar unreachable: %v", modelID, instanceID, failure)
		return mwSidecarResponse{}, false
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		log.Printf("[mw-dl] model=%s instance=%s: sidecar answered HTTP %d", modelID, instanceID, response.StatusCode)
		return mwSidecarResponse{}, false
	}
	if json.NewDecoder(response.Body).Decode(&parsed) != nil {
		log.Printf("[mw-dl] model=%s instance=%s: sidecar sent no usable JSON", modelID, instanceID)
		return mwSidecarResponse{}, false
	}
	return parsed, true
}

// resolvePresigned resolves the presigned f3mf URL per instance through the
// Playwright/Firefox sidecar (POST /resolve-makerworld). The f3mf endpoint is
// GeeTest-gated and answers 418 to anything outside a browser, so the call is
// made from the sidecar's page context; the returned CDN URL is then streamed by
// this process directly.
//
// A sidecar that does not answer is retried once. A captcha is not retried: it
// means the cookies are flagged, and one 418 blocks the whole run.
func (makerworld Makerworld) resolvePresigned(modelID string, instances []makerWorldInstance, token string) (files []resolvedFile, captcha bool) {
	if makerworld.Cfg.PlaywrightURL == "" {
		log.Printf("[mw-dl] model=%s: no resolver configured (PLAYWRIGHT_URL is empty)", modelID)
		return nil, false
	}

	// attempt runs one pass over all instances. unreachable=true means the
	// sidecar did not answer, so the caller retries the pass once.
	attempt := func() (found []resolvedFile, blocked, unreachable bool) {
		for _, instance := range instances {
			if instance.id == "" {
				continue
			}
			name := sanitizeFileName(instance.title)
			if name == "" {
				name = "instance_" + instance.id
			}
			name += ".3mf"

			parsed, reachable := makerworld.mwResolveOne(modelID, instance.id, token)
			if !reachable {
				return found, false, true
			}
			if parsed.BotWall {
				log.Printf("[mw-dl] STOP: MakerWorld served a Cloudflare bot-check (\"Just a moment\"). " +
					"The automated headless download cannot get past this - it is a bot-verification, " +
					"not something the app solves. Download it manually in a normal browser, or run from " +
					"a non-flagged network.")
				return found, true, false // blocked, not a dead sidecar: do not retry-storm
			}
			if parsed.Status == 418 ||
				strings.Contains(parsed.Body, "captchaId") ||
				strings.Contains(parsed.Body, "not a robot") {
				log.Printf("[mw-dl] model=%s instance=%s: MakerWorld captcha (HTTP %d)", modelID, instance.id, parsed.Status)
				return found, true, false // one 418 blocks the whole run
			}
			if parsed.URL != "" {
				found = append(found, resolvedFile{name: name, url: parsed.URL})
			} else {
				log.Printf("[mw-dl] model=%s instance=%s: no URL (HTTP %d, body=%.160s)",
					modelID, instance.id, parsed.Status, parsed.Body)
			}
		}
		return found, false, false
	}

	var unreachable bool
	files, captcha, unreachable = attempt()
	if unreachable {
		files, captcha, _ = attempt()
	}
	return files, captcha
}
