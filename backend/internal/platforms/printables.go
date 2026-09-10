package platforms

// Printables downloader. Models come from the GraphQL API, which needs a Prusa
// bearer token obtained through the OAuth2 authorization-code flow with PKCE
// (autoLogin, pure HTTP). The files themselves are loaded via Tor.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
)

const printablesGraphQL = "https://api.printables.com/graphql/"

// graphQL tries directly first and retries via Tor with a route change when the
// answer is unusable - a rate limit on the server IP, a non-200, or no "data"
// field - so a temporary block does not abort the download.
func (printables Printables) graphQL(body string, headers map[string]string) []byte {
	response, status := directPost(printablesGraphQL, body, headers)
	if status == 200 && bytes.Contains(response, []byte(`"data"`)) {
		return response
	}
	for attempt := 0; attempt < 2 && printables.Tor != nil; attempt++ {
		_ = printables.Tor.NewCircuit()
		if torResponse, torStatus := printables.torPost(printablesGraphQL, body, headers); torStatus == 200 && bytes.Contains(torResponse, []byte(`"data"`)) {
			return torResponse
		}
		time.Sleep(time.Second)
	}
	return response
}

const printablesUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

const printablesClientID = "EK15sodB6SmoUXmOshtCBS4PA3Bkvwgwnb8Ux5Mj"

var (
	printablesModelIDPattern = regexp.MustCompile(`model/(\d+)`)
	csrfPattern1             = regexp.MustCompile(`(?i)name="csrfmiddlewaretoken"\s+value="([^"]+)"`)
	csrfPattern2             = regexp.MustCompile(`(?i)value="([^"]+)"\s+name="csrfmiddlewaretoken"`)
)

type Printables struct{ Deps }

