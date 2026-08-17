package platforms

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestMakerworldIDExtraction(t *testing.T) {
	cases := []struct{ url, model, profile string }{
		{"https://makerworld.com/en/models/123456", "123456", ""},
		{"https://makerworld.com/en/models/789-foo?profileId-456", "789", "456"},
		{"https://makerworld.com/models/42#profileId-7", "42", "7"},
		{"https://makerworld.com/en/u/someone", "", ""},
	}
	for _, testCase := range cases {
		model := ""
		if match := modelIDPattern.FindStringSubmatch(testCase.url); match != nil {
			model = match[1]
		}
		profile := ""
		if profileMatch := profileIDPattern.FindStringSubmatch(testCase.url); profileMatch != nil {
			profile = profileMatch[1]
		}
		if model != testCase.model || profile != testCase.profile {
			t.Errorf("%q -> model=%q profile=%q, want model=%q profile=%q", testCase.url, model, profile, testCase.model, testCase.profile)
		}
	}
}

func TestGenerateTotpFormat(t *testing.T) {
	code := generateTotp("JBSWY3DPEHPK3PXP")
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) {
		t.Errorf("TOTP code %q is not 6 digits", code)
	}
	// Stable within the same 30-second window.
	if again := generateTotp("JBSWY3DPEHPK3PXP"); again != code {
		t.Errorf("TOTP not stable within window: %q vs %q", code, again)
	}
	// Spaces in the secret are ignored (same result).
	if spaced := generateTotp("JBSW Y3DP EHPK 3PXP"); spaced != code {
		t.Errorf("TOTP with spaces differs: %q vs %q", spaced, code)
	}
}

// testSeed is the example secret from RFC 4648/6238, not a real one.
const testSeed = "JBSWY3DPEHPK3PXP"

// startBambuStub serves both Bambu hosts: the API host hands out a tfaKey, the
// website host runs the CSRF double-submit and then answers with tfaStatus. It
// points bambuAPIHost/bambuWebHost at itself for the duration of the test.
func startBambuStub(t *testing.T, tfaStatus int) (csrfSeen *bool) {
	t.Helper()
	sawCSRF := false

	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"accessToken":"","loginType":"tfa","tfaKey":"stub-tfa-key"}`))
	}))
	web := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/csrf":
			http.SetCookie(writer, &http.Cookie{Name: "bbl_csrf_token", Value: "stub-csrf-token", Path: "/"})
			writer.WriteHeader(http.StatusNoContent)
		case "/api/sign-in/tfa":
			cookie, failure := request.Cookie("bbl_csrf_token")
			header := request.Header.Get("X-BBL-CSRF-Token")
			if failure != nil || header == "" || cookie.Value != header {
				writer.WriteHeader(http.StatusForbidden)
				_, _ = writer.Write([]byte(`{"error":"CSRF error: missing_cookie"}`))
				return
			}
			sawCSRF = true
			if tfaStatus == http.StatusOK {
				http.SetCookie(writer, &http.Cookie{Name: "token", Value: "stub-bearer-token", Path: "/"})
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write([]byte(`{}`))
				return
			}
			writer.WriteHeader(tfaStatus)
			_, _ = writer.Write([]byte(`{"code":5,"error":"Login failed"}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))

	originalAPI, originalWeb := bambuAPIHost, bambuWebHost
	bambuAPIHost, bambuWebHost = api.URL, web.URL
	t.Cleanup(func() {
		bambuAPIHost, bambuWebHost = originalAPI, originalWeb
		api.Close()
		web.Close()
	})
	return &sawCSRF
}

// TestMakerworldLoginPerformsCSRFHandshake pins the double-submit: without the
// cookie/header pair the endpoint never reaches the code check.
func TestMakerworldLoginPerformsCSRFHandshake(t *testing.T) {
	csrfSeen := startBambuStub(t, http.StatusOK)

	token, reason := makerworldLogin("user@example.com", "password", testSeed)
	if !*csrfSeen {
		t.Fatal("TFA request did not carry the CSRF cookie/header pair")
	}
	if reason != "" {
		t.Errorf("reason = %q, want empty", reason)
	}
	if token != "stub-bearer-token" {
		t.Errorf("token = %q, want the token from the Set-Cookie header", token)
	}
}

// TestMakerworldLoginErrorReasons guards the classification: only an app-level
// rejection may blame the seed. Reporting "seed incorrect" for a blocked or
// unreachable endpoint sends users looking for a fault that is not theirs.
func TestMakerworldLoginErrorReasons(t *testing.T) {
	cases := []struct {
		name       string
		tfaStatus  int
		wantReason string
	}{
		{"rejected code", http.StatusBadRequest, "error.makerworld_invalid_totp"},
		{"rate limited", http.StatusTooManyRequests, "error.makerworld_rate_limited"},
		{"blocked by the edge", http.StatusForbidden, "error.makerworld_login_blocked"},
		{"server error", http.StatusInternalServerError, "error.makerworld_login_blocked"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			startBambuStub(t, testCase.tfaStatus)
			token, reason := makerworldLogin("user@example.com", "password", testSeed)
			if token != "" {
				t.Errorf("token = %q, want empty", token)
			}
			if reason != testCase.wantReason {
				t.Errorf("reason = %q, want %q", reason, testCase.wantReason)
			}
		})
	}
}
