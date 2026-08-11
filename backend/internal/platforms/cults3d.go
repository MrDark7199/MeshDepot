package platforms

// Cults3D downloader. Metadata comes from the public HTML (OpenGraph, JSON-LD,
// gallery); the files are fetched via the rod browser: login → order flow
// (already ordered / open_priced cart with amount 0 / free_order / direct free
// order) → /downloaden/ links. Files are collected both from browser downloads
// and from captured file responses; ZIPs are extracted.

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"meshdepot/internal/coerce"
	"meshdepot/internal/dbutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"meshdepot/internal/safego"
)

const cults3dUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"

var (
	ldJSONPattern       = regexp.MustCompile(`(?is)<script[^>]+type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	galleryImagePattern = regexp.MustCompile(`(?i)https://(?:images\.cults3d\.com/[^\s"'<>]+|fbi\.cults3d\.com/[^\s"'<>]+\.(?:jpe?g|png|webp|gif))`)
	tagLinkPattern      = regexp.MustCompile(`(?i)href=["'][^"']*?/[a-z]{2}/tags/([^/"'?#]+)["']`)
	categoryPattern     = regexp.MustCompile(`(?i)cults3d\.com/[^/]+/(?:3d-model|3d-modell|modell-3d|mod[eè]le-3d)/([^/"?#]+)/`)
	userLinkPattern     = regexp.MustCompile(`cults3d\.com/[^/]+/(?:utilisateurs|users)/([^/"<\s]+)`)
	emojiPrefixPattern  = regexp.MustCompile(`\p{So}+\s*`)
	nameSuffixPattern   = regexp.MustCompile(`\s*[・|]\s*.+$`)
	imgproxyRealPattern = regexp.MustCompile(`/(https://fbi\.cults3d\.com/.+)$`)
	downloadLinkPattern = regexp.MustCompile(`(?i)/(?:downloaden|downloads?)/\d+`)
	orderLinkPattern    = regexp.MustCompile(`(?i)/(?:bestellungen|orders|commandes)/\d+$`)
	orderAnyPattern     = regexp.MustCompile(`(?i)/(?:bestellungen|orders|commandes)/\d+`)
)

// Cults3D implements Downloader for cults3d.com.
type Cults3D struct{ Deps }

// capturedFile is a file collected by the browser.
type capturedFile struct {
	name string
	data []byte
}

// Download loads a Cults3D model (metadata via HTML, files via browser).
func (cults3d Cults3D) Download(sourceURL string, owner Owner, progress func(step, label string, current, total int)) (Result, error) {
	// The numeric id is only for credentials; every path comes from owner.Layout.
	userID := owner.ID
	if progress == nil {
		progress = func(string, string, int, int) {}
	}

	modelSlug := ""
	if parsed, failure := url.Parse(sourceURL); failure == nil {
		modelSlug = filepath.Base(strings.TrimRight(parsed.Path, "/"))
	}

	// A password that cannot be decrypted (app key changed) is indistinguishable
	// from none at all here, and both are fixed the same way: re-enter it.
	account := cults3d.loadAccount(userID, "cults3d")
	if !account.hasLogin() {
		return Result{}, fmt.Errorf("error.cults3d_restricted:Could not download files from this Cults3D model. " +
			"Add your Cults3D email + password in Account Settings → Platforms → Cults3D for auto-login.")
	}
	username, password := account.Username, account.Password

	progress("authenticating", "", 0, 0)
	progress("fetching_metadata", "", 0, 0)
	htmlBody, _ := directGet(sourceURL, map[string]string{"User-Agent": cults3dUserAgent})
	meta := parseCults3dMetadata(string(htmlBody), sourceURL)

	progress("downloading_files", "", 0, 0)

	// Preferred: if the model is ordered (bought or obtained for free), the
	// GraphQL API returns the ready download URL - we skip the name-your-price/
	// cart detour. The file itself is fetched by the Firefox resolver (web session
	// needed; rod-Chromium is blocked by Cloudflare during login).
	var captured []capturedFile
	if nickname, apiKey := cults3d.cults3dAPICreds(userID); nickname != "" && apiKey != "" {
		if downloadURL := cults3dOrderDownloadURL(nickname, apiKey, modelSlug); downloadURL != "" {
			cookieJar := cults3d.cults3dCookieJar(userID)

			// Fastest path: direct HTTP GET with the cached cookie jar
			// (cf_clearance + _session_id) - no browser. Works as long as the
			// clearance is valid.
			if cookieJar != "" {
				captured = cults3dDirectDownload(cookieJar, downloadURL)
			}

			// Fallback: Firefox resolver (solves CF/login, reseeds the jar).
			if len(captured) == 0 && cults3d.Cfg.PlaywrightURL != "" {
				files, newJar := cults3dFirefoxDownload(cults3d.Cfg.PlaywrightURL, username, password, cookieJar, []string{downloadURL})
				captured = files
				if newJar != "" && newJar != cookieJar {
					cults3d.saveCults3dCookieJar(userID, newJar)
				}
			}
		}
	}

	// Fallback: full rod order flow (for un-ordered free models).
	if len(captured) == 0 {
		browserFiles, failure := cults3d.browserDownload(username, password, sourceURL)
		if failure != nil {
			return Result{}, FilesError("error.cults3d_auth_failed:Could not download files from Cults3D. %s", failure.Error())
		}
		captured = browserFiles
	}
	if len(captured) == 0 {
		return Result{}, FilesError("error.cults3d_auth_failed:Could not download files from Cults3D. " +
			"The model may be paid or member-only. Check your account in Account Settings → Platforms → Cults3D.")
	}

	tempDir, failure := makeTempDir(owner.Layout)
	if failure != nil {
		return Result{}, failure
	}

	files := saveCapturedFiles(tempDir, captured)
	if len(files) == 0 {
		return Result{}, FilesError("error.cults3d_auth_failed:Could not download files from Cults3D. " +
			"The model may be paid or member-only. Check your account in Account Settings → Platforms → Cults3D.")
	}

	// Resolve imgproxy URLs to direct CDN URLs + deduplicate.
	resolved := uniqueStrings(mapStrings(meta.imageURLs, resolveCults3dImageURL))
	progress("downloading_images", "", 0, len(resolved))
	allImages := downloadAllImages(owner.Layout, resolved, progress)
	var coverPath string
	if len(allImages) > 0 {
		coverPath = allImages[0]
	}

	return Result{
		Name:        meta.name,
		Description: meta.description,
		Author:      meta.author,
		SourceID:    modelSlug,
		CoverPath:   coverPath,
		AllImages:   allImages,
		Tags:        meta.tags,
		Files:       files,
	}, nil
}

// cults3dMeta bundles the metadata extracted from the HTML.
type cults3dMeta struct {
	name        string
	description string
	author      string
	tags        []string
	imageURLs   []string
}

// parseCults3dMetadata extracts name/description/author/tags/images from the
// public model HTML (OpenGraph, JSON-LD, gallery regex, tag links).
func parseCults3dMetadata(htmlBody, sourceURL string) cults3dMeta {
	openGraph := func(property string) string {
		pattern := regexp.MustCompile(`(?i)<meta[^>]+property=["']og:` + regexp.QuoteMeta(property) + `["'][^>]+content=["']([^"']+)["']`)
		if match := pattern.FindStringSubmatch(htmlBody); match != nil {
			return html.UnescapeString(match[1])
		}
		return ""
	}

	name := openGraph("title")
	if name == "" {
		name = "Cults3D model"
	}
	name = strings.TrimSpace(emojiPrefixPattern.ReplaceAllString(name, ""))
	name = strings.TrimSpace(nameSuffixPattern.ReplaceAllString(name, ""))

	description := openGraph("description")
	var imageURLs []string
	if cover := openGraph("image"); cover != "" {
		imageURLs = append(imageURLs, cover)
	}

	var author string
	var tags []string

	// JSON-LD: author/creator, keywords, image(s).
	for _, ldMatch := range ldJSONPattern.FindAllStringSubmatch(htmlBody, -1) {
		var raw any
		if json.Unmarshal([]byte(ldMatch[1]), &raw) != nil {
			continue
		}
		var items []map[string]any
		switch decoded := raw.(type) {
		case []any:
			for _, element := range decoded {
				if elementMap, ok := element.(map[string]any); ok {
					items = append(items, elementMap)
				}
			}
		case map[string]any:
			items = append(items, decoded)
		}
		for _, item := range items {
			if author == "" {
				if authorMap, ok := item["author"].(map[string]any); ok {
					author = coerce.Text(authorMap["name"])
				}
			}
			if author == "" {
				if creatorMap, ok := item["creator"].(map[string]any); ok {
					author = coerce.Text(creatorMap["name"])
				}
			}
			if len(tags) == 0 {
				switch keywords := item["keywords"].(type) {
				case string:
					for _, keyword := range strings.Split(keywords, ",") {
						if keyword = strings.TrimSpace(keyword); keyword != "" {
							tags = append(tags, keyword)
						}
					}
				case []any:
					for _, keyword := range keywords {
						if text := coerce.Text(keyword); text != "" {
							tags = append(tags, text)
						}
					}
				}
			}
			for _, imageURL := range jsonLDImages(item["image"]) {
				if imageURL != "" && !slices.Contains(imageURLs, imageURL) {
					imageURLs = append(imageURLs, imageURL)
				}
			}
		}
	}

	for _, galleryURL := range uniqueStrings(galleryImagePattern.FindAllString(htmlBody, -1)) {
		if !slices.Contains(imageURLs, galleryURL) {
			imageURLs = append(imageURLs, galleryURL)
		}
	}

	// Tags from /xx/tags/{tag} links.
	if len(tags) == 0 {
		for _, tagMatch := range tagLinkPattern.FindAllStringSubmatch(htmlBody, -1) {
			tag := html.UnescapeString(strings.TrimSpace(tagMatch[1]))
			if tag != "" && !slices.Contains(tags, tag) {
				tags = append(tags, tag)
			}
		}
	}
	// Category from the model URL.
	if len(tags) == 0 {
		if categoryMatch := categoryPattern.FindStringSubmatch(sourceURL); categoryMatch != nil {
			category := html.UnescapeString(strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(categoryMatch[1])))
			if category != "" {
				tags = []string{category}
			}
		}
	}
	// Author fallback from user links.
	if author == "" {
		if authorMatch := userLinkPattern.FindStringSubmatch(htmlBody); authorMatch != nil {
			author = html.UnescapeString(strings.TrimSpace(authorMatch[1]))
		}
	}

	return cults3dMeta{name: name, description: description, author: author, tags: tags, imageURLs: imageURLs}
}

