package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/storage"

	"meshdepot/internal/coerce"
)

// uploadImages posts several files in the "image" field at once, which is what
// the gallery form does.
func (testHarness *harness) uploadImages(designID int, files ...[]byte) response {
	testHarness.t.Helper()
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	for index, content := range files {
		part, failure := writer.CreateFormFile("image", fmt.Sprintf("bild%d.png", index))
		if failure != nil {
			testHarness.t.Fatalf("create form file: %v", failure)
		}
		if _, failure := part.Write(content); failure != nil {
			testHarness.t.Fatalf("write form file: %v", failure)
		}
	}
	if failure := writer.Close(); failure != nil {
		testHarness.t.Fatalf("close multipart writer: %v", failure)
	}
	return testHarness.do(request{
		method:  http.MethodPost,
		path:    fmt.Sprintf("/api/v1/designs/%s/images", testHarness.designPID(designID)),
		rawBody: buffer,
		headers: map[string]string{"Content-Type": writer.FormDataContentType()},
		token:   testHarness.userToken,
	})
}

// gifBytes carries the GIF signature, which http.DetectContentType recognises
// just like the PNG one.
var gifBytes = append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 600)...)

func TestImagesUploadStoresTheFiles(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")

	answer := testHarness.uploadImages(designID, pngBytes, gifBytes)

	if answer.status != http.StatusOK {
		t.Fatalf("the upload answered %d: %s", answer.status, answer.rawBody)
	}
	if uploaded := coerce.Int(answer.data(t)["uploaded"]); uploaded != 2 {
		t.Fatalf("%d images were counted", uploaded)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_images WHERE design_id = ?", designID); count != 2 {
		t.Fatalf("%d image rows exist", count)
	}
	// The files land in the design's pictures directory.
	directory := testHarness.userLayout(testHarness.userID).Pictures(designID)
	entries, failure := os.ReadDir(directory)
	if failure != nil {
		t.Fatalf("read the cover directory: %v", failure)
	}
	if len(entries) != 2 {
		t.Fatalf("%d files were written", len(entries))
	}
}

// The sort order continues where the existing images left off, so a later
// upload does not push itself to the front of the gallery.
func TestImagesUploadContinuesTheSortOrder(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")
	testHarness.insertDesignImage(designID, "1/vorhanden.png", 5)

	testHarness.uploadImages(designID, pngBytes)

	highest := testHarness.scalarInt("SELECT MAX(sort_order) FROM design_images WHERE design_id = ?", designID)
	if highest != 6 {
		t.Fatalf("the new image got the sort order %d", highest)
	}
}

// A design without a cover gets one from the first uploaded image, otherwise
// the card overview keeps showing a placeholder.
func TestImagesUploadSetsTheFirstImageAsCover(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Ohne Titelbild")

	answer := testHarness.uploadImages(designID, pngBytes, gifBytes)

	coverPath := coerce.StringOr(answer.data(t)["cover_path"], "")
	if coverPath == "" {
		t.Fatal("no cover was set")
	}
	if stored := testHarness.scalar("SELECT cover_path FROM designs WHERE id = ?", designID); stored != coverPath {
		t.Fatalf("the design carries the cover %q", stored)
	}
	flagged := testHarness.count("SELECT COUNT(*) FROM design_images WHERE design_id = ? AND is_cover = 1", designID)
	if flagged != 1 {
		t.Fatalf("%d images are flagged as the cover", flagged)
	}
}

func TestImagesUploadKeepsAnExistingCover(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Titelbild")
	testHarness.setDesignFields(designID, map[string]any{"cover_path": "1/altes-titelbild.png"})

	answer := testHarness.uploadImages(designID, pngBytes)

	if path := coerce.StringOr(answer.data(t)["cover_path"], ""); path != "1/altes-titelbild.png" {
		t.Fatalf("the cover became %q", path)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_images WHERE design_id = ? AND is_cover = 1", designID); count != 0 {
		t.Fatal("the new image was flagged as the cover")
	}
}

// A file whose bytes are not an image is skipped; the rest of the upload still
// goes through.
func TestImagesUploadSkipsFilesThatAreNotImages(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")

	answer := testHarness.uploadImages(designID, []byte("<html>kein bild</html>"), pngBytes)

	if uploaded := coerce.Int(answer.data(t)["uploaded"]); uploaded != 1 {
		t.Fatalf("%d images were counted", uploaded)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_images WHERE design_id = ?", designID); count != 1 {
		t.Fatalf("%d image rows exist", count)
	}
}

