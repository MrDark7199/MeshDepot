package platforms

// Shared helpers of the platform downloaders: HTTP GET (direct + via Tor with circuit
// rotation), Thingiverse API call, temp directory, cover download and platform token
// resolution.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"meshdepot/internal/browser"
	"meshdepot/internal/config"
	"meshdepot/internal/crypto"
	"meshdepot/internal/platforms/tor"
	"meshdepot/internal/storage"
)

const (
	minSmallFileBytes    = 50
	minImageFileBytes    = 500
	minValidFileBytes    = 100
	defaultTimeoutSecond = 60
	apiTimeoutSecond     = 30
	userAgent            = "MeshDepot/1.0 (+https://github.com/meshdepot)"

	// maxDownloadBytes caps a single streamed file. The container runs with
	// mem_limit 2g and the disk is shared with the library, so a platform that
	// answers with an endless stream must not be able to fill either.
	maxDownloadBytes = 4 << 30 // 4 GiB
	// maxResponseBytes caps responses that are read into memory (API JSON, HTML
	// pages, images). Nothing legitimate on those paths comes close.
	maxResponseBytes = 256 << 20 // 256 MiB
	// sniffBytes is how much of a response is inspected to tell a real file from
	// an HTML/JSON block page served with status 200.
	sniffBytes = 512
)

// Deps bundles the dependencies that all downloaders share.
type Deps struct {
	DB      *sql.DB
	Crypto  *crypto.Crypto
	Tor     *tor.Supervisor
	Browser *browser.Runner
	Cfg     config.Config
}

// newOutboundRequest builds a request with our User-Agent and the caller's
// headers on top of it. The URL is used as given - whether it may be sanitised
// is the caller's decision: percent-escaping the path breaks presigned CDN URLs
// whose signature covers it.
func newOutboundRequest(method, rawURL, body string, headers map[string]string) (*http.Request, error) {
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	request, failure := http.NewRequest(method, rawURL, payload)
	if failure != nil {
		return nil, failure
	}
	request.Header.Set("User-Agent", userAgent)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return request, nil
}

// fetch performs one outbound request and reads the response body, capped at
// maxResponseBytes. It is the single place where the outbound request policy
// lives: sanitised URL, our User-Agent, caller headers on top of it. Returns
// body + HTTP status, or (nil, 0) on a transport error.
//
// The four wrappers below differ only in the client (direct vs. Tor, API vs.
// download timeout) and the method - that was four copies of this function.
func fetch(client *http.Client, method, rawURL, body string, headers map[string]string) ([]byte, int) {
	request, failure := newOutboundRequest(method, sanitizeURL(rawURL), body, headers)
	if failure != nil {
		return nil, 0
	}
	response, failure := client.Do(request)
	if failure != nil {
		return nil, 0
	}
	defer response.Body.Close()
	output, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	return output, response.StatusCode
}

// apiClient is the client for metadata/API calls, with the shorter timeout.
func apiClient() *http.Client {
	return &http.Client{Timeout: apiTimeoutSecond * time.Second}
}

// directGet performs a direct HTTP GET (follows redirects).
func directGet(rawURL string, headers map[string]string) ([]byte, int) {
	return fetch(&http.Client{Timeout: defaultTimeoutSecond * time.Second}, http.MethodGet, rawURL, "", headers)
}

// directPost performs a direct HTTP POST with a raw body.
func directPost(rawURL, body string, headers map[string]string) ([]byte, int) {
	return fetch(apiClient(), http.MethodPost, rawURL, body, headers)
}

// torGet always performs a GET through the Tor SOCKS5 proxy and rotates the
// route beforehand.
func (deps Deps) torGet(rawURL string, headers map[string]string) ([]byte, int) {
	if deps.Tor == nil {
		return nil, 0
	}
	_ = deps.Tor.NewCircuit()
	client, failure := deps.Tor.HTTPClient(defaultTimeoutSecond * time.Second)
	if failure != nil {
		return nil, 0
	}
	return fetch(client, http.MethodGet, rawURL, "", headers)
}

// torPost performs a POST through the Tor SOCKS5 proxy. Unlike torGet it does
// not rotate the circuit: a POST usually continues a session on the current one.
func (deps Deps) torPost(rawURL, body string, headers map[string]string) ([]byte, int) {
	if deps.Tor == nil {
		return nil, 0
	}
	client, failure := deps.Tor.HTTPClient(apiTimeoutSecond * time.Second)
	if failure != nil {
		return nil, 0
	}
	return fetch(client, http.MethodPost, rawURL, body, headers)
}

