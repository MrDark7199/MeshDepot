package storage

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// testHash stands in for a public id; the layout never interprets it, but the
// tests read better when it looks like the real thing.
const testHash = "a3f1c8e94b2d7f0a5c6e1b8d4a9f2c37"

func TestLayoutBuildsTheDocumentedPaths(t *testing.T) {
	user := New("/data/").User(testHash)

	cases := []struct{ got, want string }{
		{New("/data/").Root(), "/data"},
		{user.Root(), "/data/user/" + testHash},
		{user.Design(42), "/data/user/" + testHash + "/design/42"},
		{user.Version(42, "2.0"), "/data/user/" + testHash + "/design/42/version/2.0"},
		{user.Pictures(42), "/data/user/" + testHash + "/design/42/pictures"},
		{user.Cover(42, "png"), "/data/user/" + testHash + "/design/42/cover.png"},
		{user.Avatar(), "/data/user/" + testHash + "/account/avatar"},
		{user.Temp(), "/data/user/" + testHash + "/tmp"},
		{user.Blob("abcdef0123456789"), "/data/user/" + testHash + "/blobs/ab/cd/abcdef0123456789"},
	}
	for _, testCase := range cases {
		if testCase.got != testCase.want {
			t.Errorf("got %q, want %q", testCase.got, testCase.want)
		}
	}
}

func TestEverythingOfAnAccountSitsUnderOneDirectory(t *testing.T) {
	user := New("/data").User(testHash)

	paths := map[string]string{
		"design":   user.Design(42),
		"version":  user.Version(42, "1.0"),
		"pictures": user.Pictures(42),
		"cover":    user.Cover(42, "jpg"),
		"avatar":   user.Avatar(),
		"temp":     user.Temp(),
		"blob":     user.Blob("abcdef0123456789"),
	}
	for name, path := range paths {
		if !strings.HasPrefix(path, user.Root()+"/") {
			t.Errorf("%s lies outside the account directory: %s", name, path)
		}
	}
}

// Deleting one account must not be able to touch another's files.
func TestPathsOfTwoAccountsNeverOverlap(t *testing.T) {
	layout := New("/data")
	first := layout.User(testHash)
	second := layout.User("0000000000000000000000000000ffff")

	if strings.HasPrefix(second.Root(), first.Root()+"/") || strings.HasPrefix(first.Root(), second.Root()+"/") {
		t.Fatalf("one account directory contains the other: %s / %s", first.Root(), second.Root())
	}
	if first.Blob("abcdef0123456789") == second.Blob("abcdef0123456789") {
		t.Fatal("both accounts share one blob path")
	}
}

// The public image endpoint only serves a cover whose name matches this shape,
// so a cover stored under any other name exists but can never be requested.
func TestCoverAlwaysMatchesTheServableShape(t *testing.T) {
	servable := regexp.MustCompile(`^cover\.[A-Za-z0-9]{1,5}$`)
	user := New("/data").User(testHash)

	inputs := map[string]string{
		"png":              "cover.png",
		".PNG":             "cover.png",
		"  jpeg  ":         "cover.jpeg",
		"":                 "cover.jpg",
		"verylongension":   "cover.jpg",
		"tar.gz":           "cover.jpg", // the dot would add a path segment
		"pn g":             "cover.jpg",
		"../../etc/passwd": "cover.jpg",
	}
	for input, want := range inputs {
		got := filepath.Base(user.Cover(42, input))
		if got != want {
			t.Errorf("Cover(%q) produced %q, want %q", input, got, want)
		}
		if !servable.MatchString(got) {
			t.Errorf("Cover(%q) produced the unservable name %q", input, got)
		}
	}
}

// A hash too short to split would otherwise panic on the slice bounds.
func TestBlobRejectsAnUnusableHash(t *testing.T) {
	if path := New("/data").User(testHash).Blob("ab"); path != "" {
		t.Fatalf("a 2-character hash produced %q", path)
	}
}

func TestRelAndAbsAreInverse(t *testing.T) {
	layout := New("/data")
	user := layout.User(testHash)
	absolute := filepath.Join(user.Version(42, "1.0"), "part.stl")

	relative := layout.Rel(absolute)
	if relative != "user/"+testHash+"/design/42/version/1.0/part.stl" {
		t.Fatalf("Rel produced %q", relative)
	}
	if back := layout.Abs(relative); back != absolute {
		t.Fatalf("Abs(Rel(x)) produced %q, want %q", back, absolute)
	}
	// The account layout answers the same, so a caller holding only that value
	// does not need the plain layout as well.
	if user.Rel(absolute) != relative || user.Abs(relative) != absolute {
		t.Fatal("the account layout resolves differently from the data layout")
	}
}

// Rows written before paths became relative hold an absolute path; Abs has to
// pass those through instead of prefixing the root a second time.
func TestAbsPassesAbsoluteInputThrough(t *testing.T) {
	layout := New("/data")
	if got := layout.Abs("/data/user/x/design/42"); got != "/data/user/x/design/42" {
		t.Fatalf("an absolute path was rewritten to %q", got)
	}
	if got := layout.Abs(""); got != "" {
		t.Fatalf("an empty path produced %q", got)
	}
}

// Rel must not turn a path outside the root into a "../.." escape that Abs
// would later resolve back out of the data directory.
func TestRelKeepsPathsOutsideTheRootAbsolute(t *testing.T) {
	if got := New("/data").Rel("/etc/passwd"); got != "/etc/passwd" {
		t.Fatalf("a path outside the root became %q", got)
	}
}

func TestContainsRejectsTraversal(t *testing.T) {
	layout := New("/data")
	cases := map[string]bool{
		"/data":                             true,
		"/data/user/" + testHash + "/x.stl": true,
		"/data/../etc/passwd":               false,
		"/etc/passwd":                       false,
		"/database/other":                   false, // prefix match without a separator must not pass
	}
	for path, want := range cases {
		if got := layout.Contains(path); got != want {
			t.Errorf("Contains(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestWriteFileCreatesMissingDirectories(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(New(root).User(testHash).Pictures(42), "cover.jpg")

	if failure := WriteFile(target, []byte("bytes")); failure != nil {
		t.Fatalf("WriteFile: %v", failure)
	}
	if data, failure := os.ReadFile(target); failure != nil || string(data) != "bytes" {
		t.Fatalf("file not written: %v", failure)
	}
}