func TestImagesUploadFailsWhenNothingIsAnImage(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")

	answer := testHarness.uploadImages(designID, []byte("<html>kein bild</html>"))

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("the upload answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.upload_failed" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestImagesUploadNeedsAFile(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")

	answer := testHarness.uploadImages(designID)

	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an upload without files answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.file_required" {
		t.Fatalf("unexpected error key %q", key)
	}
}

func TestImagesUploadRejectsAForeignDesign(t *testing.T) {
	testHarness := newHarness(t)
	foreign := testHarness.insertDesign(testHarness.adminID, "Fremd")

	answer := testHarness.uploadImages(foreign, pngBytes)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign design answered %d", answer.status)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_images WHERE design_id = ?", foreign); count != 0 {
		t.Fatal("an image was stored on a foreign design")
	}
}

func TestImagesSetCoverSwitchesTheCover(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")
	firstImage := testHarness.insertDesignImage(designID, "1/erstes.png", 1)
	secondImage := testHarness.insertDesignImage(designID, "1/zweites.png", 2)
	testHarness.setDesignFields(designID, map[string]any{"cover_path": "1/erstes.png"})
	if _, failure := testHarness.database.Exec("UPDATE design_images SET is_cover = 1 WHERE id = ?", firstImage); failure != nil {
		t.Fatalf("flag the first image: %v", failure)
	}

	answer := testHarness.asUser(http.MethodPut,
		fmt.Sprintf("/api/v1/designs/%s/images/%d/cover", testHarness.designPID(designID), secondImage), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("setting the cover answered %d: %s", answer.status, answer.rawBody)
	}
	if path := coerce.StringOr(answer.data(t)["cover_path"], ""); path != "1/zweites.png" {
		t.Fatalf("the answer carries the cover %q", path)
	}
	if stored := testHarness.scalar("SELECT cover_path FROM designs WHERE id = ?", designID); stored != "1/zweites.png" {
		t.Fatalf("the design carries the cover %q", stored)
	}
	if flagged := testHarness.scalarInt("SELECT id FROM design_images WHERE design_id = ? AND is_cover = 1", designID); flagged != secondImage {
		t.Fatalf("image %d is flagged as the cover", flagged)
	}
}

func TestImagesSetCoverRejectsAnImageOfAnotherDesign(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Eigenes")
	otherDesign := testHarness.insertDesign(testHarness.userID, "Anderes")
	otherImage := testHarness.insertDesignImage(otherDesign, "1/anderes.png", 1)

	answer := testHarness.asUser(http.MethodPut,
		fmt.Sprintf("/api/v1/designs/%s/images/%d/cover", testHarness.designPID(designID), otherImage), nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a foreign image answered %d", answer.status)
	}
	if stored := testHarness.scalar("SELECT cover_path FROM designs WHERE id = ?", designID); stored != "" {
		t.Fatalf("the design got the cover %q", stored)
	}
}

func TestImagesDeleteRemovesRowAndFile(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")
	testHarness.uploadImages(designID, pngBytes)
	imageID := testHarness.scalarInt("SELECT id FROM design_images WHERE design_id = ?", designID)
	relativePath := testHarness.scalar("SELECT path FROM design_images WHERE id = ?", imageID)
	fullPath := storage.New(testHarness.dataRoot).Abs(relativePath)

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/images/%d", testHarness.designPID(designID), imageID), nil)

	if answer.status != http.StatusOK {
		t.Fatalf("the deletion answered %d: %s", answer.status, answer.rawBody)
	}
	if count := testHarness.count("SELECT COUNT(*) FROM design_images WHERE id = ?", imageID); count != 0 {
		t.Fatal("the row survived")
	}
	if _, failure := os.Stat(fullPath); failure == nil {
		t.Fatal("the file is still on disk")
	}
}

// Deleting the cover has to hand the role to the next image - otherwise the
// design points at a file that no longer exists.
func TestImagesDeletePassesTheCoverOn(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")
	firstImage := testHarness.insertDesignImage(designID, "1/erstes.png", 1)
	secondImage := testHarness.insertDesignImage(designID, "1/zweites.png", 2)
	testHarness.setDesignFields(designID, map[string]any{"cover_path": "1/erstes.png"})

	testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/images/%d", testHarness.designPID(designID), firstImage), nil)

	if stored := testHarness.scalar("SELECT cover_path FROM designs WHERE id = ?", designID); stored != "1/zweites.png" {
		t.Fatalf("the design carries the cover %q", stored)
	}
	if flagged := testHarness.scalarInt("SELECT id FROM design_images WHERE design_id = ? AND is_cover = 1", designID); flagged != secondImage {
		t.Fatalf("image %d is flagged as the cover", flagged)
	}
}