// jsonLDImages normalizes the image field of a JSON-LD item to URL strings.
func jsonLDImages(value any) []string {
	var output []string
	switch image := value.(type) {
	case string:
		output = append(output, image)
	case map[string]any:
		output = append(output, coerce.Text(image["url"]))
	case []any:
		for _, element := range image {
			switch nestedElement := element.(type) {
			case string:
				output = append(output, nestedElement)
			case map[string]any:
				output = append(output, coerce.Text(nestedElement["url"]))
			}
		}
	}
	return output
}

// resolveCults3dImageURL extracts the direct CDN URL from an imgproxy URL.
func resolveCults3dImageURL(imageURL string) string {
	if match := imgproxyRealPattern.FindStringSubmatch(imageURL); match != nil {
		return match[1]
	}
	return imageURL
}

// saveCapturedFiles writes the collected files into tempDir; ZIPs are extracted.
// Returns the DownloadedFile list (Name = relative/original name).
func saveCapturedFiles(tempDir string, captured []capturedFile) []DownloadedFile {
	var files []DownloadedFile
	for _, capturedEntry := range captured {
		if len(capturedEntry.data) < minValidFileBytes {
			continue
		}
		if strings.EqualFold(filepath.Ext(capturedEntry.name), ".zip") {
			files = append(files, extractZip(tempDir, capturedEntry.data)...)
			continue
		}
		destination := filepath.Join(tempDir, sanitizeFileName(capturedEntry.name))
		if os.WriteFile(destination, capturedEntry.data, 0o664) == nil {
			files = append(files, DownloadedFile{TempPath: destination, Name: capturedEntry.name})
		}
	}
	return files
}

