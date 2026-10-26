package platforms

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestModelIDExtraction(t *testing.T) {
	cases := map[string]string{
		"https://www.printables.com/model/12345-cool-thing": "12345",
		"https://www.printables.com/de/model/99/files":      "99",
		"https://www.printables.com/model/7":                "7",
		"https://www.printables.com/social/profile/abc":     "",
	}
	for url, want := range cases {
		match := printablesModelIDPattern.FindStringSubmatch(url)
		got := ""
		if match != nil {
			got = match[1]
		}
		if got != want {
			t.Errorf("modelID(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestSanitizeRelName(t *testing.T) {
	cases := map[string]string{
		"Body (v2).stl":      "Body (v2).stl",
		"parts/left arm.3mf": "parts/left arm.3mf",
		"weird*name?.stl":    "weird_name_.stl",
	}
	for in, want := range cases {
		if got := sanitizeRelName(in); got != want {
			t.Errorf("sanitizeRelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBase64URL(t *testing.T) {
	out := base64URL([]byte{0xff, 0xfe, 0xfd})
	if strings.ContainsAny(out, "+/=") {
		t.Errorf("base64URL must be url-safe and unpadded, got %q", out)
	}
}

func TestCollectCookies(t *testing.T) {
	hdr := http.Header{"Set-Cookie": {
		"sessionid=abc; Path=/; HttpOnly",
		"csrftoken=xyz; Path=/",
		"sessionid=abc; Path=/", // exact duplicate of name=value
	}}
	got := collectCookies([]string{"prev=1"}, hdr)
	want := []string{"prev=1", "sessionid=abc", "csrftoken=xyz"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("collectCookies = %v, want %v", got, want)
	}
}

func TestAbsLocation(t *testing.T) {
	if got := absLocation("/o/authorize/?x=1"); got != "https://account.prusa3d.com/o/authorize/?x=1" {
		t.Errorf("absLocation relative = %q", got)
	}
	if got := absLocation("https://www.printables.com/login?code=k"); got != "https://www.printables.com/login?code=k" {
		t.Errorf("absLocation absolute changed = %q", got)
	}
}

func TestTokenExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05")
	future := time.Now().Add(time.Hour).Format("2006-01-02 15:04:05")
	if !tokenExpired(past) {
		t.Error("past timestamp should be expired")
	}
	if tokenExpired(future) {
		t.Error("future timestamp should not be expired")
	}
	if tokenExpired("garbage") {
		t.Error("unparseable timestamp should be treated as not expired")
	}
}

func TestUnusableGraphQLAnswerIsLoggedWithoutTheToken(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)

	headers := map[string]string{"Authorization": "Bearer secret-token-value"}
	logUnusableGraphQLAnswer("direct", `{"query":"{ print(id: 1) { name } }"}`, headers,
		[]byte("<html>\n  <title>Just a moment...</title>\n</html>"), 403)

	line := output.String()
	for _, expected := range []string{"HTTP 403", "with token: true", "print(id: 1)", "<html> <title>Just a moment...</title> </html>"} {
		if !strings.Contains(line, expected) {
			t.Errorf("log line misses %q: %s", expected, line)
		}
	}
	if strings.Contains(line, "secret-token-value") {
		t.Errorf("log line contains the token: %s", line)
	}
}

func TestFailedConnectionIsLoggedAsNoAnswer(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)

	logUnusableGraphQLAnswer("tor attempt 1/2", "{}", map[string]string{}, nil, 0)

	line := output.String()
	for _, expected := range []string{"tor attempt 1/2", "no answer", "with token: false", "answer: (empty)"} {
		if !strings.Contains(line, expected) {
			t.Errorf("log line misses %q: %s", expected, line)
		}
	}
}

func TestLogSnippetCutsLongAnswersWithoutBreakingCharacters(t *testing.T) {
	snippet := logSnippet([]byte(strings.Repeat("ä", graphQLLogSnippetLength+50)))
	if snippet != strings.Repeat("ä", graphQLLogSnippetLength)+"…" {
		t.Errorf("unexpected snippet: %q", snippet)
	}
}

func TestModelWithoutDataIsLoggedWithTheAnswer(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)

	logModelWithoutData("1861126", true, []byte(`{"data":{"print":null}}`), nil)

	line := output.String()
	for _, expected := range []string{"model 1861126", "with token: true", "parse error: none", `{"data":{"print":null}}`} {
		if !strings.Contains(line, expected) {
			t.Errorf("log line misses %q: %s", expected, line)
		}
	}
}
