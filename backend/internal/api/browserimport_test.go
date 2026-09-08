package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"meshdepot/internal/platforms"
)

// withKey issues an API key for the ordinary test user and returns it.
func (testHarness *harness) withKey() string {
	testHarness.t.Helper()
	answer := testHarness.asUser(http.MethodPost, "/api/v1/api-keys", map[string]any{"name": "test"})
	if answer.status != http.StatusCreated {
		testHarness.t.Fatalf("creating a key answered %d: %s", answer.status, answer.rawBody)
	}
	key, _ := answer.data(testHarness.t)["key"].(string)
	if key == "" {
		testHarness.t.Fatal("the response carried no key")
	}
	return key
}

// withApiKey performs a request authenticated by API key instead of a session.
func (testHarness *harness) withApiKey(method, path, key string, body any) response {
	testHarness.t.Helper()
	return testHarness.do(request{
		method: method, path: path, body: body, noCookie: true,
		headers: map[string]string{"Authorization": "Bearer " + key},
	})
}

// The key is the whole access control of this route, so an absent, malformed or
// revoked one must not get past it.
func TestBrowserImportRefusesWithoutAValidKey(t *testing.T) {
	testHarness := newHarness(t)
	payload := map[string]any{
		"source_url": "https://makerworld.com/en/models/1",
		"files":      []map[string]any{{"name": "a.3mf", "url": "https://example.invalid/a.3mf"}},
	}

	if answer := testHarness.anonymous(http.MethodPost, "/api/v1/imports/browser", payload); answer.status != http.StatusUnauthorized {
		t.Fatalf("without any key: %d", answer.status)
	}
	if answer := testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", "mdp_not-a-real-key", payload); answer.status != http.StatusUnauthorized {
		t.Fatalf("with an invented key: %d", answer.status)
	}

	// A session cookie must not work either. The route downloads from URLs in the
	// body; reachable with an ambient cookie, any page a signed-in member opens
	// could set it off.
	if answer := testHarness.asUser(http.MethodPost, "/api/v1/imports/browser", payload); answer.status != http.StatusUnauthorized {
		t.Fatalf("with a session cookie: %d - this route must not accept one", answer.status)
	}
}

func TestBrowserImportRefusesARevokedKey(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()
	identifier := testHarness.scalarInt("SELECT id FROM api_keys ORDER BY id DESC LIMIT 1")

	answer := testHarness.asUser(http.MethodDelete, fmt.Sprintf("/api/v1/api-keys/%d", identifier), nil)
	if answer.status != http.StatusOK {
		t.Fatalf("revoking answered %d: %s", answer.status, answer.rawBody)
	}

	after := testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", key, map[string]any{
		"source_url": "https://makerworld.com/en/models/1",
		"files":      []map[string]any{{"url": "https://example.invalid/a.3mf"}},
	})
	if after.status != http.StatusUnauthorized {
		t.Fatalf("a revoked key still worked: %d", after.status)
	}
}

// The platform comes from the URL, never from the body: it decides where the
// design is filed and which sync later touches it.
func TestBrowserImportRefusesAnUnsupportedURL(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()

	answer := testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", key, map[string]any{
		"source_url": "https://example.com/some/thing",
		"platform":   "makerworld",
		"files":      []map[string]any{{"url": "https://example.invalid/a.3mf"}},
	})
	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("an unsupported host answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.unsupported_platform" {
		t.Fatalf("unexpected error key %q", key)
	}
}

// A design already in the library is a conflict, exactly as it is for a queued
// download - re-importing would otherwise create a second copy of it.
func TestBrowserImportReportsADuplicate(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()
	const sourceURL = "https://makerworld.com/en/models/4242"

	designID := testHarness.insertDesign(testHarness.userID, "Already here")
	testHarness.insertRow("UPDATE designs SET source_url = ? WHERE id = ?", sourceURL, designID)

	answer := testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", key, map[string]any{
		"source_url": sourceURL,
		"files":      []map[string]any{{"url": "https://example.invalid/a.3mf"}},
	})
	if answer.status != http.StatusConflict {
		t.Fatalf("a duplicate answered %d: %s", answer.status, answer.rawBody)
	}
}