// storeImage downloads a single image and stores it under
// the user's staging directory. Returns the absolute staged path, or "" when
// the URL yields nothing usable: too small, unreachable, or not one of the
// raster formats on the allowlist. suffix only keeps the file names apart when
// several images are stored within the same nanosecond.
func storeImage(user storage.UserLayout, imageURL string, suffix int) string {
	if imageURL == "" {
		return ""
	}
	// Staged inside the user's temp directory: the design id is only known once
	// the row exists, so SaveDownload/AddImages move the file into the design's
	// pictures directory afterwards.
	directory := filepath.Join(user.Temp(), "images")
	if failure := storage.MkdirAll(directory); failure != nil {
		return ""
	}
	body, _ := directGet(imageURL, nil)
	if len(body) <= minImageFileBytes {
		return ""
	}
	extension, ok := imageExtension(body)
	if !ok {
		return ""
	}
	filename := fmt.Sprintf("cover_%d_%d.%s", time.Now().UnixNano(), suffix, extension)
	staged := filepath.Join(directory, filename)
	if failure := storage.WriteFile(staged, body); failure != nil {
		return ""
	}
	return staged
}

// downloadAllImages loads several image URLs and stores the usable ones.
// progress reports - if set - the progress of the "downloading_images" phase per
// image, so the "checking images" category becomes visible during sync
// (otherwise it would be a single moment invisible to the polling).
func downloadAllImages(user storage.UserLayout, imageURLs []string, progress func(step, label string, current, total int)) []string {
	var saved []string
	for index, imageURL := range imageURLs {
		if progress != nil {
			progress("downloading_images", "", index+1, len(imageURLs))
		}
		if storedPath := storeImage(user, imageURL, len(saved)); storedPath != "" {
			saved = append(saved, storedPath)
		}
	}
	return saved
}

// downloadFileTo fetches rawURL through client and writes it to destPath without
// ever holding the file in memory. It returns false when the response is not a
// usable file, in which case no file is left behind.
//
// The buffered alternative (directGet → os.WriteFile) needs the full file on the
// heap - a 500 MB 3MF costs 500 MB per job, next to a 2 GiB container limit and
// several jobs in flight. The only reason it existed is the soft-block check:
// several platforms answer a blocked download with an HTML or JSON error page
// and status 200. Sniffing the first sniffBytes gives that check the same
// information without the rest of the body ever being buffered.
func downloadFileTo(client *http.Client, rawURL, destPath string, headers map[string]string) bool {
	request, failure := newOutboundRequest(http.MethodGet, sanitizeURL(rawURL), "", headers)
	if failure != nil {
		return false
	}
	response, failure := client.Do(request)
	if failure != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return false
	}
	head := make([]byte, sniffBytes)
	headLength, _ := io.ReadFull(response.Body, head)
	head = head[:headLength]
	if !isFileBody(head) {
		return false
	}
	output, failure := os.Create(destPath)
	if failure != nil {
		return false
	}
	written, copyFailure := io.Copy(output, io.LimitReader(
		io.MultiReader(bytes.NewReader(head), response.Body), maxDownloadBytes))
	closeFailure := output.Close()
	if copyFailure != nil || closeFailure != nil || written < minSmallFileBytes {
		_ = os.Remove(destPath)
		return false
	}
	return true
}

// torDownloadFileTo is downloadFileTo through a fresh Tor circuit.
func (deps Deps) torDownloadFileTo(rawURL, destPath string, headers map[string]string) bool {
	if deps.Tor == nil {
		return false
	}
	_ = deps.Tor.NewCircuit()
	client, failure := deps.Tor.HTTPClient(downloadTimeout)
	if failure != nil {
		return false
	}
	return downloadFileTo(client, rawURL, destPath, headers)
}

// downloadClient is the client for streamed file downloads; the timeout covers
// the whole transfer, so it is far longer than the API one.
func downloadClient() *http.Client {
	return &http.Client{Timeout: downloadTimeout}
}

// downloadTimeout bounds a complete file transfer.
const downloadTimeout = 600 * time.Second

// tvApi calls the Thingiverse REST API and returns the decoded JSON. On
// error/invalid JSON the result is nil.
func thingiverseAPI(rawURL, token string) any {
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	body, _ := directGet(rawURL, headers)
	if body == nil {
		return nil
	}
	var decoded any
	if json.Unmarshal(body, &decoded) != nil {
		return nil
	}
	return decoded
}

