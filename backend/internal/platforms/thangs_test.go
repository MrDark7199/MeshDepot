package platforms

import (
	"html"
	"strings"
	"testing"
)

func TestThangsModelIDExtraction(t *testing.T) {
	cases := map[string]string{
		"https://thangs.com/designer/x/3d-model/cool-thing-123456": "123456",
		"https://thangs.com/m/model-789":                           "789",
		"https://thangs.com/3d-model/42":                           "42",
		"https://thangs.com/designer/foo":                          "",
	}
	for url, want := range cases {
		match := thangsModelIDPattern.FindStringSubmatch(url)
		got := ""
		if match != nil {
			got = match[1]
		}
		if got != want {
			t.Errorf("thangsModelID(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestThangsTitleExtraction(t *testing.T) {
	cases := map[string]string{
		"<html><head><title>Cool Widget | Thangs</title></head>": "Cool Widget",
		"<title>Gadget - Free 3D Model</title>":                  "Gadget",
		"<title>Plain &amp; Simple</title>":                      "Plain & Simple",
	}
	for body, want := range cases {
		titleMatch := titlePattern.FindStringSubmatch(body)
		if titleMatch == nil {
			t.Errorf("no title found in %q", body)
			continue
		}
		got := html.UnescapeString(strings.TrimSpace(titleSuffixPattern.ReplaceAllString(titleMatch[1], "")))
		if got != want {
			t.Errorf("title(%q) = %q, want %q", body, got, want)
		}
	}
}
