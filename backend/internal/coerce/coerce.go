// Package coerce converts the dynamically typed values from database/sql rows and
// JSON APIs into Go scalars.
//
// Every consumer used to carry its own copy: asInt in api/helpers.go handled
// strings but not json.Number, asInt in platforms/library.go the reverse, and
// toInt64 in api/tags.go neither - so a value that parsed in one handler silently
// became 0 in the next.
package coerce

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Int converts any numeric representation, numeric strings and json.Number
// included. Anything else yields 0.
func Int(value any) int {
	number, _ := Int64(value)
	return int(number)
}

// Int64 reports whether the conversion succeeded, so callers can tell a real 0
// from "not a number".
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

// Text trims surrounding whitespace; any other type yields "", which is how a
// number in a text field says the response has the wrong shape.
func Text(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	}
	return ""
}

// NumberText renders a JSON id that may arrive as a number or a string.
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

// StringOr falls back to defaultValue for NULL and empty strings.
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
