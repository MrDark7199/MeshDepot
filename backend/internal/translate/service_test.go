package translate

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"meshdepot/internal/db"
)

// newTestDatabase opens an initialised database; the schema already enables
// translation.
func newTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, failure := db.Open(filepath.Join(t.TempDir(), "translate.db"))
	if failure != nil {
		t.Fatalf("open database: %v", failure)
	}
	t.Cleanup(func() { database.Close() })
	if failure := db.InitSchema(database); failure != nil {
		t.Fatalf("init schema: %v", failure)
	}
	return database
}

// newTestService points the translation service at a local server instead of
// Google and drops the politeness pause between requests.
func newTestService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	service := New(newTestDatabase(t))
	service.endpoint = server.URL + "/translate?tl="
	service.requestPause = 0
	return service
}

// echoHandler answers like the real endpoint: one array element per q, here the
// input prefixed with the requested target language.
func echoHandler(t *testing.T, requests *[]url.Values) http.HandlerFunc {
	t.Helper()
	return func(writer http.ResponseWriter, request *http.Request) {
		if failure := request.ParseForm(); failure != nil {
			t.Errorf("parse form: %v", failure)
		}
		if requests != nil {
			*requests = append(*requests, request.Form)
		}
		target := request.URL.Query().Get("tl")

		translated := make([]string, 0, len(request.Form["q"]))
		for _, text := range request.Form["q"] {
			translated = append(translated, target+":"+text)
		}
		writer.Header().Set("Content-Type", "application/json")
		if failure := json.NewEncoder(writer).Encode(translated); failure != nil {
			t.Errorf("encode response: %v", failure)
		}
	}
}

func TestEnabledFollowsTheSetting(t *testing.T) {
	database := newTestDatabase(t)
	service := New(database)

	if !service.Enabled() {
		t.Fatal("the schema default did not enable translation")
	}
	for _, value := range []string{"0", "", "true", "yes"} {
		database.Exec("UPDATE app_settings SET value=? WHERE key='translation_enabled'", value)
		if service.Enabled() {
			t.Fatalf("the value %q enabled translation", value)
		}
	}
}

func TestEnabledWithoutTheSetting(t *testing.T) {
	database := newTestDatabase(t)
	database.Exec("DELETE FROM app_settings WHERE key='translation_enabled'")

	if New(database).Enabled() {
		t.Fatal("a missing setting enabled translation")
	}
}

func TestRequestBatchSendsEveryTextAndTheTargetLanguage(t *testing.T) {
	var requests []url.Values
	service := newTestService(t, echoHandler(t, &requests))

	translated, ok := service.requestBatch([]string{"first", "second"}, "de")
	if !ok {
		t.Fatal("the request failed")
	}
	if len(translated) != 2 || translated[0] != "de:first" || translated[1] != "de:second" {
		t.Fatalf("unexpected result %v", translated)
	}
	if len(requests) != 1 {
		t.Fatalf("expected one request, got %d", len(requests))
	}
	if got := requests[0]["q"]; len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("the texts arrived as %v", got)
	}
}

func TestRequestBatchWithoutTextsDoesNotCallTheEndpoint(t *testing.T) {
	service := newTestService(t, func(http.ResponseWriter, *http.Request) {
		t.Error("the endpoint was called for an empty batch")
	})

	translated, ok := service.requestBatch(nil, "de")
	if !ok || len(translated) != 0 {
		t.Fatalf("unexpected result %v/%v", translated, ok)
	}
}

// Google returns either a plain string or [translation, detectedLanguage] per
// element; both shapes have to be understood.
func TestRequestBatchUnderstandsBothElementShapes(t *testing.T) {
	service := newTestService(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Write([]byte(`["plain",["nested","fr"]]`))
	})

	translated, ok := service.requestBatch([]string{"a", "b"}, "en")
	if !ok {
		t.Fatal("the request failed")
	}
	if translated[0] != "plain" || translated[1] != "nested" {
		t.Fatalf("unexpected result %v", translated)
	}
}

