package platforms

// Import of a design whose download links were produced in the visitor's own
// browser (see browser-extension/).
//
// MakerWorld will not hand those links to this server: the endpoint issuing them
// is behind GeeTest, which answers HTTP 418 to anything resolving links in quick
// succession. A visitor pressing the site's own download button produces one link
// per click, and that finished link needs no credential of theirs - tested from a
// second address, it served the same bytes. The links die about five minutes
// after they are issued, which is why this skips the download queue.

import (
	"archive/zip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"meshdepot/internal/logx"
)

// Short enough to go unnoticed, long enough that a platform does not read a
// design's files as a burst - Thingiverse starts refusing after very few.
const browserImportPause = 400 * time.Millisecond

// Well above any real design, so a malformed request cannot turn into an
// unbounded series of downloads.
const maxBrowserImportImages = 24

// BrowserImportFile is one downloadable file as the extension saw it.
type BrowserImportFile struct {
	// As the browser reported it, used only when the CDN does not say better. It
	// can carry a "(1)" the local filesystem added.
	Name string
	URL  string
}

// BrowserImportRequest is one design, collected in the browser.
type BrowserImportRequest struct {
	SourceURL   string
	Platform    string
	SourceID    string
	Name        string
	Author      string
	Description string
	License     string
	CoverURL    string
	// AddToCollection is a flag rather than a name: a client that may choose the
	// name can fill a library with collections through one misspelling.
	AddToCollection bool
	// ImageURLs are the gallery pictures, cover first.
	ImageURLs []string
	Tags      []string
	Files     []BrowserImportFile
	// AttachToDesignID adds the files to that design's newest version instead of
	// creating a design. Set when the model is already in the library - see
	// AddFilesToCurrentVersion.
	AttachToDesignID int
}

// BrowserImportOutcome is what the caller reports back to the extension.
type BrowserImportOutcome struct {
	DesignID   int
	FileCount  int
	SkippedURL []string
	// Name and Description as they were stored, after the platform's own metadata
	// was merged in - which is what a translation should work from.
	Name        string
	Description string
	// Collection is what the design was filed under, "" when none was asked for.
	Collection string
}

// One fixed name: a member who turns the setting on wants their browser imports
// together in one place.
const BrowserImportCollection = "Manual Website Import"

// fileIntoCollection puts a design into the browser-import collection, creating
// it the first time. Matched by name within the account and not by
// source_platform: this collection belongs to the member, not to a platform.
func fileIntoCollection(database *sql.DB, userID, designID int) string {
	var collectionID int
	failure := database.QueryRow(
		"SELECT id FROM collections WHERE user_id = ? AND name = ? LIMIT 1",
		userID, BrowserImportCollection).Scan(&collectionID)

	if failure != nil {
		result, insertFailure := database.Exec(
			"INSERT INTO collections (user_id, name, description) VALUES (?, ?, ?)",
			userID, BrowserImportCollection, "Designs imported with the MeshDepot browser extension.")
		if insertFailure != nil {
			logx.Errorf("[browser-import] could not create the collection for user %d: %v", userID, insertFailure)
			return ""
		}
		newID, _ := result.LastInsertId()
		collectionID = int(newID)
	}

	if _, failure := database.Exec(
		"INSERT OR IGNORE INTO design_collections (design_id, collection_id) VALUES (?, ?)",
		designID, collectionID); failure != nil {
		logx.Errorf("[browser-import] could not file design %d into the collection: %v", designID, failure)
		return ""
	}
	return BrowserImportCollection
}

