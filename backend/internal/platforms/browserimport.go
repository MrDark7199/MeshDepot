package platforms

// Import of a design whose download links were produced in the visitor's own
// browser (see browser-extension/).
//
// It exists because MakerWorld will not hand those links to this server. The
// endpoint that issues them is behind GeeTest, which answers HTTP 418 to
// anything that resolves links in quick succession - a signed-in Firefox
// included, so it is about pace and reputation rather than about detecting
// automation. A visitor pressing the site's own download button produces one
// link per click, with whatever headers and captcha the site wants, and there is
// nothing left for the wall to object to.
//
// What arrives here is that finished link. The signature covers the path alone:
// tested from a second address over Tor, the same URL served the same bytes, so
// this server can fetch it without any credential of the visitor's. Nothing
// about their platform account is needed, stored or seen.
//
// The links expire about five minutes after they are issued, which is why this
// runs immediately instead of going through the download queue.

import (
	"archive/zip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// browserImportPause separates one file download from the next. Short enough to
// go unnoticed, long enough that a platform does not read a design's files as a
// burst - Thingiverse starts refusing after very few.
const browserImportPause = 400 * time.Millisecond

// maxBrowserImportImages bounds one import's gallery. Well above any real design
// and low enough that a malformed request cannot turn into an unbounded series
// of downloads.
const maxBrowserImportImages = 24

// BrowserImportFile is one downloadable file as the extension saw it.
type BrowserImportFile struct {
	// Name as the browser reported it, used only when the CDN does not say
	// better. It can carry a "(1)" the local filesystem added.
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
	// AddToCollection puts the finished design into a collection of its own.
	//
	// A flag rather than a name, and the name is this package's: a client that may
	// choose it can create any number of collections in someone's library with one
	// misspelling, and there is nothing here that needs the freedom.
	AddToCollection bool
	// ImageURLs are the gallery pictures, cover first. A design with one photo
	// out of eight looks half-imported, and the pictures are the part a person
	// recognises the model by.
	ImageURLs []string
	Tags      []string
	Files     []BrowserImportFile
}

// BrowserImportOutcome is what the caller reports back to the extension.
type BrowserImportOutcome struct {
	DesignID   int
	FileCount  int
	SkippedURL []string
	// Name and Description as they were stored - after the platform's own
	// metadata was merged in, which is what the translation should work from.
	Name        string
	Description string
	// Collection is the collection the design was filed under, "" when none was
	// asked for.
	Collection string
}

// BrowserImportCollection is where designs go when the extension asks for a
// collection. One fixed name: a member who turns the setting on wants their
// browser imports together in one place, and a name per import would defeat
// that.
const BrowserImportCollection = "Manual Website Import"

// fileIntoCollection puts a design into the browser-import collection, creating
// it the first time.
//
// Matched by name within the account, and deliberately not by source_platform:
// this collection belongs to the member, not to a platform, and the designs in
// it come from all of them. A collection they renamed is left alone - a new one
// is made rather than reaching into a name somebody chose.
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
			log.Printf("[browser-import] could not create the collection for user %d: %v", userID, insertFailure)
			return ""
		}
		newID, _ := result.LastInsertId()
		collectionID = int(newID)
	}

	if _, failure := database.Exec(
		"INSERT OR IGNORE INTO design_collections (design_id, collection_id) VALUES (?, ?)",
		designID, collectionID); failure != nil {
		log.Printf("[browser-import] could not file design %d into the collection: %v", designID, failure)
		return ""
	}
	return BrowserImportCollection
}

// ImportFromBrowser downloads the collected links and stores them as a design.
//
// Files that cannot be fetched are collected in SkippedURL rather than failing
// the whole import: with a five-minute lifetime the last link of a large model
// can be dead while the first ones are fine, and importing four of five plates
// beats importing none. Only when nothing at all arrives is this an error.
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
		// Paced. Thingiverse answers 429 to a handful of requests in quick
		// succession, and a design with ten files would otherwise fetch the first
		// two and be turned away for the rest.
		if index > 0 {
			time.Sleep(browserImportPause)
		}
		url := strings.TrimSpace(entry.URL)
		if url == "" {
			continue
		}
		name := safeImportName(entry.Name, index)
		// Two plates can arrive under one name - the browser numbers duplicates
		// per download folder, not per model. Left alone they would overwrite
		// each other in the temp directory and the import would silently lose a
		// file.
		name = uniqueName(name, usedNames)
		usedNames[strings.ToLower(name)] = true

		destination := filepath.Join(temporaryDirectory, name)
		// Guarded: this address came from a client, not from the platform code in
		// this package. See outboundguard.go.
		written, status := streamDownloadGuarded(url, destination, nil)
		if status >= 400 || written < minValidFileBytes {
			_ = os.Remove(destination)
			skipped = append(skipped, url)
			continue
		}
		// The CDN states the real filename, free of anything the local download
		// folder added. Applied after the fact so a failed download costs nothing.
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

// BrowserUpload is one file the extension carries itself.
//
// Needed because not every platform hands out a link the server can follow.
// Thingiverse builds its archive in the browser - that is the countdown before
// the download starts - and the result is a blob: address that exists only in
// that browser. There is nothing to fetch, so the bytes travel instead.
type BrowserUpload struct {
	Name string
	Open func() (io.ReadCloser, error)
}