// The end-to-end path: links are fetched during the request and the design is in
// the library when the answer arrives. No queue, because the links expire in
// about five minutes.
func TestBrowserImportStoresTheDesignImmediately(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()

	// A ZIP-shaped body, so it passes the size and magic-byte checks the library
	// applies to a downloaded file.
	payload := append([]byte("PK\x03\x04"), make([]byte, 200000)...)
	origin := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Header().Set("Content-Disposition", `attachment;filename=Plate 1.3mf`)
		responseWriter.Write(payload)
	}))
	defer origin.Close()

	answer := testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", key, map[string]any{
		"source_url": "https://makerworld.com/en/models/9911",
		"meta": map[string]any{
			"name": "Imported from the browser", "author": "Someone",
			"tags": []string{"one", "two"},
		},
		"files": []map[string]any{{"name": "whatever(1).3mf", "url": origin.URL + "/file.3mf"}},
	})

	if answer.status != http.StatusCreated {
		t.Fatalf("the import answered %d: %s", answer.status, answer.rawBody)
	}
	data := answer.data(t)
	if data["file_count"] != float64(1) {
		t.Fatalf("file_count is %v", data["file_count"])
	}

	stored := testHarness.count("SELECT COUNT(*) FROM designs WHERE user_id = ? AND source_url = ?",
		testHarness.userID, "https://makerworld.com/en/models/9911")
	if stored != 1 {
		t.Fatalf("the design is not in the library (%d rows)", stored)
	}
	// Nothing was queued: the whole point is that this does not wait.
	if queued := testHarness.count("SELECT COUNT(*) FROM download_queue"); queued != 0 {
		t.Fatalf("the import created %d queue entries", queued)
	}
}

// Dead links are common - they expire in minutes - and must not cost the files
// that did arrive.
func TestBrowserImportKeepsWhatItCouldFetch(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()

	payload := append([]byte("PK\x03\x04"), make([]byte, 200000)...)
	origin := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/expired.3mf" {
			responseWriter.WriteHeader(http.StatusForbidden)
			return
		}
		responseWriter.Write(payload)
	}))
	defer origin.Close()

	answer := testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", key, map[string]any{
		"source_url": "https://makerworld.com/en/models/7777",
		"meta":       map[string]any{"name": "Half a model"},
		"files": []map[string]any{
			{"name": "good.3mf", "url": origin.URL + "/good.3mf"},
			{"name": "gone.3mf", "url": origin.URL + "/expired.3mf"},
		},
	})

	if answer.status != http.StatusCreated {
		t.Fatalf("the import answered %d: %s", answer.status, answer.rawBody)
	}
	data := answer.data(t)
	if data["file_count"] != float64(1) || data["skipped_count"] != float64(1) {
		t.Fatalf("expected one kept and one skipped, got %v / %v", data["file_count"], data["skipped_count"])
	}
}

