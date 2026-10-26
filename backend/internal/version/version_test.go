package version

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewerComparesNumbersNotText(t *testing.T) {
	cases := []struct {
		candidate, current string
		newer              bool
	}{
		{"1.5.3", "1.5.2", true},
		{"1.5.10", "1.5.9", true},
		{"v1.6.0", "1.5.2", true},
		{"2.0", "1.9.9", true},
		{"1.5.2", "1.5.2", false},
		{"1.5", "1.5.0", false},
		{"1.5.1", "1.5.2", false},
		{"1.4.99", "1.5.0", false},
		{"", "1.5.2", false},
		{"1.6.0-beta", "1.5.2", false},
	}
	for _, testCase := range cases {
		if got := Newer(testCase.candidate, testCase.current); got != testCase.newer {
			t.Errorf("Newer(%q, %q) = %t, want %t", testCase.candidate, testCase.current, got, testCase.newer)
		}
	}
}

func checkerAgainst(t *testing.T, status int, body string) *Checker {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	checker := NewChecker()
	checker.address = server.URL
	return checker
}

func TestCheckReadsTheNewestRelease(t *testing.T) {
	checker := checkerAgainst(t, http.StatusOK,
		`{"tag_name":"v1.5.3","html_url":"https://github.com/MrDark7199/MeshDepot/releases/tag/v1.5.3"}`)
	checker.Check()
	if latest := checker.Latest(); latest.Version != "1.5.3" || latest.URL != "https://github.com/MrDark7199/MeshDepot/releases/tag/v1.5.3" {
		t.Errorf("unexpected release: %+v", latest)
	}
}

func TestCheckLinksOnlyToThisRepository(t *testing.T) {
	checker := checkerAgainst(t, http.StatusOK, `{"tag_name":"v1.5.3","html_url":"https://evil.example/download"}`)
	checker.Check()
	if latest := checker.Latest(); latest.Version != "1.5.3" || latest.URL != "" {
		t.Errorf("a foreign link must not be kept: %+v", latest)
	}
}

func TestFailedCheckKeepsThePreviousAnswer(t *testing.T) {
	checker := checkerAgainst(t, http.StatusForbidden, `{"message":"API rate limit exceeded"}`)
	checker.latest = Release{Version: "1.5.3"}
	checker.Check()
	if latest := checker.Latest(); latest.Version != "1.5.3" {
		t.Errorf("a failed check must keep the last answer: %+v", latest)
	}
}
