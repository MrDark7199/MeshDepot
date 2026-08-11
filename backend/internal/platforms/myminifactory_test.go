package platforms

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMMFObjectIDExtraction(t *testing.T) {
	cases := []struct{ url, slug, objectID string }{
		{"https://www.myminifactory.com/object/3d-print-cool-thing-123456", "3d-print-cool-thing-123456", "123456"},
		{"https://www.myminifactory.com/object/widget?foo=bar", "widget", "widget"},
		{"https://www.myminifactory.com/object/thing-99/files", "thing-99", "99"},
		{"https://www.myminifactory.com/users/someone", "", ""},
	}
	for _, testCase := range cases {
		slugMatch := myMiniFactorySlugPattern.FindStringSubmatch(testCase.url)
		slug, objectID := "", ""
		if slugMatch != nil {
			slug = slugMatch[1]
			objectID = slug
			if idMatch := myMiniFactoryIDPattern.FindStringSubmatch(slug); idMatch != nil {
				objectID = idMatch[1]
			}
		}
		if slug != testCase.slug || objectID != testCase.objectID {
			t.Errorf("%q -> slug=%q id=%q, want slug=%q id=%q", testCase.url, slug, objectID, testCase.slug, testCase.objectID)
		}
	}
}

func TestMMFParseTags(t *testing.T) {
	var rawTags []json.RawMessage
	_ = json.Unmarshal([]byte(`["dragon", {"name":"toy"}, {"other":"x"}, ""]`), &rawTags)
	tags := parseMyMiniFactoryTags(rawTags)
	if strings.Join(tags, ",") != "dragon,toy" {
		t.Errorf("tags = %v, want [dragon toy]", tags)
	}
}

func TestMMFNormalizeDesc(t *testing.T) {
	// The MMF API returns line breaks as runs of 2+ spaces / nbsp.
	input := "Title - 2025  Intro line with single spaces.  ---  Contents:    Item A  Item B"
	want := "Title - 2025\nIntro line with single spaces.\n---\nContents:\nItem A\nItem B"
	if got := normalizeMyMiniFactoryDescription(input); got != want {
		t.Fatalf("normalizeMyMiniFactoryDescription:\n got=%q\nwant=%q", got, want)
	}
	if got := normalizeMyMiniFactoryDescription("a  b"); got != "a\nb" {
		t.Fatalf("nbsp run: got=%q", got)
	}
	if got := normalizeMyMiniFactoryDescription("single spaces stay here"); got != "single spaces stay here" {
		t.Fatalf("single spaces must stay: got=%q", got)
	}
	if got := normalizeMyMiniFactoryDescription(""); got != "" {
		t.Fatalf("empty: got=%q", got)
	}
}