// Printables and Thingiverse hand out a ZIP, MakerWorld a .3mf that happens to be
// one. The first has to be unpacked and the second must not be - both start with
// "PK", so the extension decides the case, not the bytes.
func TestBrowserImportDecidesUnpackingByContentNotByName(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()

	// A real archive holding two models.
	// With a brochure inside it, the way Printables packs one - the models are
	// what belongs in a library, the PDF is not.
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range []string{"part-a.stl", "part-b.stl", "instructions.pdf"} {
		entry, failure := writer.Create(name)
		if failure != nil {
			t.Fatalf("building the archive: %v", failure)
		}
		entry.Write(make([]byte, 120000))
	}
	writer.Close()

	// A real 3MF: a ZIP carrying its model under 3D/, which is what tells it apart
	// from an archive of parts. Both are called ".3mf" here, so nothing in the
	// names says which is which and the decision has to come from the content -
	// which is the situation Chrome creates, where a download arrives with no
	// filename at all.
	var modelPackage bytes.Buffer
	modelWriter := zip.NewWriter(&modelPackage)
	contentTypes, _ := modelWriter.Create("[Content_Types].xml")
	contentTypes.Write([]byte("<Types/>"))
	modelEntry, _ := modelWriter.Create("3D/3dmodel.model")
	modelEntry.Write(make([]byte, 200000))
	modelWriter.Close()
	model := modelPackage.Bytes()

	origin := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/bundle.zip" {
			responseWriter.Write(archive.Bytes())
			return
		}
		responseWriter.Write(model)
	}))
	defer origin.Close()

	answer := testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", key, map[string]any{
		"source_url": "https://www.printables.com/model/1234-thing",
		"meta":       map[string]any{"name": "Mixed"},
		"files": []map[string]any{
			{"name": "bundle.3mf", "url": origin.URL + "/bundle.zip"},
			{"name": "plate.3mf", "url": origin.URL + "/plate.3mf"},
		},
	})

	if answer.status != http.StatusCreated {
		t.Fatalf("the import answered %d: %s", answer.status, answer.rawBody)
	}
	// Two out of the archive plus the untouched 3MF.
	if count := answer.data(t)["file_count"]; count != float64(3) {
		t.Fatalf("expected 3 files (2 models unpacked + 1 kept, the brochure dropped), got %v", count)
	}
	if pdfs := testHarness.count(`
		SELECT COUNT(*) FROM design_file_entries e
		JOIN design_files f ON f.id = e.design_file_id
		WHERE f.design_id IN (SELECT id FROM designs WHERE user_id = ?) AND e.filename LIKE '%.pdf'`,
		testHarness.userID); pdfs != 0 {
		t.Fatalf("a brochure from inside the archive was stored (%d)", pdfs)
	}

	designID := testHarness.scalarInt("SELECT id FROM designs WHERE user_id = ? AND source_url = ?",
		testHarness.userID, "https://www.printables.com/model/1234-thing")

	// The individual files live in design_file_entries; design_files is the
	// version row and carries only the first filename.
	entries := testHarness.count(`
		SELECT COUNT(*) FROM design_file_entries e
		JOIN design_files f ON f.id = e.design_file_id WHERE f.design_id = ?`, designID)
	if entries != 3 {
		t.Fatalf("expected 3 stored files, got %d", entries)
	}
	// The archive was unpacked despite being called ".3mf" ...
	parts := testHarness.count(`
		SELECT COUNT(*) FROM design_file_entries e
		JOIN design_files f ON f.id = e.design_file_id
		WHERE f.design_id = ? AND e.filename IN ('part-a.stl', 'part-b.stl')`, designID)
	if parts != 2 {
		t.Fatalf("the archive was not unpacked (%d of its 2 files stored)", parts)
	}
	// ... and the model beside it, wearing the same extension, survived whole.
	kept := testHarness.count(`
		SELECT COUNT(*) FROM design_file_entries e
		JOIN design_files f ON f.id = e.design_file_id
		WHERE f.design_id = ? AND e.filename = 'plate.3mf'`, designID)
	if kept != 1 {
		t.Fatalf("the model package was unpacked instead of kept (%d)", kept)
	}
}

// The collection is created on the first import that asks for it and reused
// afterwards - two imports must not leave two collections of the same name.
func TestBrowserImportFilesIntoOneCollection(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()

	payload := append([]byte("PK\x03\x04"), make([]byte, 200000)...)
	origin := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Write(payload)
	}))
	defer origin.Close()

	importOne := func(model string) response {
		return testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", key, map[string]any{
			"source_url":        "https://www.printables.com/model/" + model,
			"meta":              map[string]any{"name": "Design " + model},
			"add_to_collection": true,
			"files":             []map[string]any{{"name": "a.3mf", "url": origin.URL + "/a.3mf"}},
		})
	}

	first := importOne("111-one")
	if first.status != http.StatusCreated {
		t.Fatalf("the first import answered %d: %s", first.status, first.rawBody)
	}
	if first.data(t)["collection"] != platforms.BrowserImportCollection {
		t.Fatalf("the answer does not name the collection: %v", first.data(t)["collection"])
	}
	if second := importOne("222-two"); second.status != http.StatusCreated {
		t.Fatalf("the second import answered %d: %s", second.status, second.rawBody)
	}

	collections := testHarness.count("SELECT COUNT(*) FROM collections WHERE user_id = ? AND name = ?",
		testHarness.userID, platforms.BrowserImportCollection)
	if collections != 1 {
		t.Fatalf("expected one collection, found %d", collections)
	}
	filed := testHarness.count(`
		SELECT COUNT(*) FROM design_collections dc
		JOIN collections c ON c.id = dc.collection_id
		WHERE c.user_id = ? AND c.name = ?`, testHarness.userID, platforms.BrowserImportCollection)
	if filed != 2 {
		t.Fatalf("expected both designs in it, found %d", filed)
	}
}