func TestRequestBatchRejectsBadResponses(t *testing.T) {
	testCases := map[string]http.HandlerFunc{
		"server error": func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusTooManyRequests)
		},
		"no json": func(writer http.ResponseWriter, _ *http.Request) {
			writer.Write([]byte("<html>Just a moment</html>"))
		},
		"wrong element count": func(writer http.ResponseWriter, _ *http.Request) {
			writer.Write([]byte(`["only one"]`))
		},
		"unusable element": func(writer http.ResponseWriter, _ *http.Request) {
			writer.Write([]byte(`["fine",{"object":"instead of a string"}]`))
		},
		"empty inner array": func(writer http.ResponseWriter, _ *http.Request) {
			writer.Write([]byte(`["fine",[]]`))
		},
		"number instead of a string": func(writer http.ResponseWriter, _ *http.Request) {
			writer.Write([]byte(`["fine",[42,"en"]]`))
		},
	}

	for name, handler := range testCases {
		service := newTestService(t, handler)
		if _, ok := service.requestBatch([]string{"a", "b"}, "en"); ok {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestRequestBatchReportsAnUnreachableEndpoint(t *testing.T) {
	service := New(newTestDatabase(t))
	service.requestPause = 0
	// A closed listener: the address is valid, nothing answers on it.
	server := httptest.NewServer(http.NotFoundHandler())
	service.endpoint = server.URL + "/translate?tl="
	server.Close()

	if _, ok := service.requestBatch([]string{"a"}, "en"); ok {
		t.Fatal("an unreachable endpoint reported success")
	}
}

func TestRequestBatchReportsAnUnusableEndpoint(t *testing.T) {
	service := New(newTestDatabase(t))
	service.requestPause = 0
	service.endpoint = "http://example.org/\x7f?tl="

	if _, ok := service.requestBatch([]string{"a"}, "en"); ok {
		t.Fatal("an unbuildable request reported success")
	}
}

// A description longer than batchCharLimit is split, translated in several
// requests and reassembled in order.
func TestTranslateTextListReassemblesLongText(t *testing.T) {
	var requests []url.Values
	service := newTestService(t, echoHandler(t, &requests))
	long := strings.Repeat("a", 3000) + "\n" + strings.Repeat("b", 3000)

	translated, ok := service.translateTextList([]string{long}, "de")
	if !ok {
		t.Fatal("the translation failed")
	}
	if len(requests) < 2 {
		t.Fatalf("the long text was sent in %d request(s)", len(requests))
	}
	rebuilt := strings.ReplaceAll(translated[0], "de:", "")
	if rebuilt != long {
		t.Fatal("the chunks were not reassembled into the original text")
	}
}

// Several short texts must share one request instead of costing one round trip
// each.
func TestTranslateTextListBatchesShortTexts(t *testing.T) {
	var requests []url.Values
	service := newTestService(t, echoHandler(t, &requests))

	translated, ok := service.translateTextList([]string{"one", "two", "three"}, "de")
	if !ok {
		t.Fatal("the translation failed")
	}
	if len(requests) != 1 {
		t.Fatalf("expected one request, got %d", len(requests))
	}
	if translated[2] != "de:three" {
		t.Fatalf("unexpected result %v", translated)
	}
}

func TestTranslateTextListReportsAFailedBatch(t *testing.T) {
	service := newTestService(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	})

	if _, ok := service.translateTextList([]string{"text"}, "de"); ok {
		t.Fatal("a failed batch reported success")
	}
}

func TestTranslateWithoutTextsDoesNothing(t *testing.T) {
	service := newTestService(t, func(http.ResponseWriter, *http.Request) {
		t.Error("the endpoint was called for an empty list")
	})

	translated, ok := service.translate(nil, "de")
	if !ok || len(translated) != 0 {
		t.Fatalf("unexpected result %v/%v", translated, ok)
	}
}

func TestTranslateHandlesPlaintextAndMarkup(t *testing.T) {
	service := newTestService(t, echoHandler(t, nil))

	translated, ok := service.translate([]string{"plain text", "<p>marked up</p>"}, "de")
	if !ok {
		t.Fatal("the translation failed")
	}
	if translated[0] != "de:plain text" {
		t.Fatalf("unexpected plaintext result %q", translated[0])
	}
	if translated[1] != "<p>de:marked up</p>" {
		t.Fatalf("unexpected markup result %q", translated[1])
	}
}

func TestTranslateReportsFailureForBothBranches(t *testing.T) {
	for _, text := range []string{"plain text", "<p>marked up</p>"} {
		service := newTestService(t, func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusServiceUnavailable)
		})
		if _, ok := service.translate([]string{text}, "de"); ok {
			t.Fatalf("a failed request reported success for %q", text)
		}
	}
}