// extractZip extracts a ZIP archive (from memory) into tempDir and returns the
// contained files.
func extractZip(tempDir string, data []byte) []DownloadedFile {
	zipReader, failure := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if failure != nil {
		return nil
	}
	var files []DownloadedFile
	for _, zipFile := range zipReader.File {
		if zipFile.FileInfo().IsDir() {
			continue
		}
		readCloser, failure := zipFile.Open()
		if failure != nil {
			continue
		}
		destination := filepath.Join(tempDir, sanitizeFileName(filepath.Base(zipFile.Name)))
		output, failure := os.Create(destination)
		if failure != nil {
			readCloser.Close()
			continue
		}
		_, _ = io.Copy(output, readCloser)
		output.Close()
		readCloser.Close()
		if fileInfo, failure := os.Stat(destination); failure == nil && fileInfo.Size() > 0 {
			files = append(files, DownloadedFile{TempPath: destination, Name: filepath.Base(zipFile.Name)})
		}
	}
	return files
}

// extractZipFile extracts a ZIP from disk into tempDir and returns the contained
// files (for large archives, without keeping them in memory).
func extractZipFile(tempDir, zipPath string) []DownloadedFile {
	zipReader, failure := zip.OpenReader(zipPath)
	if failure != nil {
		return nil
	}
	defer zipReader.Close()
	var files []DownloadedFile
	for _, zipFile := range zipReader.File {
		if zipFile.FileInfo().IsDir() {
			continue
		}
		readCloser, failure := zipFile.Open()
		if failure != nil {
			continue
		}
		destination := filepath.Join(tempDir, sanitizeFileName(filepath.Base(zipFile.Name)))
		output, failure := os.Create(destination)
		if failure != nil {
			readCloser.Close()
			continue
		}
		_, _ = io.Copy(output, readCloser)
		output.Close()
		readCloser.Close()
		if fileInfo, failure := os.Stat(destination); failure == nil && fileInfo.Size() > 0 {
			files = append(files, DownloadedFile{TempPath: destination, Name: filepath.Base(zipFile.Name)})
		}
	}
	return files
}

