package platforms

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"meshdepot/internal/logx"
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

func TestUnusableGraphQLAnswerIsDescribedWithoutTheToken(t *testing.T) {
	headers := map[string]string{"Authorization": "Bearer secret-token-value"}
	line := describeUnusableGraphQLAnswer("direct", `{"query":"{ print(id: 1) { name } }"}`, headers,
		[]byte("<html>\n  <title>Just a moment...</title>\n</html>"), 403)

	for _, expected := range []string{"HTTP 403", "with token: true", "print(id: 1)", "<html> <title>Just a moment...</title> </html>"} {
		if !strings.Contains(line, expected) {
			t.Errorf("description misses %q: %s", expected, line)
		}
	}
	if strings.Contains(line, "secret-token-value") {
		t.Errorf("description contains the token: %s", line)
	}
}

func TestFailedConnectionIsDescribedAsNoAnswer(t *testing.T) {
	line := describeUnusableGraphQLAnswer("tor attempt 1/2", "{}", map[string]string{}, nil, 0)

	for _, expected := range []string{"tor attempt 1/2", "no answer", "with token: false", "answer: (empty)"} {
		if !strings.Contains(line, expected) {
			t.Errorf("description misses %q: %s", expected, line)
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

	logx.Configure("error")
	defer logx.Configure("all")
	logModelWithoutData("1861126", true, []byte(`{"data":{"print":null}}`), nil)

	line := output.String()
	for _, expected := range []string{"model 1861126", "with token: true", "parse error: none", `{"data":{"print":null}}`} {
		if !strings.Contains(line, expected) {
			t.Errorf("log line misses %q: %s", expected, line)
		}
	}
}

// printablesStandIn answers like the GraphQL API: 401 for the expired token,
// the model for a valid token or for no token at all when the model is public.
func printablesStandIn(t *testing.T, public bool) (*[]string, func()) {
	t.Helper()
	var seenAuthorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization := request.Header.Get("Authorization")
		seenAuthorizations = append(seenAuthorizations, authorization)
		switch {
		case authorization == "Bearer expired":
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte("verse.token_is_expired"))
		case authorization == "Bearer fresh" || (authorization == "" && public):
			_, _ = writer.Write([]byte(`{"data":{"print":{"name":"Lamp","stls":[{"id":"7","name":"body.stl"}]}}}`))
		default:
			_, _ = writer.Write([]byte(`{"data":{"print":null}}`))
		}
	}))
	previous := printablesGraphQL
	printablesGraphQL = server.URL
	return &seenAuthorizations, func() {
		printablesGraphQL = previous
		server.Close()
	}
}

func TestTurnedAwayLoginFallsBackToNoLoginForPublicModels(t *testing.T) {
	seen, stop := printablesStandIn(t, true)
	defer stop()

	headers := map[string]string{"Authorization": "Bearer expired"}
	model, failure := Printables{}.fetchModel("1", 3, headers, func() string { return "" })
	if failure != nil || model == nil || model.Name != "Lamp" {
		t.Fatalf("expected the public model without a login, got %v, %v", model, failure)
	}
	if len(*seen) != 2 || (*seen)[1] != "" {
		t.Errorf("expected one try with the token and one without, saw %q", *seen)
	}
	if _, stillThere := headers["Authorization"]; stillThere {
		t.Error("the turned-away token must leave the headers, or the file downloads send it again")
	}
}

func TestTurnedAwayLoginOnAPrivateModelPointsAtTheSettings(t *testing.T) {
	_, stop := printablesStandIn(t, false)
	defer stop()

	_, failure := Printables{}.fetchModel("1", 3, map[string]string{"Authorization": "Bearer expired"}, func() string { return "" })
	if failure == nil || !strings.HasPrefix(failure.Error(), "error.printables_auth_failed:") {
		t.Fatalf("expected the login error, got %v", failure)
	}
}

func TestRenewedLoginIsUsedBeforeGoingWithoutOne(t *testing.T) {
	seen, stop := printablesStandIn(t, false)
	defer stop()

	headers := map[string]string{"Authorization": "Bearer expired"}
	model, failure := Printables{}.fetchModel("1", 3, headers, func() string { return "fresh" })
	if failure != nil || model == nil {
		t.Fatalf("expected the model with the renewed token, got %v", failure)
	}
	if len(*seen) != 2 || (*seen)[1] != "Bearer fresh" {
		t.Errorf("expected the renewed token on the second try, saw %q", *seen)
	}
}

func TestMissingModelWithAWorkingLoginIsNotAskedForAgain(t *testing.T) {
	seen, stop := printablesStandIn(t, false)
	defer stop()

	_, failure := Printables{}.fetchModel("1", 3, map[string]string{"Authorization": "Bearer valid"}, func() string { return "" })
	if failure == nil || strings.HasPrefix(failure.Error(), "error.printables_auth_failed:") {
		t.Fatalf("expected the plain no-data error, got %v", failure)
	}
	if len(*seen) != 1 {
		t.Errorf("a token that works must not be dropped, saw %q", *seen)
	}
}

func TestLoginHopKeepsParameterNamesButNotTheirValues(t *testing.T) {
	hop := describeLoginHop(302, "https://www.printables.com/login?state=abc123&code=secret-code", nil)
	if hop != "HTTP 302 to www.printables.com/login with code, state" {
		t.Errorf("unexpected hop: %q", hop)
	}
	if strings.Contains(hop, "secret-code") || strings.Contains(hop, "abc123") {
		t.Errorf("hop leaks a parameter value: %q", hop)
	}
}

func TestLoginHopWithoutRedirectNamesThePage(t *testing.T) {
	hop := describeLoginHop(200, "", []byte("<html><head><title>\n  Two-factor authentication | Prusa Account\n</title></head></html>"))
	if hop != `HTTP 200, no further redirect, page title "Two-factor authentication | Prusa Account"` {
		t.Errorf("unexpected hop: %q", hop)
	}
}

func TestLoginSkipsThePasskeyOffer(t *testing.T) {
	authorize := "/o/authorize/?client_id=abc&response_type=code&redirect_uri=https%3A%2F%2Fwww.printables.com%2Flogin%3Fstate%3Dx"
	offer := "https://account.prusa3d.com/passkeys/offer/?next=" + url.QueryEscape(authorize)
	if destination := interstitialDestination(offer); destination != "https://account.prusa3d.com"+authorize {
		t.Errorf("expected the authorization, got %q", destination)
	}
}

func TestLoginFollowsOnlyDetoursOnPrusa(t *testing.T) {
	for _, address := range []string{
		"https://account.prusa3d.com/login/?next=%2Fo%2Fauthorize%2F",
		"https://account.prusa3d.com/o/authorize/?next=%2Fsomewhere",
		"https://account.prusa3d.com/passkeys/offer/?next=https%3A%2F%2Fevil.example%2F",
		"https://account.prusa3d.com/passkeys/offer/?next=%2F%2Fevil.example%2F",
		"https://account.prusa3d.com/passkeys/offer/",
		"https://elsewhere.example/passkeys/offer/?next=%2Fo%2Fauthorize%2F",
	} {
		if destination := interstitialDestination(address); destination != "" {
			t.Errorf("%s must not be skipped, got %q", address, destination)
		}
	}
}
