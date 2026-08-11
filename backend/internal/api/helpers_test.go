package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLikePatternNeutralizesTheWildcards(t *testing.T) {
	cases := []struct {
		input  string
		expect string
	}{
		{"lampe", "%lampe%"},
		{"50%", `%50\%%`},
		{"a_b", `%a\_b%`},
		{`c:\pfad`, `%c:\\pfad%`},
		{"", "%%"},
	}

	for _, testCase := range cases {
		if pattern := likePattern(testCase.input); pattern != testCase.expect {
			t.Fatalf("likePattern(%q) = %q, expected %q", testCase.input, pattern, testCase.expect)
		}
	}
}

func TestNullStrTurnsBlankValuesIntoNull(t *testing.T) {
	for _, blank := range []any{nil, "", "   ", "\t\n"} {
		if value := nullStr(blank); value != nil {
			t.Fatalf("nullStr(%q) = %v", blank, value)
		}
	}
	if value := nullStr("lampe"); value != "lampe" {
		t.Fatalf("nullStr passed on %v", value)
	}
	// Only strings are inspected - a number stays what it is.
	if value := nullStr(0); value != 0 {
		t.Fatalf("nullStr turned a zero into %v", value)
	}
}

func TestNullIntParsesTheUsualJSONShapes(t *testing.T) {
	cases := []struct {
		input  any
		expect any
	}{
		{nil, nil},
		{"", nil},
		{"42", 42},
		{"nonsense", 0},
		{float64(7), 7},
		{int(7), 7},
		{true, 1},
		{false, 0},
		{[]any{1}, nil},
	}

	for _, testCase := range cases {
		if value := nullInt(testCase.input); value != testCase.expect {
			t.Fatalf("nullInt(%v) = %v, expected %v", testCase.input, value, testCase.expect)
		}
	}
}

func TestJSONPathReadsNestedValues(t *testing.T) {
	document := `{"items":[{"sizes":[{"url":"https://example.org/bild.png"}]}],"name":"lampe","count":3}`

	cases := []struct {
		path   string
		expect string
	}{
		{"name", "lampe"},
		{"items.0.sizes.0.url", "https://example.org/bild.png"},
		{"items.1.sizes.0.url", ""},
		{"items.-1.sizes", ""},
		{"missing", ""},
		{"name.deeper", ""},
		{"count", ""},
		{"items", ""},
	}

	for _, testCase := range cases {
		if value := jsonPath(document, testCase.path); value != testCase.expect {
			t.Fatalf("jsonPath(%q) = %q, expected %q", testCase.path, value, testCase.expect)
		}
	}
}

func TestJSONPathSurvivesBrokenJSON(t *testing.T) {
	if value := jsonPath("{kein json", "name"); value != "" {
		t.Fatalf("broken json produced %q", value)
	}
}

func TestPathIntReadsNumericPathParametersOnly(t *testing.T) {
	mux := http.NewServeMux()
	var parsed int
	var ok bool
	mux.HandleFunc("GET /designs/{id}", func(responseWriter http.ResponseWriter, request *http.Request) {
		parsed, ok = pathInt(request, "id")
	})

	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/designs/17", nil))
	if !ok || parsed != 17 {
		t.Fatalf("a numeric id produced (%d, %v)", parsed, ok)
	}

	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/designs/abc", nil))
	if ok {
		t.Fatal("a non-numeric id was accepted")
	}
}

func TestQueryStrFallsBackToTheDefault(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/designs?search=lampe&empty=", nil)

	if value := queryStr(request, "search", "nichts"); value != "lampe" {
		t.Fatalf("search read as %q", value)
	}
	if value := queryStr(request, "missing", "nichts"); value != "nichts" {
		t.Fatalf("a missing parameter read as %q", value)
	}
	// An empty parameter counts as absent, otherwise "?sort=" would clear the
	// default sort order.
	if value := queryStr(request, "empty", "nichts"); value != "nichts" {
		t.Fatalf("an empty parameter read as %q", value)
	}
}

func TestQueryIntFallsBackOnAnythingUnparsable(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/designs?page=3&broken=abc&empty=&negative=-2", nil)

	cases := []struct {
		key    string
		expect int
	}{
		{"page", 3},
		{"broken", 9},
		{"empty", 9},
		{"missing", 9},
		{"negative", -2},
	}

	for _, testCase := range cases {
		if value := queryInt(request, testCase.key, 9); value != testCase.expect {
			t.Fatalf("%s read as %d, expected %d", testCase.key, value, testCase.expect)
		}
	}
}

func TestQueryBoolTreatsOnlyZeroAndFalseAsOff(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/designs?on=1&word=true&zero=0&off=false&empty=", nil)

	cases := []struct {
		key    string
		expect bool
	}{
		{"on", true},
		{"word", true},
		{"zero", false},
		{"off", false},
		{"empty", false},
		{"missing", false},
	}

	for _, testCase := range cases {
		if value := queryBool(request, testCase.key); value != testCase.expect {
			t.Fatalf("%s read as %v", testCase.key, value)
		}
	}
}
