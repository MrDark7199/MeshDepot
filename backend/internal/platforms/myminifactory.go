package platforms

// MyMiniFactory downloader. Metadata via the REST API v2 with an API key (the api
// path is not CF-gated). The file download endpoint /download is Cloudflare-bot-gated
// (blocks Go/Chromium → 403): the /download URLs are resolved to their presigned S3
// URLs via the Playwright/Firefox sidecar and then loaded CF-free from S3 (fallback:
// direct, if PLAYWRIGHT_URL is empty). ZIP files are extracted, HTML responses
// discarded.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"meshdepot/internal/coerce"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	myMiniFactorySlugPattern = regexp.MustCompile(`(?i)myminifactory\.com/object/([^/?#]+)`)
	myMiniFactoryIDPattern   = regexp.MustCompile(`-(\d+)$`)
	// myMiniFactoryDescriptionBreakPattern matches runs of 2+ horizontal
	// whitespaces (incl. nbsp).
	myMiniFactoryDescriptionBreakPattern = regexp.MustCompile(`[ \t\x{00a0}]{2,}`)
)

// normalizeMyMiniFactoryDescription reconstructs line breaks in MMF
// descriptions. The API v2 returns the description as flat text without real
// breaks/HTML: original block/line boundaries are encoded as runs of 2+ spaces
// (or nbsp), single spaces separate words within a line. We turn each 2+
// whitespace run into a '\n' and trim the lines so the structure survives
// translation (which keeps '\n') and display.
func normalizeMyMiniFactoryDescription(text string) string {
	if text == "" {
		return text
	}
	normalized := myMiniFactoryDescriptionBreakPattern.ReplaceAllString(text, "\n")
	lines := strings.Split(normalized, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSpace(line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// MyMiniFactory implements Downloader for myminifactory.com.
type MyMiniFactory struct{ Deps }

// Download loads a MyMiniFactory object via the REST API v2.
func (myMiniFactory MyMiniFactory) Download(sourceURL string, owner Owner, progress func(step, label string, current, total int)) (Result, error) {
	// The numeric id is only for credentials; every path comes from owner.Layout.
	userID := owner.ID
	if progress == nil {
		progress = func(string, string, int, int) {}
	}

	slugMatch := myMiniFactorySlugPattern.FindStringSubmatch(sourceURL)
	if slugMatch == nil {
		return Result{}, fmt.Errorf("Cannot extract MyMiniFactory object ID from URL: %s", sourceURL)
	}
	slug := slugMatch[1]
	objectID := slug
	if idMatch := myMiniFactoryIDPattern.FindStringSubmatch(slug); idMatch != nil {
		objectID = idMatch[1]
	}

	apiKey := myMiniFactory.PlatformToken(userID, "myminifactory")
	if apiKey == "" {
		return Result{}, fmt.Errorf("error.myminifactory_no_token:MyMiniFactory requires an API key. " +
			"Get your free API key at myminifactory.com/auth/api-key and add it in Account Settings → Platforms → MyMiniFactory.")
	}

	progress("fetching_metadata", "", 0, 0)
	apiURL := "https://www.myminifactory.com/api/v2/objects/" + objectID + "?key=" + url.QueryEscape(apiKey)
	raw, status := directGet(apiURL, map[string]string{
		"Accept":     "application/json",
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	})

	var data struct {
		ID       json.RawMessage `json:"id"`
		Status   int             `json:"status"`
		Name     string          `json:"name"`
		Desc     string          `json:"description"`
		Designer struct {
			Username string `json:"username"`
		} `json:"designer"`
		Tags   []json.RawMessage `json:"tags"`
		Images []struct {
			Original struct {
				URL string `json:"url"`
			} `json:"original"`
			Thumbnail struct {
				URL string `json:"url"`
			} `json:"thumbnail"`
		} `json:"images"`
		Files struct {
			Items []map[string]any `json:"items"`
		} `json:"files"`
	}
	_ = json.Unmarshal(raw, &data)

	if status == 401 || data.Status == 401 {
		return Result{}, fmt.Errorf("error.myminifactory_auth_failed:MyMiniFactory API key is invalid or expired. " +
			"Check your API key in Account Settings → Platforms → MyMiniFactory.")
	}
	if status != 200 || len(data.ID) == 0 || string(data.ID) == "null" || string(data.ID) == `""` {
		preview := string(raw)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		if preview == "" {
			preview = "(empty)"
		}
		return Result{}, MetadataError("MyMiniFactory: object %s not found (HTTP %d). Response: %s", objectID, status, preview)
	}

	name := data.Name
	if name == "" {
		name = "MyMiniFactory #" + objectID
	}
	author := data.Designer.Username
	tags := parseMyMiniFactoryTags(data.Tags)

	var imageURLs []string
	for _, image := range data.Images {
		if image.Original.URL != "" {
			imageURLs = append(imageURLs, image.Original.URL)
		} else if image.Thumbnail.URL != "" {
			imageURLs = append(imageURLs, image.Thumbnail.URL)
		}
	}

	fileList := data.Files.Items
	if len(fileList) == 0 {
		return Result{}, FilesError("error.myminifactory_no_files:MyMiniFactory object %s has no files.", objectID)
	}

	// If all download_urls are null → web fallback endpoint.
	nullCount := 0
	for _, fileEntry := range fileList {
		if coerce.Text(fileEntry["download_url"]) == "" {
			nullCount++
		}
	}
	if nullCount == len(fileList) {
		webURL := "https://www.myminifactory.com/download/" + objectID + "?key=" + url.QueryEscape(apiKey)
		firstName := coerce.Text(fileList[0]["filename"])
		if firstName == "" {
			firstName = coerce.Text(fileList[0]["name"])
		}
		if firstName == "" {
			firstName = name + ".stl"
		}
		fileList = []map[string]any{{"download_url": webURL, "filename": firstName}}
	}

	tempDir, failure := makeTempDir(owner.Layout)
	if failure != nil {
		return Result{}, failure
	}

	// Pre-build the download URLs (with API key).
	downloadURLs := make([]string, len(fileList))
	for index, fileEntry := range fileList {
		downloadURL := coerce.Text(fileEntry["download_url"])
		if downloadURL != "" && !strings.Contains(downloadURL, "key=") {
			separator := "?"
			if strings.Contains(downloadURL, "?") {
				separator = "&"
			}
			downloadURL += separator + "key=" + url.QueryEscape(apiKey)
		}
		downloadURLs[index] = downloadURL
	}

	// Cloudflare bypass: /download is CF-bot-gated. With the sidecar resolve the
	// 302→presigned-S3 URLs via Firefox; without the sidecar a direct attempt (fallback).
	var resolved map[string]string
	if myMiniFactory.Cfg.PlaywrightURL != "" {
		resolved = resolveMyMiniFactoryURLs(myMiniFactory.Cfg.PlaywrightURL, downloadURLs)
	}

	var files []DownloadedFile
	total := len(fileList)
	progress("downloading_files", "", 0, total)
	for index, fileEntry := range fileList {
		progress("downloading_files", "", index+1, total)
		downloadURL := downloadURLs[index]
		if downloadURL == "" {
			continue
		}
		fileName := coerce.Text(fileEntry["filename"])
		if fileName == "" {
			fileName = coerce.Text(fileEntry["name"])
		}
		if fileName == "" {
			fileName = fmt.Sprintf("file_%d.stl", len(files))
		}

		// With the sidecar only load the CF-free resolved S3 URL; without the
		// sidecar try the /download URL directly.
		fetchURL := downloadURL
		if myMiniFactory.Cfg.PlaywrightURL != "" {
			resolvedS3URL := resolved[downloadURL]
			if resolvedS3URL == "" {
				continue // CF resolve failed → skip this file
			}
			fetchURL = resolvedS3URL
		}

		extension := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
		rawFilePath := filepath.Join(tempDir, fmt.Sprintf("mmf_raw_%d.%s", index, extension))
		size, code := streamDownload(fetchURL, rawFilePath, map[string]string{"User-Agent": userAgent})

		if code != 200 || size < minValidFileBytes {
			_ = os.Remove(rawFilePath)
			continue
		}

		magic := readMagic(rawFilePath, 4)
		if strings.HasPrefix(string(magic), "<!") {
			_ = os.Remove(rawFilePath)
			continue
		}
		isZipArchive := string(magic) == "PK\x03\x04"

		if extension == "zip" || isZipArchive {
			progress("extracting_files", "", 0, 0)
			files = append(files, extractZipFile(tempDir, rawFilePath)...)
			_ = os.Remove(rawFilePath)
		} else {
			destination := filepath.Join(tempDir, sanitizeFileName(fileName))
			if os.Rename(rawFilePath, destination) == nil {
				files = append(files, DownloadedFile{TempPath: destination, Name: fileName})
			}
		}
	}

	if len(files) == 0 {
		return Result{}, FilesError("error.myminifactory_download_failed:All files from MyMiniFactory object %s failed to download. "+
			"Check your API key in Account Settings → Platforms → MyMiniFactory.", objectID)
	}

	progress("downloading_images", "", 0, len(imageURLs))
	allImages := downloadAllImages(owner.Layout, imageURLs, progress)
	var coverPath string
	if len(allImages) > 0 {
		coverPath = allImages[0]
	}

	return Result{
		Name:        name,
		Description: normalizeMyMiniFactoryDescription(data.Desc),
		Author:      author,
		SourceID:    objectID,
		CoverPath:   coverPath,
		AllImages:   allImages,
		Tags:        tags,
		Files:       files,
	}, nil
}

// ValidateReason checks the API key against the MMF REST API. The key alone is
// sufficient (no login/session needed): /search returns HTTP 200 with a valid
// key, HTTP 401 with an invalid/expired key. A return of "" means valid.
func (myMiniFactory MyMiniFactory) ValidateReason(credentials Credentials) string {
	apiKey := strings.TrimSpace(credentials.Token)
	if apiKey == "" {
		return "error.myminifactory_invalid_api_key"
	}
	_, status := directGet("https://www.myminifactory.com/api/v2/search?q=test&per_page=1&key="+url.QueryEscape(apiKey), map[string]string{
		"Accept":     "application/json",
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	})
	if status == 200 {
		return ""
	}
	return "error.myminifactory_invalid_api_key"
}

// resolveMyMiniFactoryURLs resolves the CF-bot-gated /download URLs to their
// presigned S3 URLs via the Playwright/Firefox sidecar (POST
// /resolve/myminifactory). Firefox passes Cloudflare and reads the 302 Location;
// the actual file is then loaded directly from S3 by the Go worker. Returns: map
// download-URL→S3-URL (partly/entirely empty on errors; the caller skips
// unresolved files).
func resolveMyMiniFactoryURLs(playwrightURL string, urls []string) map[string]string {
	resolvedURLs := map[string]string{}
	var clean []string
	for _, candidate := range urls {
		if candidate != "" {
			clean = append(clean, candidate)
		}
	}
	if len(clean) == 0 {
		return resolvedURLs
	}
	requestBody, _ := json.Marshal(map[string]any{"urls": clean})
	endpoint := strings.TrimRight(playwrightURL, "/") + "/resolve/myminifactory"
	request, failure := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if failure != nil {
		return resolvedURLs
	}
	request.Header.Set("Content-Type", "application/json")
	applyPlaywrightAuth(request)
	client := &http.Client{Timeout: 180 * time.Second}
	response, failure := client.Do(request)
	if failure != nil {
		return resolvedURLs
	}
	defer response.Body.Close()
	var parsed struct {
		Resolved []struct {
			URL string `json:"url"`
			S3  string `json:"s3"`
		} `json:"resolved"`
	}
	if json.NewDecoder(response.Body).Decode(&parsed) != nil {
		return resolvedURLs
	}
	for _, entry := range parsed.Resolved {
		if entry.S3 != "" {
			resolvedURLs[entry.URL] = entry.S3
		}
	}
	return resolvedURLs
}

// parseMyMiniFactoryTags normalizes the tags (string or {name}) to strings.
func parseMyMiniFactoryTags(rawTags []json.RawMessage) []string {
	var tags []string
	for _, rawTag := range rawTags {
		var text string
		if json.Unmarshal(rawTag, &text) == nil && text != "" {
			tags = append(tags, text)
			continue
		}
		var object struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(rawTag, &object) == nil && object.Name != "" {
			tags = append(tags, object.Name)
		}
	}
	return tags
}

// readMagic reads the first byteCount bytes of a file (for format detection).
func readMagic(filePath string, byteCount int) []byte {
	file, failure := os.Open(filePath)
	if failure != nil {
		return nil
	}
	defer file.Close()
	buffer := make([]byte, byteCount)
	read, _ := file.Read(buffer)
	return buffer[:read]
}