// The last image leaves the design without a cover rather than with a dangling
// path.
func TestImagesDeleteClearsTheCoverWithTheLastImage(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")
	onlyImage := testHarness.insertDesignImage(designID, "1/einziges.png", 1)
	testHarness.setDesignFields(designID, map[string]any{"cover_path": "1/einziges.png"})

	testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/images/%d", testHarness.designPID(designID), onlyImage), nil)

	if count := testHarness.count("SELECT COUNT(*) FROM designs WHERE id = ? AND cover_path IS NULL", designID); count != 1 {
		t.Fatalf("the cover path is %q", testHarness.scalar("SELECT cover_path FROM designs WHERE id = ?", designID))
	}
}

// Deleting an image that is not the cover leaves the cover alone.
func TestImagesDeleteKeepsAnUnrelatedCover(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")
	testHarness.insertDesignImage(designID, "1/titel.png", 1)
	otherImage := testHarness.insertDesignImage(designID, "1/weiteres.png", 2)
	testHarness.setDesignFields(designID, map[string]any{"cover_path": "1/titel.png"})

	testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/images/%d", testHarness.designPID(designID), otherImage), nil)

	if stored := testHarness.scalar("SELECT cover_path FROM designs WHERE id = ?", designID); stored != "1/titel.png" {
		t.Fatalf("the cover became %q", stored)
	}
}

func TestImageRoutesWithNonNumericIDsAreNotFound(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")

	calls := []struct {
		method string
		path   string
	}{
		{http.MethodPut, fmt.Sprintf("/api/v1/designs/%s/images/abc/cover", testHarness.designPID(designID))},
		{http.MethodPut, "/api/v1/designs/abc/images/1/cover"},
		{http.MethodDelete, fmt.Sprintf("/api/v1/designs/%s/images/abc", testHarness.designPID(designID))},
		{http.MethodDelete, "/api/v1/designs/abc/images/1"},
	}

	for _, call := range calls {
		answer := testHarness.asUser(call.method, call.path, nil)
		if answer.status != http.StatusNotFound {
			t.Fatalf("%s %s answered %d", call.method, call.path, answer.status)
		}
	}
}

func TestCoversServeStreamsAnImage(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Galerie")
	testHarness.uploadImages(designID, pngBytes)
	relativePath := testHarness.scalar("SELECT path FROM design_images WHERE design_id = ?", designID)

	answer := testHarness.asUser(http.MethodGet, "/api/v1/covers/"+relativePath, nil)

	if answer.status != http.StatusOK {
		t.Fatalf("serving answered %d: %s", answer.status, answer.rawBody)
	}
	headers := answer.recorder.Header()
	if headers.Get("Content-Type") != "image/png" {
		t.Fatalf("the content type is %q", headers.Get("Content-Type"))
	}
	if headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("the nosniff header is missing")
	}
}

// The path segment comes straight from the URL, so traversal out of the cover
// directory has to be impossible.
func TestCoversServeRefusesPathsOutsideTheCoverDirectory(t *testing.T) {
	testHarness := newHarness(t)
	secretPath := filepath.Join(testHarness.dataRoot, "geheim.png")
	if failure := os.WriteFile(secretPath, pngBytes, 0o644); failure != nil {
		t.Fatalf("write the file: %v", failure)
	}

	// An encoded slash survives the router and reaches the handler, whose own
	// check has to catch it.
	answer := testHarness.anonymous(http.MethodGet, "/api/v1/covers/..%2Fgeheim.png", nil)
	if answer.status != http.StatusNotFound {
		t.Fatalf("the encoded traversal answered %d", answer.status)
	}

	// A literal ".." never gets that far: the router normalises the path and
	// redirects, and the target is no longer a cover route.
	for _, path := range []string{"/api/v1/covers/../geheim.png", "/api/v1/covers/1/../../geheim.png"} {
		answer := testHarness.anonymous(http.MethodGet, path, nil)
		if answer.status != http.StatusMovedPermanently {
			t.Fatalf("%s answered %d", path, answer.status)
		}
		if location := answer.recorder.Header().Get("Location"); strings.HasPrefix(location, "/api/v1/covers/") {
			t.Fatalf("%s was redirected to %q", path, location)
		}
	}
}

func TestCoversServeIsNotFoundForAMissingFile(t *testing.T) {
	testHarness := newHarness(t)

	answer := testHarness.anonymous(http.MethodGet, "/api/v1/covers/1/gibtsnicht.png", nil)

	if answer.status != http.StatusNotFound {
		t.Fatalf("a missing file answered %d", answer.status)
	}
}

