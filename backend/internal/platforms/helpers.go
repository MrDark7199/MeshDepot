package platforms

// Shared helpers of the platform downloaders: HTTP GET direct and through Tor,
// temp directories, cover download and token resolution.

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

	// The container runs with mem_limit 2g and shares its disk with the library, so
	// a platform answering with an endless stream must not fill either.
	maxDownloadBytes = 4 << 30 // 4 GiB
	// maxResponseBytes caps what is read into memory: API JSON, HTML, images.
	maxResponseBytes = 256 << 20 // 256 MiB
	// sniffBytes is how much of a response is inspected to tell a real file from an
	// HTML or JSON block page served with status 200.
	sniffBytes = 512
)

type Deps struct {
	DB      *sql.DB
	Crypto  *crypto.Crypto
	Tor     *tor.Supervisor
	Browser *browser.Runner
	Cfg     config.Config
}

// newOutboundRequest builds a request with our User-Agent and the caller's
// headers. The URL is used as given: percent-escaping the path breaks presigned
// CDN URLs whose signature covers it.
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

// fetch performs one outbound request and reads the body, capped at
// maxResponseBytes. The single place the outbound policy lives; the four
// wrappers below differ only in client and method. (nil, 0) on a transport error.
func fetch(client *http.Client, method, rawURL, body string, headers map[string]string) ([]byte, int) {
	request, failure := newOutboundRequest(method, sanitizeURL(rawURL), body, headers)
	if failure != nil {
		return nil, 0
	}
	response, failure := client.Do(request)
	if failure != nil {
		recordRequest(rawURL, requestKindAPI, 0)
		return nil, 0
	}
	defer response.Body.Close()
	recordRequest(rawURL, requestKindAPI, response.StatusCode)
	output, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	return output, response.StatusCode
}

func apiClient() *http.Client {
	return &http.Client{Timeout: apiTimeoutSecond * time.Second}
}

func directGet(rawURL string, headers map[string]string) ([]byte, int) {
	return fetch(&http.Client{Timeout: defaultTimeoutSecond * time.Second}, http.MethodGet, rawURL, "", headers)
}

// directGetWith is directGet through a caller-supplied client, so an address
// that came from a client can be fetched through the guarded one.
func directGetWith(client *http.Client, rawURL string, headers map[string]string) ([]byte, int) {
	return fetch(client, http.MethodGet, rawURL, "", headers)
}

func directPost(rawURL, body string, headers map[string]string) ([]byte, int) {
	return fetch(apiClient(), http.MethodPost, rawURL, body, headers)
}

// torGet rotates the circuit beforehand.
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

// torPost does not rotate the circuit: a POST usually continues a session.
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

// storeImage downloads one image into the user's staging directory and returns
// the absolute path, or "" when the URL yields nothing usable: too small,
// unreachable, or not one of the allowed raster formats. suffix only keeps names
// apart within the same nanosecond.
func storeImage(user storage.UserLayout, imageURL string, suffix int) string {
	return storeImageWith(user, imageURL, suffix, false)
}

// storeImageGuarded is storeImage for an address a client supplied.
func storeImageGuarded(user storage.UserLayout, imageURL string, suffix int) string {
	return storeImageWith(user, imageURL, suffix, true)
}