// browserDownload runs the full rod browser flow (login + order + file capture)
// and returns the collected files.
func (cults3d Cults3D) browserDownload(email, password, modelURL string) ([]capturedFile, error) {
	if cults3d.Browser == nil {
		return nil, fmt.Errorf("browser unavailable")
	}
	downloadDir, failure := os.MkdirTemp("", "cults3d_dl_")
	if failure != nil {
		return nil, failure
	}

	var (
		mutex    sync.Mutex
		captured []capturedFile
		seen     = map[string]bool{}
	)
	addFile := func(name string, data []byte) {
		if len(data) == 0 {
			return
		}
		key := fmt.Sprintf("%s:%d", name, len(data))
		mutex.Lock()
		defer mutex.Unlock()
		if seen[key] {
			return
		}
		seen[key] = true
		captured = append(captured, capturedFile{name: name, data: data})
	}

	flowFailure := cults3d.Browser.WithPage(120*time.Second, func(page *rod.Page) error {
		_ = proto.NetworkEnable{}.Call(page)
		_ = proto.NetworkSetUserAgentOverride{UserAgent: cults3dUserAgent}.Call(page)
		_ = proto.PageSetDownloadBehavior{Behavior: "allow", DownloadPath: downloadDir}.Call(page)

		// Capture file responses (octet-stream/attachment/.stl…).
		fileRequestIDs := map[proto.NetworkRequestID]string{}
		var responseMutex sync.Mutex
		// Own goroutine: WithPage's recover does not reach it, so guard it here.
		safego.Go("cults3d.response-capture", page.EachEvent(func(event *proto.NetworkResponseReceived) {
			responseURL := event.Response.URL
			if !strings.Contains(responseURL, "cults3d.com") {
				return
			}
			contentType := cdpHeader(event.Response.Headers, "content-type")
			contentDisposition := cdpHeader(event.Response.Headers, "content-disposition")
			if isCults3dFile(responseURL, contentType, contentDisposition) {
				responseMutex.Lock()
				fileRequestIDs[event.RequestID] = cults3dRespName(responseURL, contentDisposition)
				responseMutex.Unlock()
			}
		}, func(event *proto.NetworkLoadingFinished) {
			responseMutex.Lock()
			name, ok := fileRequestIDs[event.RequestID]
			responseMutex.Unlock()
			if !ok {
				return
			}
			responseBody, failure := proto.NetworkGetResponseBody{RequestID: event.RequestID}.Call(page)
			if failure != nil {
				return
			}
			data := []byte(responseBody.Body)
			if responseBody.Base64Encoded {
				if decoded, failure := base64.StdEncoding.DecodeString(responseBody.Body); failure == nil {
					data = decoded
				}
			}
			addFile(name, data)
		}))

		if failure := cults3dLogin(page, email, password); failure != nil {
			return failure
		}
		if failure := cults3dOrderFlow(page, modelURL); failure != nil {
			return failure
		}
		cults3dFetchDownloadLinks(page)
		// Wait for downloads in progress.
		time.Sleep(4 * time.Second)
		return nil
	})

	collectDownloadDir(downloadDir, addFile)
	_ = os.RemoveAll(downloadDir)

	mutex.Lock()
	defer mutex.Unlock()
	if flowFailure != nil && len(captured) == 0 {
		return nil, flowFailure
	}
	return captured, nil
}