// The image root is the whole data directory, so the shape of the path is what
// stands between a request and the database, a blob or an avatar - even for a
// caller who may see the design's own pictures.
func TestCoversServeAcceptsOnlyTheDocumentedShape(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Bild")
	testHarness.uploadImages(designID, pngBytes)
	user := testHarness.userLayout(testHarness.userID)
	hash := testHarness.publicID(testHarness.userID)

	galleryPath := testHarness.scalar("SELECT path FROM design_images WHERE design_id = ?", designID)
	coverFile := user.Cover(designID, "png")
	if failure := storage.WriteFile(coverFile, pngBytes); failure != nil {
		t.Fatalf("write the cover: %v", failure)
	}
	// Files that exist but must never be served through this endpoint.
	blobFile := user.Blob("aabbccddeeff0011")
	if failure := storage.WriteFile(blobFile, pngBytes); failure != nil {
		t.Fatalf("write the blob: %v", failure)
	}
	avatarFile := filepath.Join(user.Avatar(), "avatar.png")
	if failure := storage.WriteFile(avatarFile, pngBytes); failure != nil {
		t.Fatalf("write the avatar: %v", failure)
	}

	cases := []struct {
		label  string
		path   string
		status int
	}{
		{"gallery image", galleryPath, http.StatusOK},
		{"cover", storage.New(testHarness.dataRoot).Rel(coverFile), http.StatusOK},
		{"blob", storage.New(testHarness.dataRoot).Rel(blobFile), http.StatusNotFound},
		{"avatar", storage.New(testHarness.dataRoot).Rel(avatarFile), http.StatusNotFound},
		{"database", "meshdepot.db", http.StatusNotFound},
		{"numeric account segment", fmt.Sprintf("1/stl/%d/cover.png", designID), http.StatusNotFound},
		{"uppercase account segment", strings.ToUpper(hash) + "/design/1/cover.png", http.StatusNotFound},
		{"account root", "user/" + hash, http.StatusNotFound},
		{"temp directory", "user/" + hash + "/tmp/x.png", http.StatusNotFound},
	}
	for _, testCase := range cases {
		answer := testHarness.asUser(http.MethodGet, "/api/v1/covers/"+testCase.path, nil)
		if answer.status != testCase.status {
			t.Errorf("%s (%q) answered %d, want %d", testCase.label, testCase.path, answer.status, testCase.status)
		}
	}
}

// The pictures of a design are only for people who may see the design. This is
// the case the endpoint used to get wrong: the cover lives under the fixed name
// cover.<ext> below the sequential design id, so anyone who knew an account's
// public id - which every share payload and the user search hand out - could
// count their way through a stranger's library.
func TestCoversServeHidesTheDesignsOfAnotherUser(t *testing.T) {
	testHarness := newHarness(t)
	strangerID := testHarness.createUser("fremd@example.com", "Fremd", false)
	designID := testHarness.insertDesign(strangerID, "Nicht geteilt")
	user := testHarness.userLayout(strangerID)
	coverFile := user.Cover(designID, "png")
	if failure := storage.WriteFile(coverFile, pngBytes); failure != nil {
		t.Fatalf("write the cover: %v", failure)
	}
	coverPath := storage.New(testHarness.dataRoot).Rel(coverFile)

	// A logged-in member of the same instance.
	if answer := testHarness.asUser(http.MethodGet, "/api/v1/covers/"+coverPath, nil); answer.status != http.StatusNotFound {
		t.Fatalf("a member read a foreign cover: %d", answer.status)
	}
	// Nobody at all.
	if answer := testHarness.anonymous(http.MethodGet, "/api/v1/covers/"+coverPath, nil); answer.status != http.StatusNotFound {
		t.Fatalf("an anonymous request read a foreign cover: %d", answer.status)
	}
	// An admin has no business here either: no other design route lets one read
	// a member's library, and this one is not the exception.
	if answer := testHarness.asAdmin(http.MethodGet, "/api/v1/covers/"+coverPath, nil); answer.status != http.StatusNotFound {
		t.Fatalf("the admin read a foreign cover: %d", answer.status)
	}
}