// ImportFromBrowser downloads the collected links and stores them as a design.
// Files that cannot be fetched are collected in SkippedURL: with a five-minute
// lifetime the last link of a large model can be dead while the first ones are
// fine, and four of five plates beats none.
func ImportFromBrowser(database *sql.DB, owner Owner, request BrowserImportRequest) (BrowserImportOutcome, error) {
	if len(request.Files) == 0 {
		return BrowserImportOutcome{}, errors.New("error.no_files")
	}

	temporaryDirectory, failure := makeTempDir(owner.Layout)
	if failure != nil {
		return BrowserImportOutcome{}, failure
	}

	var files []DownloadedFile
	var skipped []string
	usedNames := map[string]bool{}
	for index, entry := range preferPrintable(request.Files) {
		// Paced: Thingiverse answers 429 to a handful of requests in quick succession.
		if index > 0 {
			time.Sleep(browserImportPause)
		}
		url := strings.TrimSpace(entry.URL)
		if url == "" {
			continue
		}
		name := safeImportName(entry.Name, index)
		// The browser numbers duplicates per download folder, not per model, so two
		// plates can arrive under one name and overwrite each other.
		name = uniqueName(name, usedNames)
		usedNames[strings.ToLower(name)] = true

		destination := filepath.Join(temporaryDirectory, name)
		// Guarded: this address came from a client. See outboundguard.go.
		written, status := streamDownloadGuarded(url, destination, nil)
		if status >= 400 || written < minValidFileBytes {
			_ = os.Remove(destination)
			skipped = append(skipped, url)
			continue
		}
		// The CDN states the real filename, free of what the download folder added.
		// Applied after the fact, so a failed download costs nothing.
		if better := filenameFromDisposition(url); better != "" && !usedNames[strings.ToLower(better)] {
			renamed := filepath.Join(temporaryDirectory, better)
			if os.Rename(destination, renamed) == nil {
				usedNames[strings.ToLower(better)] = true
				destination, name = renamed, better
			}
		}
		files = append(files, unpackIfArchive(temporaryDirectory, destination, name, usedNames)...)
	}

	return finishBrowserImport(database, owner, request, temporaryDirectory, files, skipped)
}

// BrowserUpload is one file the extension carries itself, for platforms that
// hand out no link the server can follow: Thingiverse builds its archive in the
// browser, and a blob: address exists only there.
type BrowserUpload struct {
	Name string
	Open func() (io.ReadCloser, error)
}

// ImportUploadsFromBrowser stores files the extension uploaded rather than links
// to fetch. Everything after the files exist is shared with ImportFromBrowser.
func ImportUploadsFromBrowser(database *sql.DB, owner Owner, request BrowserImportRequest, uploads []BrowserUpload) (BrowserImportOutcome, error) {
	if len(uploads) == 0 {
		return BrowserImportOutcome{}, errors.New("error.no_files:No files were sent with this import.")
	}
	temporaryDirectory, failure := makeTempDir(owner.Layout)
	if failure != nil {
		return BrowserImportOutcome{}, failure
	}

	var files []DownloadedFile
	usedNames := map[string]bool{}
	for index, upload := range uploads {
		name := uniqueName(safeImportName(upload.Name, index), usedNames)
		usedNames[strings.ToLower(name)] = true

		destination := filepath.Join(temporaryDirectory, name)
		written, failure := writeUpload(upload, destination)
		if failure != nil || written < minValidFileBytes {
			_ = os.Remove(destination)
			continue
		}
		files = append(files, unpackIfArchive(temporaryDirectory, destination, name, usedNames)...)
	}
	return finishBrowserImport(database, owner, request, temporaryDirectory, files, nil)
}

// writeUpload streams one uploaded file to disk. A model archive is measured in
// hundreds of megabytes, and holding one per request exhausts the container.
func writeUpload(upload BrowserUpload, destination string) (int64, error) {
	reader, failure := upload.Open()
	if failure != nil {
		return 0, failure
	}
	defer reader.Close()

	output, failure := os.Create(destination)
	if failure != nil {
		return 0, failure
	}
	written, copyFailure := io.Copy(output, io.LimitReader(reader, maxDownloadBytes))
	closeFailure := output.Close()
	if copyFailure != nil || closeFailure != nil {
		return 0, errors.New("could not write the uploaded file")
	}
	return written, nil
}

