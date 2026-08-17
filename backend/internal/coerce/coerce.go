// Package coerce converts the dynamically typed values that come out of
// database/sql (`map[string]any` rows) and out of JSON APIs into Go scalars.
//
// It exists because every consumer used to carry its own copy: `asInt` in
// api/helpers.go handled strings but not json.Number, `asInt` in
// platforms/library.go handled json.Number but not strings, and `toInt64` in
// api/tags.go handled neither. Same names, different behavior, so a value that
// parsed in one handler silently became 0 in the next. Each conversion is
// defined exactly once here.
package coerce

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Int converts any numeric representation - including numeric strings and
// json.Number - into an int. Anything else yields 0.
func Int(value any) int {
	number, _ := Int64(value)
	return int(number)
}

// Int64 converts any numeric representation into an int64. The second return
// value reports whether the conversion succeeded, so callers can tell a real 0
// apart from "not a number".
func Int64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case json.Number:
		number, failure := typed.Int64()
		return number, failure == nil
	case string:
		number, failure := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return number, failure == nil
	case []byte:
		number, failure := strconv.ParseInt(strings.TrimSpace(string(typed)), 10, 64)
		return number, failure == nil
	}
	return 0, false
}

// Text returns a string value trimmed of surrounding whitespace. Values of any
// other type yield "" - used for JSON fields where a number in a text field
// means the response does not have the expected shape.
func Text(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	}
	return ""
}

// NumberText renders a JSON id that may arrive as either a number or a string.
// Non-numeric, non-string values yield "".
func NumberText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	}
	if number, ok := Int64(value); ok {
		return strconv.FormatInt(number, 10)
	}
	return ""
}

// StringOr renders a database column as a string and falls back to
// defaultValue for NULL and empty strings.
func StringOr(value any, defaultValue string) string {
	switch text := value.(type) {
	case nil:
		return defaultValue
	case string:
		if text == "" {
			return defaultValue
		}
		return text
	case []byte:
		if len(text) == 0 {
			return defaultValue
		}
		return string(text)
	default:
		return fmt.Sprintf("%v", value)
	}
}