// Sharing a design with someone includes its pictures - otherwise their copy of
// the detail view shows empty frames.
func TestCoversServeFollowsAShareWithAUser(t *testing.T) {
	testHarness := newHarness(t)
	ownerID := testHarness.createUser("besitzer@example.com", "Besitzer", false)
	designID := testHarness.insertDesign(ownerID, "Geteilt")
	user := testHarness.userLayout(ownerID)
	coverFile := user.Cover(designID, "png")
	if failure := storage.WriteFile(coverFile, pngBytes); failure != nil {
		t.Fatalf("write the cover: %v", failure)
	}
	coverPath := storage.New(testHarness.dataRoot).Rel(coverFile)

	if answer := testHarness.asUser(http.MethodGet, "/api/v1/covers/"+coverPath, nil); answer.status != http.StatusNotFound {
		t.Fatalf("before the share the answer was %d", answer.status)
	}
	testHarness.shareDesign(designID, ownerID, testHarness.userID)
	if answer := testHarness.asUser(http.MethodGet, "/api/v1/covers/"+coverPath, nil); answer.status != http.StatusOK {
		t.Fatalf("after the share the answer was %d", answer.status)
	}
}

// The public share page has no session by definition, so its token is what
// carries the pictures - and it carries them for its own design only.
func TestCoversServeAcceptsAShareLinkForItsOwnDesign(t *testing.T) {
	testHarness := newHarness(t)
	sharedDesignID := testHarness.insertDesign(testHarness.userID, "Am Link")
	otherDesignID := testHarness.insertDesign(testHarness.userID, "Nicht am Link")
	user := testHarness.userLayout(testHarness.userID)
	root := storage.New(testHarness.dataRoot)

	paths := map[int]string{}
	for _, designID := range []int{sharedDesignID, otherDesignID} {
		coverFile := user.Cover(designID, "png")
		if failure := storage.WriteFile(coverFile, pngBytes); failure != nil {
			t.Fatalf("write the cover: %v", failure)
		}
		paths[designID] = root.Rel(coverFile)
	}

	created := testHarness.asUser(http.MethodPost,
		fmt.Sprintf("/api/v1/designs/%s/links", testHarness.designPID(sharedDesignID)),
		map[string]any{"expires_in_hours": 24})
	if created.status != http.StatusOK && created.status != http.StatusCreated {
		t.Fatalf("creating the link answered %d: %s", created.status, created.rawBody)
	}
	token, _ := created.data(t)["token"].(string)
	if token == "" {
		t.Fatalf("the link carries no token: %s", created.rawBody)
	}

	answer := testHarness.anonymous(http.MethodGet, "/api/v1/covers/"+paths[sharedDesignID]+"?share="+token, nil)
	if answer.status != http.StatusOK {
		t.Fatalf("the link did not serve its own cover: %d", answer.status)
	}
	// The same token must not become a key to the rest of the library.
	answer = testHarness.anonymous(http.MethodGet, "/api/v1/covers/"+paths[otherDesignID]+"?share="+token, nil)
	if answer.status != http.StatusNotFound {
		t.Fatalf("the link served another design's cover: %d", answer.status)
	}
	answer = testHarness.anonymous(http.MethodGet, "/api/v1/covers/"+paths[sharedDesignID]+"?share="+strings.Repeat("a", 32), nil)
	if answer.status != http.StatusNotFound {
		t.Fatalf("an invented token answered %d", answer.status)
	}
}

// The account segment has to name the design's real owner. Without that check it
// would be decoration: any valid public id plus the right design id would read
// the file, because the path on disk is built from the design alone.
func TestCoversServeRejectsAForeignAccountSegment(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Eigenes")
	user := testHarness.userLayout(testHarness.userID)
	coverFile := user.Cover(designID, "png")
	if failure := storage.WriteFile(coverFile, pngBytes); failure != nil {
		t.Fatalf("write the cover: %v", failure)
	}

	swapped := fmt.Sprintf("user/%s/design/%d/cover.png", testHarness.publicID(testHarness.adminID), designID)
	if answer := testHarness.asUser(http.MethodGet, "/api/v1/covers/"+swapped, nil); answer.status != http.StatusNotFound {
		t.Fatalf("the swapped account segment answered %d", answer.status)
	}
}

// A cached answer must not outlive the session it was authorized for.
func TestCoversServeMarksTheAnswerPrivate(t *testing.T) {
	testHarness := newHarness(t)
	designID := testHarness.insertDesign(testHarness.userID, "Mit Bild")
	testHarness.uploadImages(designID, pngBytes)
	relativePath := testHarness.scalar("SELECT path FROM design_images WHERE design_id = ?", designID)

	answer := testHarness.asUser(http.MethodGet, "/api/v1/covers/"+relativePath, nil)

	if cacheControl := answer.recorder.Header().Get("Cache-Control"); !strings.HasPrefix(cacheControl, "private") {
		t.Fatalf("the cache header is %q", cacheControl)
	}
}