// Only the text nodes are translated - attributes, tags and nesting stay.
func TestTranslateHTMLKeepsTheMarkup(t *testing.T) {
	service := newTestService(t, echoHandler(t, nil))

	translated, ok := service.translateHTML(`<div class="note"><b>bold</b> and plain</div>`, "de")
	if !ok {
		t.Fatal("the translation failed")
	}
	if translated != `<div class="note"><b>de:bold</b>de: and plain</div>` {
		t.Fatalf("unexpected result %q", translated)
	}
}

// Script and style content is code, not prose, and must be left alone.
func TestTranslateHTMLSkipsScriptAndStyle(t *testing.T) {
	var requests []url.Values
	service := newTestService(t, echoHandler(t, &requests))

	translated, ok := service.translateHTML(`<style>.a{color:red}</style><script>alert(1)</script><p>text</p>`, "de")
	if !ok {
		t.Fatal("the translation failed")
	}
	if !strings.Contains(translated, "<style>.a{color:red}</style>") {
		t.Fatalf("the style block was altered: %q", translated)
	}
	if !strings.Contains(translated, "<script>alert(1)</script>") {
		t.Fatalf("the script block was altered: %q", translated)
	}
	if len(requests) != 1 || len(requests[0]["q"]) != 1 {
		t.Fatalf("unexpected texts sent: %v", requests)
	}
}

// A fragment without prose costs no request at all.
func TestTranslateHTMLWithoutTextNodes(t *testing.T) {
	service := newTestService(t, func(http.ResponseWriter, *http.Request) {
		t.Error("the endpoint was called for a fragment without text")
	})

	translated, ok := service.translateHTML(`<img src="a.png"><br>`, "de")
	if !ok {
		t.Fatal("the translation failed")
	}
	if !strings.Contains(translated, "<img src=\"a.png\"/>") {
		t.Fatalf("unexpected result %q", translated)
	}
}

func TestTranslateHTMLReportsAFailedBatch(t *testing.T) {
	service := newTestService(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	})

	if _, ok := service.translateHTML("<p>text</p>", "de"); ok {
		t.Fatal("a failed batch reported success")
	}
}

func TestTranslateHTMLWithAnEmptyFragment(t *testing.T) {
	service := newTestService(t, echoHandler(t, nil))
	if _, ok := service.translateHTML("", "de"); !ok {
		t.Fatal("an empty fragment failed")
	}
}

func TestUpsertRowReplacesTheContent(t *testing.T) {
	service := New(newTestDatabase(t))
	designID := insertDesign(t, service.DB, "Name", "Description")

	service.upsertRow(designID, "name", "de", "first")
	service.upsertRow(designID, "name", "de", "second")

	var content string
	var rowCount int
	service.DB.QueryRow("SELECT COUNT(*) FROM design_translations WHERE design_id=?", designID).Scan(&rowCount)
	service.DB.QueryRow("SELECT content FROM design_translations WHERE design_id=? AND lang='de'", designID).Scan(&content)
	if rowCount != 1 {
		t.Fatalf("expected one row, got %d", rowCount)
	}
	if content != "second" {
		t.Fatalf("unexpected content %q", content)
	}
}

