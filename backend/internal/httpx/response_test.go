package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var decoded map[string]any
	if failure := json.Unmarshal(recorder.Body.Bytes(), &decoded); failure != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), failure)
	}
	return decoded
}

func TestJSONWritesStatusAndContentType(t *testing.T) {
	recorder := httptest.NewRecorder()
	JSON(recorder, http.StatusTeapot, map[string]any{"value": 42})

	if recorder.Code != http.StatusTeapot {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("unexpected content type %q", contentType)
	}
	if decodeBody(t, recorder)["value"] != float64(42) {
		t.Fatalf("unexpected body %s", recorder.Body)
	}
}

func TestSuccessUsesEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	Success(recorder, map[string]any{"id": 7})

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
	decoded := decodeBody(t, recorder)
	if decoded["success"] != true || decoded["message"] != "OK" {
		t.Fatalf("unexpected envelope %v", decoded)
	}
	data, isObject := decoded["data"].(map[string]any)
	if !isObject || data["id"] != float64(7) {
		t.Fatalf("unexpected data %v", decoded["data"])
	}
}

// data is omitempty, so an endpoint that only confirms an action sends no key
// at all instead of "data": null.
func TestSuccessOmitsEmptyData(t *testing.T) {
	recorder := httptest.NewRecorder()
	Success(recorder, nil)

	if _, present := decodeBody(t, recorder)["data"]; present {
		t.Fatalf("data was serialised: %s", recorder.Body)
	}
}

func TestSuccessMessageKeepsCustomMessage(t *testing.T) {
	recorder := httptest.NewRecorder()
	SuccessMessage(recorder, []int{1, 2}, "design.deleted")

	decoded := decodeBody(t, recorder)
	if decoded["message"] != "design.deleted" {
		t.Fatalf("unexpected message %v", decoded["message"])
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
}

func TestSuccessStatusKeepsStatusAndMessage(t *testing.T) {
	recorder := httptest.NewRecorder()
	SuccessStatus(recorder, http.StatusCreated, map[string]any{"id": 1}, "design.created")

	if recorder.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
	decoded := decodeBody(t, recorder)
	if decoded["success"] != true || decoded["message"] != "design.created" {
		t.Fatalf("unexpected envelope %v", decoded)
	}
}

// The error shape has no success field - the frontend distinguishes the two by
// the status code and the presence of "error".
func TestErrorWritesKeyOnly(t *testing.T) {
	recorder := httptest.NewRecorder()
	Error(recorder, http.StatusNotFound, "error.not_found")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
	decoded := decodeBody(t, recorder)
	if decoded["error"] != "error.not_found" {
		t.Fatalf("unexpected error %v", decoded["error"])
	}
	if len(decoded) != 1 {
		t.Fatalf("the error body carries extra keys: %v", decoded)
	}
}

// The frontend splits at the first colon, so a parameter that itself contains
// one has to survive intact.
func TestErrorKeepsParameterWithColon(t *testing.T) {
	recorder := httptest.NewRecorder()
	Error(recorder, http.StatusBadGateway, "error.platform_failed:https://example.org/model")

	if decodeBody(t, recorder)["error"] != "error.platform_failed:https://example.org/model" {
		t.Fatalf("the parameter was altered: %s", recorder.Body)
	}
}

// json.Encoder writes the status first, so an unencodable value still produces
// the intended code instead of a 200.
func TestJSONKeepsStatusWhenEncodingFails(t *testing.T) {
	recorder := httptest.NewRecorder()
	JSON(recorder, http.StatusInternalServerError, make(chan int))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
}

// The response bodies are assembled from SELECT * rows, so the internal columns
// have to be removed on the way out - at every depth, or a nested design in a
// collection would carry the owner the top level just dropped.
func TestJSONStripsInternalKeysAtEveryDepth(t *testing.T) {
	recorder := httptest.NewRecorder()
	Success(recorder, map[string]any{
		"id":      7,
		"user_id": 1,
		"nested":  map[string]any{"user_id": 2, "hash": "secret", "name": "keep"},
		"list": []any{
			map[string]any{"shared_with_user_id": 3, "id": 9},
			[]any{map[string]any{"owner_user_id": 4, "totp_secret": "s", "keep": true}},
		},
		"rows": []map[string]any{{"avatar_path": "/data/x", "label": "keep"}},
	})

	body := recorder.Body.String()
	for _, forbidden := range []string{"user_id", "owner_user_id", "shared_with_user_id", "hash", "totp_secret", "avatar_path"} {
		if strings.Contains(body, `"`+forbidden+`"`) {
			t.Errorf("%s survived at some depth: %s", forbidden, body)
		}
	}
	// Everything else has to stay - the scrubber is a deny list, not a filter.
	for _, kept := range []string{`"id":7`, `"name":"keep"`, `"id":9`, `"keep":true`, `"label":"keep"`} {
		if !strings.Contains(body, kept) {
			t.Errorf("%s was removed: %s", kept, body)
		}
	}
}

// The rowid never travels: a row that carries a public id has it put in place
// of "id" on the way out, wherever in the body it sits. Handlers build their
// bodies with SELECT *, so both columns are there by construction and relying
// on each handler to swap them would only need one to forget.
func TestJSONPromotesThePublicIDOverTheRowID(t *testing.T) {
	recorder := httptest.NewRecorder()
	Success(recorder, map[string]any{
		"id":        70,
		"public_id": "6542f3b633ea5113dde299b0aba0c101",
		"nested":    map[string]any{"id": 71, "public_id": "aa11bb22cc33dd44ee55ff6677889900"},
		"rows":      []map[string]any{{"id": 3, "name": "a tag keeps its numeric id"}},
	})

	body := recorder.Body.String()
	if strings.Contains(body, `"public_id"`) {
		t.Errorf("public_id travelled alongside id: %s", body)
	}
	for _, forbidden := range []string{`"id":70`, `"id":71`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the rowid %s survived: %s", forbidden, body)
		}
	}
	for _, wanted := range []string{`"id":"6542f3b633ea5113dde299b0aba0c101"`, `"id":"aa11bb22cc33dd44ee55ff6677889900"`, `"id":3`} {
		if !strings.Contains(body, wanted) {
			t.Errorf("%s is missing: %s", wanted, body)
		}
	}
}

// An empty public id would replace a working id with nothing. The backfill
// makes it impossible, but a response is the wrong place to discover that.
func TestJSONKeepsTheIDWhenThePublicIDIsBlank(t *testing.T) {
	recorder := httptest.NewRecorder()
	Success(recorder, map[string]any{"id": 5, "public_id": ""})

	if body := recorder.Body.String(); !strings.Contains(body, `"id":5`) {
		t.Errorf("a blank public id took the id with it: %s", body)
	}
}