// cults3dLogin signs in on the sign-in page (remove cookie banner, fill the form,
// form.submit()).
func cults3dLogin(page *rod.Page, email, password string) error {
	if failure := page.Navigate("https://cults3d.com/en/users/sign-in"); failure != nil {
		return failure
	}
	_ = page.WaitLoad()
	time.Sleep(1500 * time.Millisecond)
	dismissCults3dCookies(page)

	// Cloudflare sometimes shows a JS interstitial ("Just a moment") before the
	// login form; the headless browser solves it automatically, but it takes a
	// few seconds. So wait generously for the email field.
	emailElement, failure := page.Timeout(45 * time.Second).Element(`input[name="user[email]"]`)
	if failure != nil {
		if info, infoFailure := page.Info(); infoFailure == nil && strings.Contains(strings.ToLower(info.Title), "just a moment") {
			return fmt.Errorf("login form not found (Cloudflare challenge not cleared)")
		}
		return fmt.Errorf("login form not found")
	}
	_ = emailElement.Input(email)
	if passwordElement, failure := page.Element(`input[name="user[password]"]`); failure == nil {
		_ = passwordElement.Input(password)
	}
	time.Sleep(800 * time.Millisecond)
	// form.submit() bypasses the disabled-button state.
	_, _ = page.Eval(`() => { const f = document.querySelector('form'); if (f) f.submit(); }`)
	time.Sleep(5 * time.Second)
	page.Timeout(10 * time.Second).WaitNavigation(proto.PageLifecycleEventNameNetworkIdle)()

	if info, failure := page.Info(); failure == nil && strings.Contains(info.URL, "sign-in") {
		return fmt.Errorf("login failed - check email/password in settings")
	}
	return nil
}

// cults3dOrderFlow ensures a (free) order exists and navigates to the order
// page.
func cults3dOrderFlow(page *rod.Page, modelURL string) error {
	slug := ""
	if parsed, failure := url.Parse(modelURL); failure == nil {
		slug = filepath.Base(strings.TrimRight(parsed.Path, "/"))
	}

	if failure := page.Navigate(modelURL); failure != nil {
		return failure
	}
	_ = page.WaitLoad()
	dismissCults3dCookies(page)

	if existing := findOrderLinks(page); len(existing) > 0 {
		_ = page.Navigate(existing[0])
		_ = page.WaitLoad()
		dismissCults3dCookies(page)
		return nil
	}

	openPriced, freeOrder := findOrderActionLinks(page)
	switch {
	case openPriced != "":
		cartGetURL := absCults3d(openPriced)
		// Remove the "new" segment for the POST.
		cartPostURL := regexp.MustCompile(`(?i)/(new|neu|nouveau|nuevo|novo|nuovo|novyi|yeni)(?:[?]|$)`).ReplaceAllString(cartGetURL, "")
		_ = page.Navigate(cartGetURL)
		_ = page.WaitLoad()
		dismissCults3dCookies(page)
		csrfToken := cults3dCSRF(page)
		orderURL := cults3dCartPost(page, cartPostURL, csrfToken)
		if orderURL == "" {
			orderURL = cults3dLatestOrder(page)
		}
		if orderURL != "" {
			_ = page.Navigate(orderURL)
			_ = page.WaitLoad()
			dismissCults3dCookies(page)
		}
	case freeOrder != "":
		_ = page.Navigate(absCults3d(freeOrder))
		_ = page.WaitLoad()
		dismissCults3dCookies(page)
	default:
		_ = page.Navigate("https://cults3d.com/en/free-order/" + slug)
		_ = page.WaitLoad()
		dismissCults3dCookies(page)
		if info, failure := page.Info(); failure != nil || !orderAnyPattern.MatchString(info.URL) {
			if latest := cults3dLatestOrder(page); latest != "" {
				_ = page.Navigate(latest)
				_ = page.WaitLoad()
				dismissCults3dCookies(page)
			} else {
				return fmt.Errorf("purchase - model requires purchase (no free download action found)")
			}
		}
	}
	return nil
}

// cults3dFetchDownloadLinks visits all /downloaden/ links of the current page to
// trigger the file downloads.
func cults3dFetchDownloadLinks(page *rod.Page) {
	links := allPageLinks(page)
	for _, link := range links {
		if parsed, failure := url.Parse(link); failure == nil && downloadLinkPattern.MatchString(parsed.Path) {
			_ = page.Navigate(link)
			_ = page.WaitLoad()
			time.Sleep(2 * time.Second)
		}
	}
}

