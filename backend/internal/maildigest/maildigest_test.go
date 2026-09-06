package maildigest

import (
	"strings"
	"testing"
)

// One entry reads like an immediate message; several must not repeat the same
// title as a subject for a batch of thirty-four.
func TestComposeSubjectDependsOnTheCount(t *testing.T) {
	single, _ := compose([]pending{{title: "Download failed", body: "nope"}}, "")
	if single != "Download failed" {
		t.Fatalf("a single entry keeps its own title, got %q", single)
	}
	many, _ := compose([]pending{
		{title: "Download failed", body: "a"},
		{title: "Download failed", body: "b"},
		{title: "Download finished", body: "c"},
	}, "")
	if !strings.Contains(many, "3") {
		t.Fatalf("a summary must say how many, got %q", many)
	}
	if many == "Download failed" {
		t.Fatal("a batch must not be titled like one of its entries")
	}
}

// The delay is the one thing about this message that could mislead, so it has
// to be stated in the body.
func TestComposeWarnsThatItMayBeOutOfDate(t *testing.T) {
	_, body := compose([]pending{{title: "Download failed", body: "nope", when: "2026-09-06 10:00:00"}}, "")
	if !strings.Contains(strings.ToLower(body), "summary") || !strings.Contains(strings.ToLower(body), "already") {
		t.Fatalf("the body must say it is a delayed summary, got:\n%s", body)
	}
	if !strings.Contains(body, "10") {
		t.Fatalf("the body should name the interval, got:\n%s", body)
	}
}

// Every entry has to appear, with its own text - a summary that drops entries
// would be worse than the flood it replaces.
func TestComposeListsEveryEntry(t *testing.T) {
	_, body := compose([]pending{
		{title: "First", body: "one"},
		{title: "Second", body: "two"},
	}, "")
	for _, expected := range []string{"First", "one", "Second", "two"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("%q is missing from:\n%s", expected, body)
		}
	}
}