// finishBrowserImport is everything both routes share.
func finishBrowserImport(database *sql.DB, owner Owner, request BrowserImportRequest,
	temporaryDirectory string, files []DownloadedFile, skipped []string) (BrowserImportOutcome, error) {

	// Again, now that any archive has been opened: the first pass judges what to
	// download and cannot see inside a ZIP.
	files = preferPrintableFiles(files)
	if len(files) == 0 {
		return BrowserImportOutcome{SkippedURL: skipped}, errors.New(
			"error.browser_import_no_files:None of the files could be downloaded. " +
				"The links may have expired, or the platform may only hand them to the browser that asked.")
	}

	// The model is in the library already: its files join the newest version, and
	// nothing below applies - name, pictures and tags belong to the design and are
	// stored. Creating a design is untouched by this.
	if request.AttachToDesignID > 0 {
		added, failure := AddFilesToCurrentVersion(database, owner, request.AttachToDesignID, files)
		if failure != nil {
			return BrowserImportOutcome{SkippedURL: skipped}, failure
		}
		return BrowserImportOutcome{
			DesignID: request.AttachToDesignID, FileCount: added, SkippedURL: skipped,
		}, nil
	}

	enrichFromPlatform(&request)

	result := Result{
		Name:        strings.TrimSpace(request.Name),
		Description: strings.TrimSpace(request.Description),
		Author:      strings.TrimSpace(request.Author),
		SourceID:    strings.TrimSpace(request.SourceID),
		Tags:        uniqueStrings(request.Tags),
		Files:       files,
	}
	// Cover first, then the gallery. Deduplicated, because the cover is usually the
	// first gallery picture as well.
	var imageURLs []string
	seenImages := map[string]bool{}
	for _, candidate := range append([]string{request.CoverURL}, request.ImageURLs...) {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" || seenImages[trimmed] {
			continue
		}
		seenImages[trimmed] = true
		imageURLs = append(imageURLs, trimmed)
		if len(imageURLs) >= maxBrowserImportImages {
			break
		}
	}
	if len(imageURLs) > 0 {
		if stored := downloadAllImagesGuarded(owner.Layout, imageURLs); len(stored) > 0 {
			result.CoverPath = stored[0]
			result.AllImages = stored
		}
	}

	designID, failure := SaveDownload(database, owner, request.Platform, request.SourceURL, result)
	if failure != nil {
		return BrowserImportOutcome{SkippedURL: skipped}, failure
	}

	outcome := BrowserImportOutcome{
		DesignID: designID, FileCount: len(files), SkippedURL: skipped,
		Name: result.Name, Description: result.Description,
	}
	if request.AddToCollection {
		// After the design exists, and never fatal: failing now would report an error
		// for a design that is in the library.
		outcome.Collection = fileIntoCollection(database, owner.ID, designID)
	}
	return outcome, nil
}

// filenameFromDisposition asks the CDN what the file is called. A HEAD is cheap
// next to the download that just happened, and the answer is better than the
// browser's: "GTA6_LOGO++V2.3mf" rather than "GTA6_LOGO++V2(1).3mf".
func filenameFromDisposition(url string) string {
	request, failure := newOutboundRequest(http.MethodHead, url, "", nil)
	if failure != nil {
		return ""
	}
	response, failure := guardedDownloadClient().Do(request)
	if failure != nil {
		return ""
	}
	defer response.Body.Close()
	_, parameters, failure := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	if failure != nil {
		return ""
	}
	return sanitiseImportName(parameters["filename"])
}

// safeImportName turns whatever the browser reported into a usable filename.
func safeImportName(reported string, index int) string {
	if cleaned := sanitiseImportName(reported); cleaned != "" {
		return cleaned
	}
	return fmt.Sprintf("file-%d.3mf", index+1)
}

// sanitiseImportName strips any path from a reported name and refuses the ones
// that would escape the temp directory. The name comes from a browser.
func sanitiseImportName(reported string) string {
	name := strings.TrimSpace(reported)
	if name == "" {
		return ""
	}
	name = filepath.Base(filepath.FromSlash(strings.ReplaceAll(name, "\\", "/")))
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return ""
	}
	if len(name) > 180 {
		extension := filepath.Ext(name)
		name = name[:180-len(extension)] + extension
	}
	return name
}