func dismissCults3dCookies(page *rod.Page) {
	_, _ = page.Eval(`() => { document.getElementById('qc-cmp2-container')?.remove(); document.getElementById('qc-cmp2-ui')?.remove(); }`)
	time.Sleep(300 * time.Millisecond)
}

func allPageLinks(page *rod.Page) []string {
	evalResult, failure := page.Eval(`() => [...document.querySelectorAll('a[href]')].map(a => a.href)`)
	if failure != nil {
		return nil
	}
	var output []string
	for _, value := range evalResult.Value.Arr() {
		output = append(output, value.Str())
	}
	return output
}

func findOrderLinks(page *rod.Page) []string {
	var output []string
	for _, link := range allPageLinks(page) {
		if parsed, failure := url.Parse(link); failure == nil && orderLinkPattern.MatchString(parsed.Path) {
			output = append(output, link)
		}
	}
	return output
}

func findOrderActionLinks(page *rod.Page) (openPriced, freeOrder string) {
	evalResult, failure := page.Eval(`() => {
		const links = [...document.querySelectorAll('a[href]')];
		const op = links.find(a => /open.priced.lines/i.test(a.getAttribute('href') || ''));
		const fo = links.find(a => /free.order|free_order/i.test(a.getAttribute('href') || ''));
		return { op: op ? op.getAttribute('href') : '', fo: fo ? fo.getAttribute('href') : '' };
	}`)
	if failure != nil {
		return "", ""
	}
	return evalResult.Value.Get("op").Str(), evalResult.Value.Get("fo").Str()
}

func cults3dCSRF(page *rod.Page) string {
	evalResult, failure := page.Eval(`() => document.querySelector('meta[name="csrf-token"]')?.content || ''`)
	if failure != nil {
		return ""
	}
	return evalResult.Value.Str()
}

// cults3dCartPost posts the cart with amount 0 (using the browser cookies) and
// returns the order URL on success.
func cults3dCartPost(page *rod.Page, cartPostURL, csrfToken string) string {
	cookies, failure := page.Cookies([]string{})
	if failure != nil {
		return ""
	}
	var cookiePairs []string
	for _, cookie := range cookies {
		cookiePairs = append(cookiePairs, cookie.Name+"="+cookie.Value)
	}
	form := url.Values{
		"authenticity_token":       {csrfToken},
		"line[amount_in_currency]": {"0"},
		"button":                   {""},
	}.Encode()
	// directPost follows redirects; the final URL is not directly available in the
	// response - so check the orders list afterwards (cults3dLatestOrder).
	directPost(cartPostURL, form, map[string]string{
		"User-Agent":   cults3dUserAgent,
		"Content-Type": "application/x-www-form-urlencoded",
		"Cookie":       strings.Join(cookiePairs, "; "),
		"Referer":      cartPostURL,
	})
	return ""
}

func cults3dLatestOrder(page *rod.Page) string {
	_ = page.Navigate("https://cults3d.com/en/orders")
	_ = page.WaitLoad()
	dismissCults3dCookies(page)
	for _, link := range allPageLinks(page) {
		if parsed, failure := url.Parse(link); failure == nil && orderAnyPattern.MatchString(parsed.Path) {
			return link
		}
	}
	return ""
}

func absCults3d(href string) string {
	if strings.HasPrefix(href, "http") {
		return href
	}
	return "https://cults3d.com" + href
}

// collectDownloadDir reads finished files from the browser download directory.
func collectDownloadDir(directory string, addFile func(string, []byte)) {
	entries, failure := os.ReadDir(directory)
	if failure != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".crdownload") {
			continue
		}
		if data, failure := os.ReadFile(filepath.Join(directory, entry.Name())); failure == nil {
			addFile(entry.Name(), data)
		}
	}
}

// Validate checks email/password via HTTP form login (Devise, _session_id).
func (cults3d Cults3D) Validate(credentials Credentials) bool {
	return credentials.Email != "" && credentials.Password != "" && cults3dAutoLogin(credentials.Email, credentials.Password) != ""
}

var (
	cults3dCSRFPattern   = regexp.MustCompile(`(?i)name="authenticity_token"\s+value="([^"]+)"`)
	cults3dCookiePattern = regexp.MustCompile(`(?i)_session_id=([^;]+)`)
)

