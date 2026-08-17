package platforms

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestPlatformTableMatchesExternalLists guards the promise made by the doc
// comment on All: the platform table is the single source of truth, but two
// lists outside this package repeat it and cannot import it - the SQLite CHECK
// constraints in db/schema.sql and the frontend constants in
// frontend/src/constants/platforms.ts. Adding a platform to All without
// touching those two produces rows the database rejects, or a design the UI
// renders without a label and colour, so the drift fails the build here.
func TestPlatformTableMatchesExternalLists(t *testing.T) {
	names := make([]string, 0, len(All))
	labels := map[string]string{}
	needsCredentials := []string{}
	for _, platform := range All {
		names = append(names, platform.Name)
		labels[platform.Name] = platform.Label
		if platform.NeedsCredentials {
			needsCredentials = append(needsCredentials, platform.Name)
		}
	}

	t.Run("schema", func(t *testing.T) {
		schema, ok := readOptional(t, "../db/schema.sql")
		if !ok {
			return
		}
		// designs.source_platform additionally allows 'manual' (self-added designs).
		wantSourcePlatform := append(append([]string{}, names...), "manual")
		compare(t, "designs.source_platform CHECK", wantSourcePlatform, checkConstraintValues(t, schema, "source_platform"))
		compare(t, "platform_accounts.platform CHECK", names, checkConstraintValues(t, schema, "platform"))
	})

	t.Run("frontend", func(t *testing.T) {
		source, ok := readOptional(t, "../../../frontend/src/constants/platforms.ts")
		if !ok {
			return
		}
		// The frontend maps also carry 'manual', which has no downloader.
		withManual := append(append([]string{}, names...), "manual")
		colors := objectLiteral(t, source, "PLATFORM_COLORS")
		compare(t, "PLATFORM_COLORS", withManual, keys(colors))

		frontendLabels := objectLiteral(t, source, "PLATFORM_LABELS")
		compare(t, "PLATFORM_LABELS", withManual, keys(frontendLabels))
		for name, label := range labels {
			if frontendLabels[name] != label {
				t.Errorf("PLATFORM_LABELS[%q] = %q, platform table says %q", name, frontendLabels[name], label)
			}
		}

		compare(t, "PLATFORM_BASES", names, keys(objectLiteral(t, source, "PLATFORM_BASES")))
		compare(t, "PLATFORMS", names, arrayLiteral(t, source, "PLATFORMS"))
		compare(t, "PLATFORMS_REQUIRING_CREDENTIALS", needsCredentials, arrayLiteral(t, source, "PLATFORMS_REQUIRING_CREDENTIALS"))

		// detectPlatformFromUrl pairs each domain with the key it returns; both
		// halves have to match the table.
		detected := map[string]string{}
		for _, match := range detectPattern.FindAllStringSubmatch(source, -1) {
			detected[match[2]] = match[1]
		}
		compare(t, "detectPlatformFromUrl", names, keys(detected))
		for _, platform := range All {
			if detected[platform.Name] != platform.Domain {
				t.Errorf("detectPlatformFromUrl checks %q for %q, platform table says %q", detected[platform.Name], platform.Name, platform.Domain)
			}
		}
	})
}

var (
	detectPattern      = regexp.MustCompile(`url\.includes\('([^']+)'\)\) return '([^']+)'`)
	objectKeyPattern   = regexp.MustCompile(`(?m)^\s+(\w+):\s*'([^']*)',`)
	arrayValuePattern  = regexp.MustCompile(`'([^']*)'`)
	quotedValuePattern = regexp.MustCompile(`'([^']*)'`)
)

// readOptional reads a file outside the Go module. Those are only present in a
// full checkout, so a missing file skips instead of failing (same contract as
// the OpenAPI drift test).
func readOptional(t *testing.T, path string) (string, bool) {
	content, failure := os.ReadFile(path)
	if failure != nil {
		t.Skipf("%s not reachable - run the tests from a full checkout (./startCodeTest.sh)", path)
		return "", false
	}
	return string(content), true
}

// checkConstraintValues returns the quoted values of the first
// `CHECK (<column> IN (...))` constraint in the schema.
func checkConstraintValues(t *testing.T, schema, column string) []string {
	t.Helper()
	start := strings.Index(schema, "CHECK ("+column+" IN (")
	if start < 0 {
		t.Fatalf("no CHECK constraint for column %q in schema.sql", column)
	}
	list := schema[start:]
	end := strings.Index(list, "))")
	if end < 0 {
		t.Fatalf("unterminated CHECK constraint for column %q", column)
	}
	values := []string{}
	for _, match := range quotedValuePattern.FindAllStringSubmatch(list[:end], -1) {
		values = append(values, match[1])
	}
	return values
}

// objectLiteral parses `export const <name> ... = { key: 'value', ... }` into a
// map. Only the flat string-valued literals of platforms.ts are supported.
func objectLiteral(t *testing.T, source, name string) map[string]string {
	t.Helper()
	return parsePairs(t, source, name, "}")
}

// arrayLiteral parses `export const <name> = [ 'a', 'b' ]`.
func arrayLiteral(t *testing.T, source, name string) []string {
	t.Helper()
	body := literalBody(t, source, name, "]")
	values := []string{}
	for _, match := range arrayValuePattern.FindAllStringSubmatch(body, -1) {
		values = append(values, match[1])
	}
	return values
}

func parsePairs(t *testing.T, source, name, terminator string) map[string]string {
	t.Helper()
	pairs := map[string]string{}
	for _, match := range objectKeyPattern.FindAllStringSubmatch(literalBody(t, source, name, terminator), -1) {
		pairs[match[1]] = match[2]
	}
	return pairs
}

func literalBody(t *testing.T, source, name, terminator string) string {
	t.Helper()
	start := strings.Index(source, "export const "+name)
	if start < 0 {
		t.Fatalf("%s not found in platforms.ts", name)
	}
	body := source[start:]
	end := strings.Index(body, "\n"+terminator)
	if end < 0 {
		t.Fatalf("unterminated literal %s in platforms.ts", name)
	}
	return body[:end]
}

func keys(pairs map[string]string) []string {
	result := make([]string, 0, len(pairs))
	for key := range pairs {
		result = append(result, key)
	}
	return result
}

// compare reports missing/extra entries; order is irrelevant in all lists here.
func compare(t *testing.T, what string, want, got []string) {
	t.Helper()
	wantSorted, gotSorted := append([]string{}, want...), append([]string{}, got...)
	sort.Strings(wantSorted)
	sort.Strings(gotSorted)
	if strings.Join(wantSorted, ",") != strings.Join(gotSorted, ",") {
		t.Errorf("%s drifted from the platform table:\n  table: %v\n  list:  %v", what, wantSorted, gotSorted)
	}
}
