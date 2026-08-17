package platforms

import (
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