// cults3dAutoLogin signs in via HTTP form and returns the _session_id
// cookie. Pure HTTP flow without a browser.
func cults3dAutoLogin(email, password string) string {
	loginURL := "https://cults3d.com/en/users/sign-in"
	client := noRedirectClient()

	// Step 1: fetch the sign-in page → CSRF token + initial _session_id.
	_, header1, loginBody := rawRequest(client, "GET", loginURL, "", map[string]string{"User-Agent": cults3dUserAgent})
	csrfMatch := cults3dCSRFPattern.FindStringSubmatch(string(loginBody))
	if csrfMatch == nil {
		return ""
	}
	csrfToken := csrfMatch[1]
	initialSession := ""
	for _, setCookie := range header1["Set-Cookie"] {
		if cookieMatch := cults3dCookiePattern.FindStringSubmatch(setCookie); cookieMatch != nil {
			initialSession = cookieMatch[1]
			break
		}
	}

	// Step 2: post credentials (do not follow the redirect, 302/303 expected).
	form := url.Values{
		"authenticity_token": {csrfToken},
		"user[email]":        {email},
		"user[password]":     {password},
		"user[time_zone]":    {""},
	}.Encode()
	postStatus, header2, _ := rawRequest(client, "POST", loginURL, form, map[string]string{
		"User-Agent":   cults3dUserAgent,
		"Content-Type": "application/x-www-form-urlencoded",
		"Referer":      loginURL,
		"Cookie":       "_session_id=" + initialSession,
	})
	if postStatus != 302 && postStatus != 303 {
		return ""
	}
	for _, setCookie := range header2["Set-Cookie"] {
		if cookieMatch := cults3dCookiePattern.FindStringSubmatch(setCookie); cookieMatch != nil {
			return cookieMatch[1]
		}
	}
	return ""
}

// cdpHeader reads a header case-insensitively from the CDP response headers.
func cdpHeader(headers proto.NetworkHeaders, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value.Str()
		}
	}
	return ""
}

// isCults3dFile decides based on URL/content-type/disposition whether a response
// is a downloadable file.
func isCults3dFile(responseURL, contentType, contentDisposition string) bool {
	if regexp.MustCompile(`(?i)attachment`).MatchString(contentDisposition) && !regexp.MustCompile(`(?i)\.txt`).MatchString(contentDisposition) {
		return true
	}
	if regexp.MustCompile(`(?i)octet-stream|model/stl|application/zip`).MatchString(contentType) {
		return true
	}
	return regexp.MustCompile(`(?i)\.(stl|3mf|obj|step|zip)(\?|$)`).MatchString(responseURL)
}

// cults3dRespName determines the file name from Content-Disposition or URL.
func cults3dRespName(responseURL, contentDisposition string) string {
	if match := regexp.MustCompile(`(?i)filename\*?=(?:UTF-8''|["']?)([^"';\n]+)`).FindStringSubmatch(contentDisposition); match != nil {
		name := strings.TrimSpace(strings.ReplaceAll(match[1], `"`, ""))
		if decoded, failure := url.QueryUnescape(name); failure == nil {
			name = decoded
		}
		if name != "" {
			return name
		}
	}
	base := responseURL
	if index := strings.IndexByte(base, '?'); index >= 0 {
		base = base[:index]
	}
	base = base[strings.LastIndexByte(base, '/')+1:]
	if base == "" {
		return "file.stl"
	}
	return base
}

// cults3dOrderDownloadURL queries the GraphQL API (basic auth nickname:apiKey)
// for the download URL of the order belonging to the given model slug. Empty if
// the model has not (yet) been ordered.
func cults3dOrderDownloadURL(nickname, apiKey, slug string) string {
	const query = `query={ myself { ordersBatch(limit:100){ results { lines { downloadUrl creation { slug } } } } } }`
	request, failure := http.NewRequest(http.MethodPost, "https://cults3d.com/graphql", strings.NewReader(query))
	if failure != nil {
		return ""
	}
	request.SetBasicAuth(nickname, apiKey)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, failure := (&http.Client{Timeout: 60 * time.Second}).Do(request)
	if failure != nil {
		return ""
	}
	defer response.Body.Close()
	var parsed struct {
		Data struct {
			Myself struct {
				OrdersBatch struct {
					Results []struct {
						Lines []struct {
							DownloadURL string `json:"downloadUrl"`
							Creation    struct {
								Slug string `json:"slug"`
							} `json:"creation"`
						} `json:"lines"`
					} `json:"results"`
				} `json:"ordersBatch"`
			} `json:"myself"`
		} `json:"data"`
	}
	if json.NewDecoder(response.Body).Decode(&parsed) != nil {
		return ""
	}
	for _, order := range parsed.Data.Myself.OrdersBatch.Results {
		for _, line := range order.Lines {
			if line.Creation.Slug == slug && line.DownloadURL != "" {
				return line.DownloadURL
			}
		}
	}
	return ""
}

