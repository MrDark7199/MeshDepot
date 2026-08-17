package httpx

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	goErrorKey   = regexp.MustCompile(`"(error\.[a-z0-9_]+)`)
	dictErrorKey = regexp.MustCompile(`'(error\.[a-z0-9_]+)'\s*:`)
)

// TestErrorKeysAreTranslated pins the error convention described on Error: every
// key the backend hands to the frontend must exist in every dictionary.
//
// Without this the three styles that used to coexist (i18n key, plain English,
// "key:payload") were indistinguishable at the call site, and a key with no
// dictionary entry was invisible until it appeared on a user's screen as the
// literal string "error.sync_timeout" - which is exactly what happened to a
// dozen of them.
func TestErrorKeysAreTranslated(t *testing.T) {
	dictionaries := map[string]map[string]bool{}
	for _, language := range []string{"en", "de"} {
		path := filepath.Join("..", "..", "..", "frontend", "src", "i18n", language+".ts")
		content, failure := os.ReadFile(path)
		if failure != nil {
			t.Skipf("dictionary %s unreadable (%v) - needs a full checkout", path, failure)
		}
		keys := map[string]bool{}
		for _, match := range dictErrorKey.FindAllStringSubmatch(string(content), -1) {
			keys[match[1]] = true
		}
		dictionaries[language] = keys
	}

	// Where each key is emitted, so a failure names a file instead of a key.
	origin := map[string]string{}
	root := filepath.Join("..", "..")
	failure := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		content, readFailure := os.ReadFile(path)
		if readFailure != nil {
			return readFailure
		}
		for _, match := range goErrorKey.FindAllStringSubmatch(string(content), -1) {
			if _, seen := origin[match[1]]; !seen {
				origin[match[1]] = path
			}
		}
		return nil
	})
	if failure != nil {
		t.Fatalf("walking %s: %v", root, failure)
	}
	if len(origin) == 0 {
		t.Fatal("no error keys found - the source scan is broken, not the code")
	}

	keys := make([]string, 0, len(origin))
	for key := range origin {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		for _, language := range []string{"en", "de"} {
			if !dictionaries[language][key] {
				t.Errorf("%s: %q has no entry in frontend/src/i18n/%s.ts - users would see the raw key",
					origin[key], key, language)
			}
		}
	}
}