func TestOriginalOfReturnsTheStoredText(t *testing.T) {
	service := New(newTestDatabase(t))
	designID := insertDesign(t, service.DB, "Name", "Description")

	if original := service.OriginalOf(designID, "name"); original != "" {
		t.Fatalf("an unknown original returned %q", original)
	}
	service.upsertRow(designID, "name", "original", "Ursprünglicher Name")
	if original := service.OriginalOf(designID, "name"); original != "Ursprünglicher Name" {
		t.Fatalf("unexpected original %q", original)
	}
}

func TestDeleteDisplayRowsKeepsTheOriginal(t *testing.T) {
	service := New(newTestDatabase(t))
	designID := insertDesign(t, service.DB, "Name", "Description")
	service.upsertRow(designID, "name", "original", "Ursprung")
	service.upsertRow(designID, "name", "de", "Anzeige")

	service.deleteDisplayRows(designID)

	var languages []string
	rows, _ := service.DB.Query("SELECT lang FROM design_translations WHERE design_id=?", designID)
	defer rows.Close()
	for rows.Next() {
		var language string
		rows.Scan(&language)
		languages = append(languages, language)
	}
	if len(languages) != 1 || languages[0] != "original" {
		t.Fatalf("unexpected rows %v", languages)
	}
}

func TestApplyToDesignStoresOriginalAndDisplayRows(t *testing.T) {
	service := newTestService(t, echoHandler(t, nil))
	designID := insertDesign(t, service.DB, "Würfel", "Ein Würfel")

	if !service.ApplyToDesign(designID, "Würfel", pointerTo("Ein Würfel"), true) {
		t.Fatal("the pipeline reported a failure")
	}

	var name, description string
	service.DB.QueryRow("SELECT name, description FROM designs WHERE id=?", designID).Scan(&name, &description)
	if name != "en:Würfel" || description != "en:Ein Würfel" {
		t.Fatalf("the design was not translated into the canonical language: %q/%q", name, description)
	}
	for _, expectation := range []struct{ field, language, content string }{
		{"name", "original", "Würfel"},
		{"description", "original", "Ein Würfel"},
		{"name", "de", "de:Würfel"},
		{"description", "de", "de:Ein Würfel"},
	} {
		var content string
		failure := service.DB.QueryRow(
			"SELECT content FROM design_translations WHERE design_id=? AND field=? AND lang=?",
			designID, expectation.field, expectation.language,
		).Scan(&content)
		if failure != nil {
			t.Fatalf("%s/%s is missing: %v", expectation.field, expectation.language, failure)
		}
		if content != expectation.content {
			t.Fatalf("%s/%s holds %q", expectation.field, expectation.language, content)
		}
	}
}

// An edit keeps the original that the download stored.
func TestApplyToDesignWithoutStoringTheOriginal(t *testing.T) {
	service := newTestService(t, echoHandler(t, nil))
	designID := insertDesign(t, service.DB, "Name", "Description")

	if !service.ApplyToDesign(designID, "Name", pointerTo("Description"), false) {
		t.Fatal("the pipeline reported a failure")
	}

	var rowCount int
	service.DB.QueryRow(
		"SELECT COUNT(*) FROM design_translations WHERE design_id=? AND lang='original'", designID,
	).Scan(&rowCount)
	if rowCount != 0 {
		t.Fatalf("%d original rows were written", rowCount)
	}
}

func TestApplyToDesignWithoutADescription(t *testing.T) {
	service := newTestService(t, echoHandler(t, nil))
	designID := insertDesign(t, service.DB, "Name", "Description")

	for _, description := range []*string{nil, pointerTo("   ")} {
		if !service.ApplyToDesign(designID, "Name", description, true) {
			t.Fatal("the pipeline reported a failure")
		}
		var stored string
		service.DB.QueryRow("SELECT description FROM designs WHERE id=?", designID).Scan(&stored)
		if stored != "Description" {
			t.Fatalf("the description was overwritten with %q", stored)
		}
		var rowCount int
		service.DB.QueryRow(
			"SELECT COUNT(*) FROM design_translations WHERE design_id=? AND field='description'", designID,
		).Scan(&rowCount)
		if rowCount != 0 {
			t.Fatalf("%d description rows were written", rowCount)
		}
	}
}