// Without the flag nothing is filed anywhere - the setting is off by default and
// must stay a decision.
func TestBrowserImportFilesNothingWithoutTheFlag(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()

	payload := append([]byte("PK\x03\x04"), make([]byte, 200000)...)
	origin := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Write(payload)
	}))
	defer origin.Close()

	answer := testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", key, map[string]any{
		"source_url": "https://www.printables.com/model/333-three",
		"meta":       map[string]any{"name": "Unfiled"},
		"files":      []map[string]any{{"name": "a.3mf", "url": origin.URL + "/a.3mf"}},
	})
	if answer.status != http.StatusCreated {
		t.Fatalf("the import answered %d: %s", answer.status, answer.rawBody)
	}
	if answer.data(t)["collection"] != "" {
		t.Fatalf("a collection was named anyway: %v", answer.data(t)["collection"])
	}
	if made := testHarness.count("SELECT COUNT(*) FROM collections WHERE user_id = ?", testHarness.userID); made != 0 {
		t.Fatalf("%d collection(s) were created without being asked", made)
	}
}

// The upload route, end to end: a platform that builds its archive in the
// browser hands out a blob: address the server cannot fetch, so the bytes come
// with the request. The archive still has to be unpacked.
func TestBrowserImportUploadStoresCarriedFiles(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range []string{"body.stl", "lid.stl"} {
		entry, failure := writer.Create(name)
		if failure != nil {
			t.Fatalf("building the archive: %v", failure)
		}
		entry.Write(make([]byte, 150000))
	}
	writer.Close()

	var form bytes.Buffer
	multipartWriter := multipart.NewWriter(&form)
	multipartWriter.WriteField("payload", `{"source_url":"https://www.thingiverse.com/thing:987654",
		"meta":{"name":"Carried in the browser","author":"someone"}}`)
	part, _ := multipartWriter.CreateFormFile("file", "all-files.zip")
	part.Write(archive.Bytes())
	multipartWriter.Close()

	answer := testHarness.do(request{
		method: http.MethodPost, path: "/api/v1/imports/browser/upload",
		rawBody: bytes.NewReader(form.Bytes()), noCookie: true,
		headers: map[string]string{
			"Authorization": "Bearer " + key,
			"Content-Type":  multipartWriter.FormDataContentType(),
		},
	})

	if answer.status != http.StatusCreated {
		t.Fatalf("the upload answered %d: %s", answer.status, answer.rawBody)
	}
	if count := answer.data(t)["file_count"]; count != float64(2) {
		t.Fatalf("expected the archive unpacked into 2 files, got %v", count)
	}

	designID := testHarness.scalarInt("SELECT id FROM designs WHERE user_id = ? AND source_url = ?",
		testHarness.userID, "https://www.thingiverse.com/thing:987654")
	if designID == 0 {
		t.Fatal("the design is not in the library")
	}
	if archived := testHarness.count(`
		SELECT COUNT(*) FROM design_file_entries e
		JOIN design_files f ON f.id = e.design_file_id
		WHERE f.design_id = ? AND e.filename LIKE '%.zip'`, designID); archived != 0 {
		t.Fatalf("the archive itself was stored (%d)", archived)
	}
	if queued := testHarness.count("SELECT COUNT(*) FROM download_queue"); queued != 0 {
		t.Fatalf("the upload created %d queue entries", queued)
	}
}