func (printables Printables) Download(sourceURL string, owner Owner, progress func(step, label string, current, total int)) (Result, error) {
	// The numeric id is only for credentials; every path comes from owner.Layout.
	userID := owner.ID
	if progress == nil {
		progress = func(string, string, int, int) {}
	}

	match := printablesModelIDPattern.FindStringSubmatch(sourceURL)
	if match == nil {
		return Result{}, fmt.Errorf("Cannot extract Printables model ID from URL: %s", sourceURL)
	}
	modelID := match[1]

	bearer := printables.resolveToken(userID, progress)

	baseHeaders := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"User-Agent":   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Referer":      "https://www.printables.com/",
	}
	if bearer != "" {
		baseHeaders["Authorization"] = "Bearer " + bearer
	}

	// - Step 1: metadata + STL file list --------------------
	progress("fetching_metadata", "", 0, 0)
	metaQuery := fmt.Sprintf("{ print(id: %s) { name description user { publicUsername } images { filePath } tags { name } stls { id name fileSize folder } } }", modelID)
	metaBody, _ := json.Marshal(map[string]string{"query": metaQuery})
	rawResp := printables.graphQL(string(metaBody), baseHeaders)
	var meta struct {
		Data struct {
			Print *struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				User        struct {
					PublicUsername string `json:"publicUsername"`
				} `json:"user"`
				Images []struct {
					FilePath string `json:"filePath"`
				} `json:"images"`
				Tags []struct {
					Name string `json:"name"`
				} `json:"tags"`
				Stls []struct {
					ID     json.Number `json:"id"`
					Name   string      `json:"name"`
					Folder string      `json:"folder"`
				} `json:"stls"`
			} `json:"print"`
		} `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	_ = json.Unmarshal(rawResp, &meta)
	// Printables often revokes access tokens earlier than their JWT exp claim says,
	// answering 401 without data. Fetch a fresh token and try once more.
	if meta.Data.Print == nil && bearer != "" {
		if newToken := printables.Deps.forceLogin(userID, "printables"); newToken != "" {
			bearer = newToken
			baseHeaders["Authorization"] = "Bearer " + newToken
			rawResp = printables.graphQL(string(metaBody), baseHeaders)
			meta.Data.Print = nil
			_ = json.Unmarshal(rawResp, &meta)
		}
	}
	if meta.Data.Print == nil {
		detail := string(meta.Errors)
		if detail == "" {
			detail = "no data"
		}
		return Result{}, MetadataError("Printables API returned no data for model %s. Error: %s. "+
			"The model may be private, deleted, or the API has changed.", modelID, detail)
	}
	print := meta.Data.Print

	name := print.Name
	if name == "" {
		name = "Printables #" + modelID
	}
	author := print.User.PublicUsername
	var tags []string
	for _, tag := range print.Tags {
		if tag.Name != "" {
			tags = append(tags, tag.Name)
		}
	}

	tempDir, failure := makeTempDir(owner.Layout)
	if failure != nil {
		return Result{}, failure
	}

	// - Step 2: per file a getDownloadLink mutation, then download via Tor ---
	downloadHeaders := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Referer":    "https://www.printables.com/model/" + modelID,
	}
	var files []DownloadedFile
	total := len(print.Stls)
	progress("downloading_files", "", 0, total)
	for index, stl := range print.Stls {
		progress("downloading_files", "", index+1, total)
		fileID := stl.ID.String()
		if fileID == "" {
			continue
		}
		filename := stl.Name
		if filename == "" {
			filename = fmt.Sprintf("file_%d.stl", len(files))
		}
		folder := strings.TrimSpace(stl.Folder)

		safeName := sanitizeRelName(filename)
		safeFolder := ""
		if folder != "" {
			safeFolder = sanitizeRelName(folder)
		}
		relativePath := safeName
		if safeFolder != "" {
			relativePath = safeFolder + "/" + safeName
		}

		mutation := fmt.Sprintf(`mutation{getDownloadLink(printId:"%s",id:"%s",fileType:stl,source:model_detail){ok errors{field messages} output{files{id link} link}}}`, modelID, fileID)
		mutationBody, _ := json.Marshal(map[string]string{"query": mutation})
		downloadResp := printables.graphQL(string(mutationBody), baseHeaders)
		var downloadLink struct {
			Data struct {
				GetDownloadLink struct {
					Output struct {
						Link string `json:"link"`
					} `json:"output"`
				} `json:"getDownloadLink"`
			} `json:"data"`
		}
		_ = json.Unmarshal(downloadResp, &downloadLink)
		downloadURL := downloadLink.Data.GetDownloadLink.Output.Link
		if downloadURL == "" {
			continue
		}

		subDirectory := tempDir
		if safeFolder != "" {
			subDirectory = filepath.Join(tempDir, safeFolder)
			_ = os.MkdirAll(subDirectory, 0o775)
		}
		tempPath := filepath.Join(subDirectory, safeName)

		// The CDN blocks direct IPs - up to three times via Tor with a route change.
		stored := false
		for attempt := 1; attempt <= 3; attempt++ {
			if printables.torDownloadFileTo(downloadURL, tempPath, downloadHeaders) {
				stored = true
				break
			}
		}
		if !stored {
			continue
		}
		files = append(files, DownloadedFile{TempPath: tempPath, Name: relativePath})
	}

	if len(files) == 0 {
		message := fmt.Sprintf("%d STL(s) found but none could be downloaded.", len(print.Stls))
		if len(print.Stls) == 0 {
			message = "No STL files found in model metadata."
		}
		return Result{}, FilesError("error.printables_no_files:Downloaded 0 files from Printables model %s. "+
			"The model may be private/paid (login required), have no downloadable STL files, or the API has changed. %s", modelID, message)
	}

	// - Step 3: images -----------------------------
	var imageURLs []string
	for _, image := range print.Images {
		if image.FilePath != "" {
			imageURLs = append(imageURLs, "https://media.printables.com/"+image.FilePath)
		}
	}
	progress("downloading_images", "", 0, len(imageURLs))
	allImages := downloadAllImages(owner.Layout, imageURLs, progress)
	var coverPath string
	if len(allImages) > 0 {
		coverPath = allImages[0]
	}

	return Result{
		Name:        name,
		Description: print.Description,
		Author:      author,
		SourceID:    modelID,
		CoverPath:   coverPath,
		AllImages:   allImages,
		Tags:        tags,
		Files:       files,
	}, nil
}

func (printables Printables) Validate(credentials Credentials) bool {
	return credentials.Email != "" && credentials.Password != "" && autoLoginPrintables(credentials.Email, credentials.Password) != ""
}

// resolveToken returns the stored token while it is valid, otherwise one from
// autoLogin, persisted encrypted. Besides the stored expiry it honours the JWT
// exp claim, since Printables revokes tokens well before the 28 days.
func (printables Printables) resolveToken(userID int, progress func(string, string, int, int)) string {
	token, _ := printables.Deps.resolveToken(userID, "printables", progress)
	return token
}

// autoLoginPrintables runs the Prusa OAuth2 flow with PKCE.
func autoLoginPrintables(email, password string) string {
	state := randHex(6)
	verifier := base64URL(randBytes(32))
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64URL(sum[:])
	redirectURI := "https://www.printables.com/login?state=" + state
	next := "/o/authorize/?client_id=" + printablesClientID +
		"&response_type=code&code_challenge=" + challenge +
		"&code_challenge_method=S256&scope=basic_info" +
		"&redirect_uri=" + url.QueryEscape(redirectURI)
	loginURL := "https://account.prusa3d.com/login/?next=" + url.QueryEscape(next)

	client := noRedirectClient()

	// Step 1: the login page, for the CSRF token and cookies.
	status1, header1, body1 := rawRequest(client, http.MethodGet, loginURL, "", map[string]string{
		"User-Agent": printablesUserAgent, "Accept": "text/html", "Accept-Language": "en-US,en;q=0.9",
	})
	if status1 != 200 || len(body1) == 0 {
		return ""
	}
	cookies := collectCookies(nil, header1)
	csrfToken := ""
	if csrfMatch := csrfPattern1.FindStringSubmatch(string(body1)); csrfMatch != nil {
		csrfToken = csrfMatch[1]
	} else if csrfMatch := csrfPattern2.FindStringSubmatch(string(body1)); csrfMatch != nil {
		csrfToken = csrfMatch[1]
	}
	if csrfToken == "" {
		return ""
	}

	// Step 2: post the credentials without following the redirect.
	form := url.Values{
		"csrfmiddlewaretoken": {csrfToken},
		"next":                {next},
		"email":               {email},
		"password":            {password},
	}.Encode()
	status2, header2, _ := rawRequest(client, http.MethodPost, loginURL, form, map[string]string{
		"User-Agent": printablesUserAgent, "Referer": loginURL,
		"Content-Type": "application/x-www-form-urlencoded",
		"Accept":       "text/html,application/xhtml+xml", "Accept-Language": "en-US,en;q=0.9",
		"Origin": "https://account.prusa3d.com", "Cookie": strings.Join(cookies, "; "),
	})
	if status2 < 300 || status2 >= 400 {
		return ""
	}
	cookies = collectCookies(cookies, header2)
	currentURL := absLocation(header2.Get("Location"))

	// Step 3: follow the redirect chain until printables.com?code=…
	authCode := ""
	for index := 0; index < 5 && currentURL != ""; index++ {
		if parsed, failure := url.Parse(currentURL); failure == nil && (parsed.Host == "www.printables.com" || parsed.Host == "printables.com") {
			authCode = parsed.Query().Get("code")
			break
		}
		status3, header3, _ := rawRequest(client, http.MethodGet, currentURL, "", map[string]string{
			"User-Agent": printablesUserAgent, "Cookie": strings.Join(cookies, "; "),
		})
		cookies = collectCookies(cookies, header3)
		currentURL = absLocation(header3.Get("Location"))
		if status3 >= 400 {
			break
		}
	}
	if authCode == "" {
		return ""
	}

	// Step 4: exchange the auth code for an access_token.
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"client_id":     {printablesClientID},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}.Encode()
	tokenRaw, _ := directPost("https://account.prusa3d.com/o/token/", tokenForm, map[string]string{
		"User-Agent": printablesUserAgent, "Content-Type": "application/x-www-form-urlencoded",
	})
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(tokenRaw, &tokenResponse)
	return tokenResponse.AccessToken
}

// relativeNamePattern matches what is stripped from file and folder names.
var relativeNamePattern = regexp.MustCompile(`[^\w\-./() ]`)

// sanitizeRelName cleans a name from the platform API and drops empty, "." and
// ".." segments, so a crafted name cannot escape the download directory through
// filepath.Join. "" when nothing safe remains.
func sanitizeRelName(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	var parts []string
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		parts = append(parts, relativeNamePattern.ReplaceAllString(segment, "_"))
	}
	return strings.Join(parts, "/")
}

func noRedirectClient() *http.Client {
	return &http.Client{
		Timeout:       25 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// rawRequest performs one request without following redirects.
func rawRequest(client *http.Client, method, rawURL, body string, headers map[string]string) (int, http.Header, []byte) {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	request, failure := http.NewRequest(method, rawURL, reader)
	if failure != nil {
		return 0, http.Header{}, nil
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, failure := client.Do(request)
	if failure != nil {
		return 0, http.Header{}, nil
	}
	defer response.Body.Close()
	output, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	return response.StatusCode, response.Header, output
}

// collectCookies appends the name=value pairs, preserving order and dropping
// exact duplicates.
func collectCookies(existing []string, header http.Header) []string {
	seen := map[string]bool{}
	output := make([]string, 0, len(existing))
	for _, cookie := range existing {
		if !seen[cookie] {
			seen[cookie] = true
			output = append(output, cookie)
		}
	}
	for _, setCookie := range header["Set-Cookie"] {
		nameValue := setCookie
		if index := strings.IndexByte(nameValue, ';'); index >= 0 {
			nameValue = nameValue[:index]
		}
		nameValue = strings.TrimSpace(nameValue)
		if nameValue != "" && !seen[nameValue] {
			seen[nameValue] = true
			output = append(output, nameValue)
		}
	}
	return output
}

func absLocation(location string) string {
	location = strings.TrimSpace(location)
	if location != "" && !strings.HasPrefix(location, "http") {
		return "https://account.prusa3d.com" + location
	}
	return location
}

func tokenExpired(timestamp string) bool {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05"} {
		if parsed, failure := time.Parse(layout, timestamp); failure == nil {
			return parsed.Before(time.Now())
		}
	}
	return false
}

func randBytes(length int) []byte {
	randomBytes := make([]byte, length)
	_, _ = rand.Read(randomBytes)
	return randomBytes
}

func randHex(length int) string { return hex.EncodeToString(randBytes(length)) }

func base64URL(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}