// sanitizeURL percent-encodes the path segments and keeps the query.
func sanitizeURL(raw string) string {
	parsed, failure := url.Parse(raw)
	if failure != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	segments := strings.Split(parsed.Path, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	output := parsed.Scheme + "://" + parsed.Host + strings.Join(segments, "/")
	if parsed.RawQuery != "" {
		output += "?" + parsed.RawQuery
	}
	return output
}

// makeTmpDir creates a unique temp directory for a download.
func makeTempDir(user storage.UserLayout) (string, error) {
	directory := filepath.Join(user.Temp(),
		fmt.Sprintf("dl_%d_%d", time.Now().UnixNano(), os.Getpid()))
	if failure := storage.MkdirAll(directory); failure != nil {
		return "", failure
	}
	return directory, nil
}

// coverFilenamePattern filters the file extension out of a cover URL path.
var coverFilenamePattern = regexp.MustCompile(`[^\w.\-]`)

// downloadCover loads the title image of a design. Returns the absolute path of
// the staged file.
func downloadCover(user storage.UserLayout, imageURL string) string {
	return storeImage(user, imageURL, os.Getpid())
}

// imageExtensions maps the raster formats we accept to their file extension.
// SVG is deliberately absent: it is a document format that can carry <script>,
// and these files are served from the app's own origin.
var imageExtensions = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// imageExtension determines the file extension from the actual bytes. The URL
// extension must not be trusted: a platform's metadata can point at .../x.html
// or .../x.svg, and http.ServeFile derives the Content-Type from the extension -
// so arbitrary HTML would be served as text/html on the app origin, with the
// viewer's session in reach. Returns false for anything not on the allowlist.
func imageExtension(body []byte) (string, bool) {
	contentType := http.DetectContentType(body)
	if index := strings.IndexByte(contentType, ';'); index >= 0 {
		contentType = strings.TrimSpace(contentType[:index])
	}
	extension, ok := imageExtensions[strings.ToLower(contentType)]
	return extension, ok
}

// PlatformToken returns the stored platform token of a user (decrypted) or empty
// if none is stored. Tokens come exclusively from the user's account
// (Account Settings → Platforms); there is no server-wide fallback.
func (deps Deps) PlatformToken(userID int, platform string) string {
	var encrypted sql.NullString
	failure := deps.DB.QueryRow(
		"SELECT token FROM platform_accounts WHERE user_id = ? AND platform = ? LIMIT 1",
		userID, platform,
	).Scan(&encrypted)
	if failure == nil && encrypted.Valid && encrypted.String != "" {
		if decrypted, ok := deps.Crypto.Decrypt(encrypted.String, userID); ok {
			return decrypted
		}
	}
	return ""
}

// sanitizeFileName replaces every character outside [\w.-] with "_".
func sanitizeFileName(name string) string {
	return coverFilenamePattern.ReplaceAllString(name, "_")
}

// totpAlphabet is the RFC 4648 base32 alphabet (without padding).
const totpAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// generateTotp computes the 6-digit TOTP code (30-second window, HMAC-SHA1) from
// a base32 secret.
func generateTotp(secret string) string {
	secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	var buffer uint64
	var bits uint
	var key []byte
	for index := 0; index < len(secret); index++ {
		position := strings.IndexByte(totpAlphabet, secret[index])
		if position < 0 {
			continue
		}
		buffer = buffer<<5 | uint64(position)
		bits += 5
		if bits >= 8 {
			bits -= 8
			key = append(key, byte(buffer>>bits))
		}
	}
	counter := uint64(time.Now().Unix() / 30)
	counterBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(counterBytes, counter)
	hasher := hmac.New(sha1.New, key)
	hasher.Write(counterBytes)
	sum := hasher.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", code)
}

// streamDownload streams a URL to disk (follows redirects) and returns the
// written size + HTTP status (for 403/magic-byte checks).
func streamDownload(rawURL, destPath string, headers map[string]string) (int64, int) {
	// The URL is passed through unsanitised: these are presigned CDN links whose
	// signature covers the path.
	request, failure := newOutboundRequest(http.MethodGet, rawURL, "", headers)
	if failure != nil {
		return 0, 0
	}
	response, failure := downloadClient().Do(request)
	if failure != nil {
		return 0, 0
	}
	defer response.Body.Close()
	// Status first: creating the file before checking it left a 0-byte corpse in
	// the temp directory behind every 403 from a platform.
	if response.StatusCode >= 400 {
		return 0, response.StatusCode
	}
	output, failure := os.Create(destPath)
	if failure != nil {
		return 0, response.StatusCode
	}
	written, copyFailure := io.Copy(output, io.LimitReader(response.Body, maxDownloadBytes))
	closeFailure := output.Close()
	if copyFailure != nil || closeFailure != nil {
		// A truncated file is worse than none: it would pass the size checks in
		// SaveDownload and end up in the library as a corrupt model.
		_ = os.Remove(destPath)
		return 0, response.StatusCode
	}
	return written, response.StatusCode
}

// jwtExpired checks whether a JWT access token is expired according to its `exp`
// claim. Tokens that are not JWTs (e.g. cookies) count as "not expired" (false).
func jwtExpired(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, failure := base64.RawURLEncoding.DecodeString(parts[1])
	if failure != nil {
		// Some tokens use standard base64/padding.
		payload, failure = base64.StdEncoding.DecodeString(parts[1])
		if failure != nil {
			return false
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return false
	}
	return time.Now().Unix() >= claims.Exp
}

// uniqueStrings removes duplicates (order preserved). Not slices.Compact:
// that only drops *consecutive* duplicates and would need a sort first.
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	output := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			output = append(output, value)
		}
	}
	return output
}

func mapStrings(values []string, transform func(string) string) []string {
	output := make([]string, len(values))
	for index, value := range values {
		output[index] = transform(value)
	}
	return output
}