// ImportUploadsFromBrowser stores files the extension uploaded, rather than
// links for the server to fetch. Everything after the files exist is shared with
// ImportFromBrowser.
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

// writeUpload streams one uploaded file to disk. Streamed rather than read into
// memory: a model archive is measured in hundreds of megabytes, and holding one
// per request is how a container runs out of memory.
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

// finishBrowserImport is everything both routes share: the metadata, the images
// and the design itself.
func finishBrowserImport(database *sql.DB, owner Owner, request BrowserImportRequest,
	temporaryDirectory string, files []DownloadedFile, skipped []string) (BrowserImportOutcome, error) {

	// Again, now that any archive has been opened. The first pass judges what to
	// download and cannot see inside a ZIP - and a Printables download is exactly
	// that: one archive whose contents are only known afterwards, brochure
	// included.
	files = preferPrintableFiles(files)
	if len(files) == 0 {
		return BrowserImportOutcome{SkippedURL: skipped}, errors.New(
			"error.browser_import_no_files:None of the files could be downloaded. " +
				"The links may have expired, or the platform may only hand them to the browser that asked.")
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
	// Cover first, then the rest of the gallery. Deduplicated because the cover is
	// usually the first gallery picture as well, and importing it twice would put
	// the same photo in the design twice.
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
		// After the design exists, and never fatal: the import succeeded, and
		// failing it now would leave a design in the library reported as an error.
		outcome.Collection = fileIntoCollection(database, owner.ID, designID)
	}
	return outcome, nil
}

// filenameFromDisposition asks the CDN what the file is called.
//
// A HEAD is cheap next to the download that just happened, and the answer is
// better than the browser's: "GTA6_LOGO++V2.3mf" rather than
// "GTA6_LOGO++V2(1).3mf", where the "(1)" only ever meant that the visitor's
// download folder already held a file of that name.
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
// that would escape the temp directory. The name reaches here from a browser,
// so it is treated as input rather than as fact.
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
// extension. Comparison is case-insensitive so it also holds on a filesystem
// that does not distinguish the two.
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

// enrichFromPlatform fills in what the browser could not read.
//
// A page is not always a complete source: Printables loads its description only
// when the reader asks for it, so the extension sees the summary line and
// nothing more, however carefully it looks. The platform's own API has the whole
// thing and answers a public model without credentials.
//
// Only gaps are filled, never overwrites - except the description, where the
// longer text wins. That one exception is the whole reason this exists: what the
// page offered was not empty, it was a first sentence.
func enrichFromPlatform(request *BrowserImportRequest) {
	public, ok := PublicMetadata(request.Platform, request.SourceURL)
	if !ok {
		return
	}
	mergePublicMetadata(request, public)
}

// mergePublicMetadata is the rule itself, kept apart from the fetching so it can
// be tested without a network.
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
	// Appended rather than replacing: the browser sees pictures the API does not
	// list, and the other way round. SaveDownload deduplicates by content.
	request.ImageURLs = append(request.ImageURLs, public.ImageURLs...)
}

// unpackIfArchive turns a container into the files it holds and leaves a model
// alone.
//
// Decided by what the file contains, not by what it is called. The name was the
// first rule and it broke as soon as one was missing: Chrome does not know a
// download's filename when it reports it, the fallback said ".3mf", and a
// Printables archive was filed as a single model instead of the eight files
// inside it.
//
// A 3MF is a ZIP as well, so the magic bytes alone cannot separate them - but a
// 3MF always carries its model under 3D/, and no ordinary archive of parts does.
//
// Anything that cannot be read as an archive, or that holds nothing, is kept as
// it arrived. Storing a file the library may not understand is a smaller harm
// than dropping one somebody asked for.
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

// isModelPackage reports whether a ZIP is a 3MF rather than a bag of files.
//
// The marker is 3D/…​.model, which the 3MF specification requires and which an
// archive of STLs never has. Read from the central directory, so nothing is
// extracted to find out.
func isModelPackage(reader *zip.Reader) bool {
	for _, entry := range reader.File {
		name := strings.ToLower(strings.ReplaceAll(entry.Name, "\\", "/"))
		if strings.HasPrefix(name, "3d/") && strings.HasSuffix(name, ".model") {
			return true
		}
	}
	return false
}

// preferPrintable narrows a file list to what somebody wants in a model library.
//
// A platform lists everything the designer uploaded: the models, and beside them
// a brochure, a wiring diagram, a photo. Thingiverse's "download all files" hands
// over the lot, and importing it that way put a PDF in the library next to the
// parts.
//
// The same rule the Thingiverse downloader already follows, so both ways of
// importing the same design produce the same thing - and with the same escape:
// when nothing looks printable the whole list is kept, because a design whose
// files are named in a way this does not recognise should still arrive.
// preferPrintableFiles is preferPrintable for files already on disk, applied
// after an archive has been opened.
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
	// The ones left behind are in the temp directory and go with it; nothing that
	// reached the library is touched.
	return printable
}

func preferPrintable(entries []BrowserImportFile) []BrowserImportFile {
	var printable []BrowserImportFile
	for _, entry := range entries {
		name := entry.Name
		if name == "" {
			// No name from the client - judge by the address instead, which is
			// where the filename usually is anyway.
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
