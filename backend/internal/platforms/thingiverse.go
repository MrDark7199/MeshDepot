package platforms

// Thingiverse downloader. Loads a design via the Thingiverse REST API (bearer
// token, no browser); the model files are fetched via Tor with retry because of
// CDN IP blocks.

import (
	"fmt"
	"log"
	"meshdepot/internal/coerce"
	"net/url"
	"path/filepath"
	"regexp"
)

// thingIDPattern extracts the thing ID from a Thingiverse URL.
var thingIDPattern = regexp.MustCompile(`(?i)(?:thing[:\-/])(\d+)`)

// printableExtPattern recognizes printable file extensions (preferred selection).
var printableExtPattern = regexp.MustCompile(`(?i)\.(stl|3mf|obj|step|stp)$`)

// Thingiverse implements Downloader for thingiverse.com.
type Thingiverse struct{ Deps }

// Download loads a Thingiverse design via the REST API.
func (thingiverse Thingiverse) Download(sourceURL string, owner Owner, progress func(step, label string, current, total int)) (Result, error) {
	// The numeric id is only for credentials; every path comes from owner.Layout.
	userID := owner.ID
	if progress == nil {
		progress = func(string, string, int, int) {}
	}

	match := thingIDPattern.FindStringSubmatch(sourceURL)
	if match == nil {
		return Result{}, fmt.Errorf("Cannot extract Thingiverse thing ID from URL: %s", sourceURL)
	}
	thingID := match[1]

	token := thingiverse.PlatformToken(userID, "thingiverse")

	progress("fetching_metadata", "", 0, 0)
	thingData, _ := thingiverseAPI("https://api.thingiverse.com/things/"+thingID, token).(map[string]any)
	if coerce.Text(thingData["name"]) == "" {
		if token == "" {
			return Result{}, fmt.Errorf("Thingiverse requires an App Token even for public models. " +
				"Create one at thingiverse.com/apps/create, then add it in Account Settings → Platforms → Thingiverse.")
		}
		return Result{}, MetadataError("error.thingiverse_auth_failed:Thingiverse API returned no data for thing %s. "+
			"Your token may be invalid or expired. Update it in Account Settings → Platforms → Thingiverse.", thingID)
	}

	name := coerce.Text(thingData["name"])
	description := coerce.Text(thingData["description"])
	var author string
	if creator, ok := thingData["creator"].(map[string]any); ok {
		author = coerce.Text(creator["name"])
	}
	var tags []string
	if rawTags, ok := thingData["tags"].([]any); ok {
		for _, rawTag := range rawTags {
			if tagMap, ok := rawTag.(map[string]any); ok {
				if tagName := coerce.Text(tagMap["name"]); tagName != "" {
					tags = append(tags, tagName)
				}
			}
		}
	}

	// Images via the dedicated images endpoint (largest size preferred).
	allImageURLs := thingiverse.collectImageURLs(thingID, token, thingData)

	fileList, _ := thingiverseAPI("https://api.thingiverse.com/things/"+thingID+"/files", token).([]any)
	if len(fileList) == 0 {
		return Result{}, FilesError("No files found for Thingiverse thing %s", thingID)
	}

	// Prefer printable files, otherwise all.
	var printable, all []map[string]any
	for _, rawFile := range fileList {
		fileMap, ok := rawFile.(map[string]any)
		if !ok {
			continue
		}
		all = append(all, fileMap)
		if printableExtPattern.MatchString(coerce.Text(fileMap["name"])) {
			printable = append(printable, fileMap)
		}
	}
	filesToDownload := printable
	if len(filesToDownload) == 0 {
		filesToDownload = all
	}

	tempDir, failure := makeTempDir(owner.Layout)
	if failure != nil {
		return Result{}, failure
	}

	var files []DownloadedFile
	total := len(filesToDownload)
	progress("downloading_files", "", 0, total)
	for index, fileEntry := range filesToDownload {
		downloadURL := coerce.Text(fileEntry["download_url"])
		fileName := coerce.Text(fileEntry["name"])
		if fileName == "" {
			fileName = fmt.Sprintf("file_%d", len(files))
		}
		if downloadURL == "" {
			continue
		}

		// api.thingiverse.com/v2/files/.../download returns the file directly (200).
		// Direct first; only on an IP block (403/soft block: HTML/JSON instead of file)
		// retry via Tor with a route change - datacenter IPs are partly blocked, Tor
		// exits conversely blocked by Thingiverse itself.
		authHeader := map[string]string{"Authorization": "Bearer " + token}
		tempPath := filepath.Join(tempDir, sanitizeFileName(fileName))
		stored := downloadFileTo(downloadClient(), downloadURL, tempPath, authHeader)
		if !stored {
			for attempt := 1; attempt <= 3; attempt++ {
				if thingiverse.torDownloadFileTo(downloadURL, tempPath, authHeader) {
					stored = true
					break
				}
				log.Printf("[dl] %s: Tor attempt %d failed", fileName, attempt)
			}
		}
		if !stored {
			continue
		}
		files = append(files, DownloadedFile{TempPath: tempPath, Name: fileName})
		progress("downloading_files", "", index+1, total)
	}

	if len(files) == 0 {
		return Result{}, FilesError("No files could be downloaded from Thingiverse thing %s", thingID)
	}

	var allImagePaths []string
	progress("downloading_images", "", 0, len(allImageURLs))
	for index, imageURL := range allImageURLs {
		if storedPath := downloadCover(owner.Layout, imageURL); storedPath != "" {
			allImagePaths = append(allImagePaths, storedPath)
		}
		progress("downloading_images", "", index+1, len(allImageURLs))
	}
	var coverPath string
	if len(allImagePaths) > 0 {
		coverPath = allImagePaths[0]
	}

	return Result{
		Name:        name,
		Description: description,
		Author:      author,
		SourceID:    thingID,
		CoverPath:   coverPath,
		AllImages:   allImagePaths,
		Tags:        tags,
		Files:       files,
	}, nil
}