// designs.name is limited to 255 runes, so a long translated title has to be cut
// before it reaches the column.
func TestApplyToDesignTruncatesTheName(t *testing.T) {
	service := newTestService(t, echoHandler(t, nil))
	designID := insertDesign(t, service.DB, "Name", "Description")
	longName := strings.Repeat("ä", 400)

	if !service.ApplyToDesign(designID, longName, nil, false) {
		t.Fatal("the pipeline reported a failure")
	}

	var stored string
	service.DB.QueryRow("SELECT name FROM designs WHERE id=?", designID).Scan(&stored)
	if length := len([]rune(stored)); length != 255 {
		t.Fatalf("the stored name has %d runes", length)
	}
}

func TestApplyToDesignDoesNothingWhenDisabled(t *testing.T) {
	service := newTestService(t, func(http.ResponseWriter, *http.Request) {
		t.Error("the endpoint was called although translation is disabled")
	})
	service.DB.Exec("UPDATE app_settings SET value='0' WHERE key='translation_enabled'")
	designID := insertDesign(t, service.DB, "Name", "Description")

	if service.ApplyToDesign(designID, "Name", pointerTo("Description"), true) {
		t.Fatal("the pipeline ran although translation is disabled")
	}
}

// A failing endpoint must not leave half-translated display rows behind.
func TestApplyToDesignClearsDisplayRowsOnFailure(t *testing.T) {
	service := newTestService(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
	designID := insertDesign(t, service.DB, "Name", "Description")
	service.upsertRow(designID, "name", "de", "veraltet")

	if service.ApplyToDesign(designID, "Name", pointerTo("Description"), true) {
		t.Fatal("a failed translation reported success")
	}

	var rowCount int
	service.DB.QueryRow("SELECT COUNT(*) FROM design_translations WHERE design_id=? AND lang='de'", designID).Scan(&rowCount)
	if rowCount != 0 {
		t.Fatalf("%d stale display rows survived", rowCount)
	}
}

// The canonical translation succeeded, only the display language failed: the
// design keeps its English text, the display rows are dropped.
func TestApplyToDesignSurvivesAFailingDisplayLanguage(t *testing.T) {
	var callCount int
	service := newTestService(t, func(writer http.ResponseWriter, request *http.Request) {
		callCount++
		if callCount > 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		echoHandler(t, nil)(writer, request)
	})
	designID := insertDesign(t, service.DB, "Name", "Description")

	if !service.ApplyToDesign(designID, "Name", nil, true) {
		t.Fatal("the pipeline reported a failure although the canonical language worked")
	}

	var name string
	service.DB.QueryRow("SELECT name FROM designs WHERE id=?", designID).Scan(&name)
	if name != "en:Name" {
		t.Fatalf("unexpected name %q", name)
	}
	var rowCount int
	service.DB.QueryRow("SELECT COUNT(*) FROM design_translations WHERE design_id=? AND lang='de'", designID).Scan(&rowCount)
	if rowCount != 0 {
		t.Fatalf("%d display rows were written", rowCount)
	}
}

func insertDesign(t *testing.T, database *sql.DB, name, description string) int {
	t.Helper()
	result, failure := database.Exec(
		"INSERT INTO designs (user_id, name, description, source_platform) VALUES (1, ?, ?, 'manual')",
		name, description,
	)
	if failure != nil {
		t.Fatalf("insert design: %v", failure)
	}
	id, _ := result.LastInsertId()
	return int(id)
}

func pointerTo(value string) *string { return &value }