// uniqueName returns a name not yet in taken, adding "-2", "-3", … before the
// extension. Case-insensitive, so it also holds on a filesystem that is.
func uniqueName(name string, taken map[string]bool) string {
	if !taken[strings.ToLower(name)] {
		return name
	}
	extension := filepath.Ext(name)
	base := strings.TrimSuffix(name, extension)
	for counter := 2; ; counter++ {
		candidate := fmt.Sprintf("%s-%d%s", base, counter, extension)
		if !taken[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

// enrichFromPlatform fills in what the browser could not read: Printables loads
// its description only when the reader asks for it, so the extension sees the
// summary line and nothing more. Only gaps are filled - except the description,
// where the longer text wins, which is the whole reason this exists.
func enrichFromPlatform(request *BrowserImportRequest) {
	public, ok := PublicMetadata(request.Platform, request.SourceURL)
	if !ok {
		return
	}
	mergePublicMetadata(request, public)
}

// mergePublicMetadata is the rule alone, so it can be tested without a network.
func mergePublicMetadata(request *BrowserImportRequest, public PublicMeta) {
	if strings.TrimSpace(request.Name) == "" {
		request.Name = public.Name
	}
	if strings.TrimSpace(request.Author) == "" {
		request.Author = public.Author
	}
	if len(strings.TrimSpace(public.Description)) > len(strings.TrimSpace(request.Description)) {
		request.Description = public.Description
	}
	if len(request.Tags) == 0 {
		request.Tags = public.Tags
	}
	// Appended rather than replacing: each side sees pictures the other does not.
	// SaveDownload deduplicates by content.
	request.ImageURLs = append(request.ImageURLs, public.ImageURLs...)
}

// unpackIfArchive turns a container into the files it holds and leaves a model
// alone, decided by content rather than by name: Chrome does not know a
// download's filename when it reports it, and a Printables archive was filed as
// one model instead of the eight files inside it. A 3MF is a ZIP too, but it
// always carries its model under 3D/. Anything unreadable is kept as it arrived.
func unpackIfArchive(temporaryDirectory, path, name string, usedNames map[string]bool) []DownloadedFile {
	asItArrived := []DownloadedFile{{TempPath: path, Name: name}}

	reader, failure := zip.OpenReader(path)
	if failure != nil {
		return asItArrived
	}
	isModel := isModelPackage(&reader.Reader)
	reader.Close()
	if isModel {
		return asItArrived
	}

	extracted := extractZipFile(temporaryDirectory, path)
	if len(extracted) == 0 {
		return asItArrived
	}
	_ = os.Remove(path)
	for _, entry := range extracted {
		usedNames[strings.ToLower(entry.Name)] = true
	}
	return extracted
}

// isModelPackage reports whether a ZIP is a 3MF rather than a bag of files. The
// 3D/….model marker is read from the central directory, extracting nothing.
func isModelPackage(reader *zip.Reader) bool {
	for _, entry := range reader.File {
		name := strings.ToLower(strings.ReplaceAll(entry.Name, "\\", "/"))
		if strings.HasPrefix(name, "3d/") && strings.HasSuffix(name, ".model") {
			return true
		}
	}
	return false
}

// preferPrintableFiles narrows a file list to what belongs in a model library:
// a platform lists the brochure and the wiring diagram beside the parts. The
// same rule the Thingiverse downloader follows, with the same escape - when
// nothing looks printable the whole list is kept.
func preferPrintableFiles(files []DownloadedFile) []DownloadedFile {
	var printable []DownloadedFile
	for _, file := range files {
		if printableExtPattern.MatchString(file.Name) {
			printable = append(printable, file)
		}
	}
	if len(printable) == 0 {
		return files
	}
	// The ones left behind are in the temp directory and go with it.
	return printable
}

func preferPrintable(entries []BrowserImportFile) []BrowserImportFile {
	var printable []BrowserImportFile
	for _, entry := range entries {
		name := entry.Name
		if name == "" {
			// No name from the client - judge by the address, which usually holds one.
			if parsed, failure := url.Parse(entry.URL); failure == nil {
				name = path.Base(parsed.Path)
			}
		}
		if printableExtPattern.MatchString(name) {
			printable = append(printable, entry)
		}
	}
	if len(printable) == 0 {
		return entries
	}
	return printable
}
