package api

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestOpenAPIMatchesRouter guards docs/openapi.yaml against drift: every route
// registered in router.go must be documented and vice versa. Without this the
// spec silently rots - 10 endpoints (~11 %) were missing and two removed SSE
// routes were still documented when this check was added.
//
// Both files are parsed as text (no YAML dependency, no running server): the
// router lines look like `mux.Handle("GET /api/v1/tags", …)`, and the spec's
// path items are the only two-space-indented `/…:` keys inside `paths:`.
func TestOpenAPIMatchesRouter(t *testing.T) {
	const specPath = "../../../docs/openapi.yaml"
	if _, failure := os.Stat(specPath); os.IsNotExist(failure) {
		t.Skip("docs/openapi.yaml not reachable - run the tests from a full checkout (./startCodeTest.sh)")
	}
	routes := parseRouterOperations(t, "router.go")
	documented := parseSpecOperations(t, specPath)

	if len(routes) == 0 || len(documented) == 0 {
		t.Fatalf("parsed nothing: %d routes, %d documented operations", len(routes), len(documented))
	}
	for _, operation := range difference(routes, documented) {
		t.Errorf("route is not documented in docs/openapi.yaml: %s", operation)
	}
	for _, operation := range difference(documented, routes) {
		t.Errorf("documented in docs/openapi.yaml but no such route: %s", operation)
	}
}

var (
	routePattern = regexp.MustCompile(`"(GET|POST|PUT|DELETE|PATCH) (/api/v1[^"]*)"`)
	pathPattern  = regexp.MustCompile(`^ {2}(/\S*):\s*$`)
	verbPattern  = regexp.MustCompile(`^ {4}(get|post|put|delete|patch):\s*$`)
	paramPattern = regexp.MustCompile(`\{[^}]+\}`)
)

// normalizeOperation renders one operation as "METHOD /path" with the API
// prefix stripped and path parameters anonymized, so that `{id}` in the spec
// and `{designId}` in the router compare equal.
func normalizeOperation(method, path string) string {
	path = strings.TrimPrefix(path, "/api/v1")
	return strings.ToUpper(method) + " " + paramPattern.ReplaceAllString(path, "{}")
}

func parseRouterOperations(t *testing.T, filename string) map[string]bool {
	t.Helper()
	source, failure := os.ReadFile(filename)
	if failure != nil {
		t.Fatalf("read %s: %v", filename, failure)
	}
	operations := map[string]bool{}
	for _, match := range routePattern.FindAllStringSubmatch(string(source), -1) {
		operations[normalizeOperation(match[1], match[2])] = true
	}
	return operations
}

func parseSpecOperations(t *testing.T, filename string) map[string]bool {
	t.Helper()
	source, failure := os.ReadFile(filename)
	if failure != nil {
		t.Fatalf("read %s: %v", filename, failure)
	}
	operations := map[string]bool{}
	currentPath, insidePaths := "", false
	for _, line := range strings.Split(string(source), "\n") {
		if strings.HasPrefix(line, "paths:") {
			insidePaths = true
			continue
		}
		// Any other top-level key ends the paths section.
		if insidePaths && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") {
			insidePaths = false
		}
		if !insidePaths {
			continue
		}
		if match := pathPattern.FindStringSubmatch(line); match != nil {
			currentPath = match[1]
			continue
		}
		if match := verbPattern.FindStringSubmatch(line); match != nil && currentPath != "" {
			operations[normalizeOperation(match[1], currentPath)] = true
		}
	}
	return operations
}

// difference returns the sorted keys present in a but not in b.
func difference(a, b map[string]bool) []string {
	var only []string
	for key := range a {
		if !b[key] {
			only = append(only, key)
		}
	}
	sort.Strings(only)
	return only
}

// The drift check above compares methods and paths, so it stays green when a
// parameter changes type. These two facts are the ones that silently rot: the
// account routes take the public id, and no schema hands out an internal
// foreign key any more.
func TestOpenAPIDescribesAccountsByTheirPublicID(t *testing.T) {
	specification, failure := os.ReadFile("../../../docs/openapi.yaml")
	if failure != nil {
		t.Skip("docs/openapi.yaml not reachable - run the tests from a full checkout (./startCodeTest.sh)")
	}
	text := string(specification)

	for _, forbidden := range []string{"user_id:", "shared_with_user_id:"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the specification still documents %s, which the response envelope removes", forbidden)
		}
	}
	// Every /users/{id} and /admin/users/{id} parameter has to be a string.
	inAccountRoute := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "  /") {
			inAccountRoute = strings.HasPrefix(line, "  /users/{id}") || strings.HasPrefix(line, "  /admin/users/{id}")
		}
		if inAccountRoute && strings.Contains(line, "name: id, in: path") && strings.Contains(line, "type: integer") {
			t.Errorf("an account route still takes a numeric id: %s", strings.TrimSpace(line))
		}
	}
}