// cults3dFirefoxDownload lets the Firefox resolver sidecar visit the given
// download URLs (from the API) and collect the files (web session needed; Firefox
// passes Cloudflare). Returns: captured files.
func cults3dFirefoxDownload(playwrightURL, email, password, cookieJar string, urls []string) ([]capturedFile, string) {
	requestBody, _ := json.Marshal(map[string]any{
		"email": email, "password": password, "urls": urls, "cookieJar": cookieJar,
	})
	endpoint := strings.TrimRight(playwrightURL, "/") + "/download/cults3d-url"
	request, failure := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if failure != nil {
		return nil, ""
	}
	request.Header.Set("Content-Type", "application/json")
	applyPlaywrightAuth(request)
	response, failure := (&http.Client{Timeout: 300 * time.Second}).Do(request)
	if failure != nil {
		return nil, ""
	}
	defer response.Body.Close()
	var parsed struct {
		Files []struct {
			Name string `json:"name"`
			Data string `json:"data"`
		} `json:"files"`
		CookieJar string `json:"cookieJar"`
		Error     string `json:"error"`
	}
	if json.NewDecoder(response.Body).Decode(&parsed) != nil {
		return nil, ""
	}
	var output []capturedFile
	for _, file := range parsed.Files {
		raw, failure := base64.StdEncoding.DecodeString(file.Data)
		if failure != nil || len(raw) == 0 {
			continue
		}
		output = append(output, capturedFile{name: file.Name, data: raw})
	}
	return output, parsed.CookieJar
}

// cults3dDirectDownload loads the file via a simple HTTP GET with the cached
// cookie jar (cf_clearance + _session_id) and Firefox user agent - without a
// browser. Cults3D/downloads redirects to the file (or CDN) with a valid session.
// Returns nil if an HTML page comes instead (Cloudflare challenge or login
// redirect) - then the Firefox fallback takes over.
func cults3dDirectDownload(cookieJar, downloadURL string) []capturedFile {
	request, failure := http.NewRequest(http.MethodGet, downloadURL, nil)
	if failure != nil {
		return nil
	}
	request.Header.Set("Cookie", cookieJar)
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; rv:122.0) Gecko/20100101 Firefox/122.0")
	request.Header.Set("Accept", "*/*")
	response, failure := (&http.Client{Timeout: 300 * time.Second}).Do(request)
	if failure != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	// HTML = Cloudflare interstitial or login page → not a file.
	if strings.Contains(contentType, "text/html") {
		return nil
	}
	data, failure := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if failure != nil || len(data) == 0 {
		return nil
	}
	name := cults3dRespName(downloadURL, response.Header.Get("Content-Disposition"))
	return []capturedFile{{name: name, data: data}}
}

// cults3dCookieJar returns the stored (encrypted) cookie jar (cf_clearance +
// _session_id) of the account, or "" if none exists.
func (cults3d Cults3D) cults3dCookieJar(userID int) string {
	var encrypted sql.NullString
	_ = cults3d.DB.QueryRow(
		"SELECT session_cookie FROM platform_accounts WHERE user_id=? AND platform='cults3d' LIMIT 1", userID,
	).Scan(&encrypted)
	if !encrypted.Valid || encrypted.String == "" {
		return ""
	}
	if jar, ok := cults3d.Crypto.Decrypt(encrypted.String, userID); ok {
		return jar
	}
	return ""
}

// saveCults3dCookieJar stores the cookie jar encrypted so that later downloads
// can skip both the Cloudflare check and the login.
func (cults3d Cults3D) saveCults3dCookieJar(userID int, jar string) {
	if encrypted, failure := cults3d.Crypto.Encrypt(jar, userID); failure == nil {
		dbutil.ExecLogged(cults3d.DB, "UPDATE platform_accounts SET session_cookie=? WHERE user_id=? AND platform='cults3d'", encrypted, userID)
	}
}