// collectImageURLs fetches the image URLs (largest size preferred) from the
// images endpoint with a fallback to default_image.
func (thingiverse Thingiverse) collectImageURLs(thingID, token string, thingData map[string]any) []string {
	var urls []string
	imageList, _ := thingiverseAPI("https://api.thingiverse.com/things/"+thingID+"/images", token).([]any)
	for _, rawImage := range imageList {
		imageMap, ok := rawImage.(map[string]any)
		if !ok {
			continue
		}
		sizes, _ := imageMap["sizes"].([]any)
		var picked string
		for _, want := range []string{"large", "medium", "small", "thumb"} {
			for _, rawSize := range sizes {
				sizeEntry, ok := rawSize.(map[string]any)
				if !ok {
					continue
				}
				if coerce.Text(sizeEntry["type"]) == "display" && coerce.Text(sizeEntry["size"]) == want {
					picked = coerce.Text(sizeEntry["url"])
					break
				}
			}
			if picked != "" {
				break
			}
		}
		if picked == "" {
			picked = coerce.Text(imageMap["url"])
		}
		if picked != "" {
			urls = append(urls, picked)
		}
	}
	// Fallback: default_image
	if len(urls) == 0 {
		if defaultImage, ok := thingData["default_image"].(map[string]any); ok {
			if imageURL := coerce.Text(defaultImage["url"]); imageURL != "" {
				urls = append(urls, imageURL)
			}
		}
	}
	return urls
}

// Validate checks a Thingiverse app token via /users/me.
func (thingiverse Thingiverse) Validate(credentials Credentials) bool {
	return thingiverse.ValidateReason(credentials) == ""
}

// ValidateReason checks token and username separately and returns an i18n key
// for the exact failure cause (or "" on success), so the frontend can show a
// precise message.
func (thingiverse Thingiverse) ValidateReason(credentials Credentials) string {
	if credentials.Token == "" {
		return "error.platform_invalid_token"
	}
	// 1) Token valid? /users/me must return the own account.
	me, _ := thingiverseAPI("https://api.thingiverse.com/users/me", credentials.Token).(map[string]any)
	if coerce.Text(me["name"]) == "" {
		log.Printf("[tv-validate] token invalid: /users/me keys=%v", mapKeys(me))
		return "error.platform_invalid_token"
	}
	// 2) Username (if given) must be a real Thingiverse user - the library sync
	// calls /users/{username}/likes; a typo would otherwise silently run into the
	// void. NOT compared by equality with /users/me (the own display name may
	// differ), but: is /users/{username} resolvable?
	if credentials.Email != "" {
		resolvedUser, _ := thingiverseAPI("https://api.thingiverse.com/users/"+url.PathEscape(credentials.Email), credentials.Token).(map[string]any)
		if coerce.Text(resolvedUser["name"]) == "" {
			log.Printf("[tv-validate] username %q not resolvable, keys=%v", credentials.Email, mapKeys(resolvedUser))
			return "error.platform_invalid_username"
		}
	}
	return ""
}

// mapKeys returns the keys of a JSON map (only for debug logging).
func mapKeys(input map[string]any) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	return keys
}

// isFileBody checks whether a response body is a real file (not an empty body
// and not an HTML/JSON error page of a soft block).
func isFileBody(data []byte) bool {
	return len(data) >= minSmallFileBytes && data[0] != '<' && data[0] != '{'
}
