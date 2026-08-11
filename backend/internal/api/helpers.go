package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
)

// fetchRow reads a single row and answers the request itself if that is not
// possible: a failed query becomes a 500, a missing row a 404. ok=false means a
// response has already been written and the handler must return.
//
// The point is to keep the two apart at every call site - see dbutil.QueryMap.
func (server *Server) fetchRow(responseWriter http.ResponseWriter, query string, args ...any) (map[string]any, bool) {
	row, found, failure := dbutil.QueryMap(server.DB, query, args...)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return nil, false
	}
	if !found {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return nil, false
	}
	return row, true
}

// optionalRow reads a row whose absence is normal (an existence probe, a name
// for a notification text). A query failure is logged and reported as "not
// there", because there is nothing better the caller could do with it - but it
// no longer disappears without a trace.
func (server *Server) optionalRow(context, query string, args ...any) (map[string]any, bool) {
	row, found, failure := dbutil.QueryMap(server.DB, query, args...)
	if failure != nil {
		log.Printf("[api] %s: query failed: %v", context, failure)
		return nil, false
	}
	return row, found
}

// likePattern builds a "contains" pattern for LIKE and neutralizes the wildcards
// % and _ (and the escape character itself) in the user input - otherwise a
// search for "50%" matches everything. Every query using it must end its LIKE
// with ESCAPE '\'.
func likePattern(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(value)
	return "%" + escaped + "%"
}

// nullStr turns an empty string into SQL NULL and passes everything else
// through unchanged.
//
// It used to return the value as-is despite its name, so every caller (designs.go)
// wrote ” instead of NULL - which is what makes the "IS NOT NULL AND != ”"
// double conditions in users.go and scheduler.go necessary in the first place.
// The identically named helper in platforms/save.go always did it correctly.
func nullStr(value any) any {
	if value == nil {
		return nil
	}
	if text, isString := value.(string); isString && strings.TrimSpace(text) == "" {
		return nil
	}
	return value
}

// nullInt maps nil and the empty string to NULL and everything else to an int.
func nullInt(value any) any {
	switch number := value.(type) {
	case nil:
		return nil
	case string:
		if number == "" {
			return nil
		}
		parsed, _ := strconv.Atoi(number)
		return parsed
	case float64:
		return int(number)
	case int:
		return number
	case bool:
		if number {
			return 1
		}
		return 0
	default:
		return nil
	}
}

// sortSlice sorts rows stably by the given comparison.
func sortSlice(rows []map[string]any, less func(first, second map[string]any) bool) {
	sort.SliceStable(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
}

// jsonPath navigates JSON via a dot path ("0.sizes.0.url") and returns the
// string at the end (or "" if not present/not a string).
func jsonPath(body, path string) string {
	var root any
	if json.Unmarshal([]byte(body), &root) != nil {
		return ""
	}
	current := root
	for _, segment := range strings.Split(path, ".") {
		if index, failure := strconv.Atoi(segment); failure == nil {
			array, ok := current.([]any)
			if !ok || index < 0 || index >= len(array) {
				return ""
			}
			current = array[index]
			continue
		}
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = object[segment]
		if !ok {
			return ""
		}
	}
	if text, ok := current.(string); ok {
		return text
	}
	return ""
}