// The key guards this route as it guards the other one.
func TestBrowserImportUploadRefusesWithoutAKey(t *testing.T) {
	testHarness := newHarness(t)

	var form bytes.Buffer
	multipartWriter := multipart.NewWriter(&form)
	multipartWriter.WriteField("payload", `{"source_url":"https://www.thingiverse.com/thing:1"}`)
	part, _ := multipartWriter.CreateFormFile("file", "a.stl")
	part.Write(make([]byte, 150000))
	multipartWriter.Close()

	answer := testHarness.do(request{
		method: http.MethodPost, path: "/api/v1/imports/browser/upload",
		rawBody: bytes.NewReader(form.Bytes()),
		headers: map[string]string{"Content-Type": multipartWriter.FormDataContentType()},
	})
	if answer.status != http.StatusUnauthorized {
		t.Fatalf("without a key: %d", answer.status)
	}
}

// A key travels in a header, so a plaintext connection from outside hands it to
// everyone on the way. Refused there, and allowed from the same machine or the
// same network, where a self-hosted instance on plain HTTP is the ordinary
// arrangement.
func TestBrowserImportRefusesAPlaintextConnectionFromOutside(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()
	payload := map[string]any{
		"source_url": "https://www.printables.com/model/1-x",
		"files":      []map[string]any{{"url": "https://example.invalid/a.3mf"}},
	}

	outside := testHarness.do(request{
		method: http.MethodPost, path: "/api/v1/imports/browser", body: payload, noCookie: true,
		headers:    map[string]string{"Authorization": "Bearer " + key},
		remoteAddr: "203.0.113.7:44321",
	})
	if outside.status != http.StatusForbidden {
		t.Fatalf("plaintext from a public address answered %d, expected 403", outside.status)
	}
	if key := outside.errorKey(t); key != "error.insecure_connection" {
		t.Fatalf("unexpected error key %q", key)
	}

	// The same request from the server's own network gets past the check and
	// fails later, on the unreachable file - which is the point: it got that far.
	inside := testHarness.do(request{
		method: http.MethodPost, path: "/api/v1/imports/browser", body: payload, noCookie: true,
		headers:    map[string]string{"Authorization": "Bearer " + key},
		remoteAddr: "192.168.1.20:44321",
	})
	if inside.status == http.StatusForbidden {
		t.Fatalf("a caller on the server's own network was refused: %s", inside.rawBody)
	}
}

// An expired key is no key. Checked in the lookup itself, so there is no state
// where one is known but refused.
func TestAPIKeyStopsWorkingWhenItExpires(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()

	testHarness.insertRow("UPDATE api_keys SET expires_at = datetime('now', '-1 day')")
	answer := testHarness.withApiKey(http.MethodGet, "/api/v1/imports/browser", key, nil)
	if answer.status != http.StatusUnauthorized {
		t.Fatalf("an expired key answered %d", answer.status)
	}

	testHarness.insertRow("UPDATE api_keys SET expires_at = datetime('now', '+1 day')")
	if again := testHarness.withApiKey(http.MethodGet, "/api/v1/imports/browser", key, nil); again.status != http.StatusOK {
		t.Fatalf("a key still in date answered %d: %s", again.status, again.rawBody)
	}
}

// The queue refuses a download that cannot be kept, and this route has to do the
// same - otherwise the quota is a limit on one way in and not on the library.
func TestBrowserImportRefusesWhenTheQuotaIsFull(t *testing.T) {
	testHarness := newHarness(t)
	key := testHarness.withKey()

	// A limit of one byte, already exceeded by anything at all.
	testHarness.insertRow("UPDATE users SET storage_quota_bytes = 1 WHERE id = ?", testHarness.userID)
	designID := testHarness.insertDesign(testHarness.userID, "Something already here")
	testHarness.insertRow(`INSERT INTO design_files (design_id, version, filename, path, size_bytes, file_count, is_current)
		VALUES (?, '1.0', 'a.stl', 'x', 5000, 1, 1)`, designID)

	answer := testHarness.withApiKey(http.MethodPost, "/api/v1/imports/browser", key, map[string]any{
		"source_url": "https://www.printables.com/model/555-full",
		"files":      []map[string]any{{"name": "a.stl", "url": "https://example.invalid/a.stl"}},
	})
	if answer.status != http.StatusUnprocessableEntity {
		t.Fatalf("a full quota answered %d", answer.status)
	}
	if key := answer.errorKey(t); key != "error.storage_quota_exceeded" {
		t.Fatalf("unexpected error key %q", key)
	}
}