func storeImageWith(user storage.UserLayout, imageURL string, suffix int, guarded bool) string {
	if imageURL == "" {
		return ""
	}
	// Staged in the user's temp directory: the design id only exists once the row
	// does, so SaveDownload moves the file into the pictures directory afterwards.
	directory := filepath.Join(user.Temp(), "images")
	if failure := storage.MkdirAll(directory); failure != nil {
		return ""
	}
	var body []byte
	if guarded {
		body, _ = directGetWith(guardedDownloadClient(), imageURL, nil)
	} else {
		body, _ = directGet(imageURL, nil)
	}
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

// downloadAllImages stores the usable ones. progress reports the
// "downloading_images" phase per image, which would otherwise be a single moment
// invisible to the polling.
func downloadAllImages(user storage.UserLayout, imageURLs []string, progress func(step, label string, current, total int)) []string {
	return downloadAllImagesWith(user, imageURLs, progress, false)
}

// downloadAllImagesGuarded is the same for addresses a client supplied.
func downloadAllImagesGuarded(user storage.UserLayout, imageURLs []string) []string {
	return downloadAllImagesWith(user, imageURLs, nil, true)
}

func downloadAllImagesWith(user storage.UserLayout, imageURLs []string,
	progress func(step, label string, current, total int), guarded bool) []string {

	var saved []string
	for index, imageURL := range imageURLs {
		if progress != nil {
			progress("downloading_images", "", index+1, len(imageURLs))
		}
		storedPath := ""
		if guarded {
			storedPath = storeImageGuarded(user, imageURL, len(saved))
		} else {
			storedPath = storeImage(user, imageURL, len(saved))
		}
		if storedPath != "" {
			saved = append(saved, storedPath)
		}
	}
	return saved
}

// downloadFileTo writes the response to destPath without holding the file in
// memory, and leaves nothing behind when the response is not a usable file.
//
// The buffered alternative costs a 500 MB heap for a 500 MB 3MF, against a 2 GiB
// container limit and several jobs in flight. It only existed for the soft-block
// check - several platforms answer a blocked download with an error page and
// status 200 - and sniffing the first sniffBytes answers that just as well.
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

// downloadClient covers the whole transfer, so its timeout is far longer.
func downloadClient() *http.Client {
	return &http.Client{Timeout: downloadTimeout}
}

const downloadTimeout = 600 * time.Second

// thingiverseAPI returns the decoded JSON, or nil on error or invalid JSON.
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

func makeTempDir(user storage.UserLayout) (string, error) {
	directory := filepath.Join(user.Temp(),
		fmt.Sprintf("dl_%d_%d", time.Now().UnixNano(), os.Getpid()))
	if failure := storage.MkdirAll(directory); failure != nil {
		return "", failure
	}
	return directory, nil
}

var coverFilenamePattern = regexp.MustCompile(`[^\w.\-]`)

// downloadCover returns the absolute path of the staged title image.
func downloadCover(user storage.UserLayout, imageURL string) string {
	return storeImage(user, imageURL, os.Getpid())
}

// SVG is deliberately absent: it is a document format that can carry <script>,
// and these files are served from the app's own origin.
var imageExtensions = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// imageExtension reads the format from the bytes. The URL extension cannot be
// trusted - a platform may point at .../x.svg, and http.ServeFile derives the
// Content-Type from it, so arbitrary HTML would be served on the app origin with
// the viewer's session in reach.
func imageExtension(body []byte) (string, bool) {
	contentType := http.DetectContentType(body)
	if index := strings.IndexByte(contentType, ';'); index >= 0 {
		contentType = strings.TrimSpace(contentType[:index])
	}
	extension, ok := imageExtensions[strings.ToLower(contentType)]
	return extension, ok
}

// PlatformToken returns the user's decrypted platform token. Tokens come from
// the account alone; there is no server-wide fallback.
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

func sanitizeFileName(name string) string {
	return coverFilenamePattern.ReplaceAllString(name, "_")
}

const totpAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// generateTotp computes the 6-digit code (30-second window, HMAC-SHA1).
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

// streamDownload streams a URL to disk and returns the written size and HTTP
// status, for the 403 and magic-byte checks.
func streamDownload(rawURL, destPath string, headers map[string]string) (int64, int) {
	return streamDownloadWith(downloadClient(), rawURL, destPath, headers)
}

// streamDownloadGuarded is streamDownload for an address this server did not
// choose: every connection is checked against the private ranges. outboundguard.go
// explains why that belongs at the socket rather than at the URL.
func streamDownloadGuarded(rawURL, destPath string, headers map[string]string) (int64, int) {
	return streamDownloadWith(guardedDownloadClient(), rawURL, destPath, headers)
}

func streamDownloadWith(client *http.Client, rawURL, destPath string, headers map[string]string) (int64, int) {
	// Unsanitised: these are presigned CDN links whose signature covers the path.
	request, failure := newOutboundRequest(http.MethodGet, rawURL, "", headers)
	if failure != nil {
		return 0, 0
	}
	response, failure := client.Do(request)
	if failure != nil {
		recordRequest(rawURL, requestKindDownload, 0)
		return 0, 0
	}
	defer response.Body.Close()
	recordRequest(rawURL, requestKindDownload, response.StatusCode)
	// Status first: creating the file before checking left a 0-byte corpse behind
	// every 403.
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
		// A truncated file would pass the size checks in SaveDownload and land in the
		// library as a corrupt model.
		_ = os.Remove(destPath)
		return 0, response.StatusCode
	}
	return written, response.StatusCode
}

// jwtExpired reads the exp claim. Anything that is not a JWT, a cookie for
// instance, counts as not expired.
func jwtExpired(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, failure := base64.RawURLEncoding.DecodeString(parts[1])
	if failure != nil {
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

// uniqueStrings preserves order. Not slices.Compact: that drops only consecutive
// duplicates and would need a sort first.
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
