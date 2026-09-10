// Package httpx holds the HTTP response conventions: success
// {success,message,data}, error {error}.
package httpx

import (
	"encoding/json"
	"net/http"
)

type Envelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// internalKeys never reach a client: foreign keys and secrets that a SELECT *
// carries along by construction. The bodies are built from maps so a new column
// reaches the frontend without a code change, which is why the removal happens on
// the way out rather than as a column list per query.
//
// The numeric user id in particular has to go - it is the number the public id
// exists to hide. "id" is deliberately absent: tag, collection and file ids are
// legitimately numeric.
var internalKeys = map[string]bool{
	"user_id":             true,
	"owner_user_id":       true,
	"shared_with_user_id": true,
	"hash":                true,
	"totp_secret":         true,
	"avatar_path":         true,
}

// scrubDepth bounds the walk: response bodies are a handful of levels deep.
const scrubDepth = 12

// JSON writes any value as JSON without the internal columns.
func JSON(responseWriter http.ResponseWriter, status int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(status)
	_ = json.NewEncoder(responseWriter).Encode(scrub(value, 0))
}

// scrub cleans the maps and slices a handler assembled. Typed payloads pass
// through: they list their fields explicitly.
func scrub(value any, depth int) any {
	if depth > scrubDepth {
		return value
	}
	switch typed := value.(type) {
	case Envelope:
		typed.Data = scrub(typed.Data, depth+1)
		return typed
	case map[string]any:
		promotePublicID(typed)
		for key := range typed {
			if internalKeys[key] {
				delete(typed, key)
				continue
			}
			typed[key] = scrub(typed[key], depth+1)
		}
		return typed
	case []map[string]any:
		for index := range typed {
			scrub(typed[index], depth+1)
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = scrub(typed[index], depth+1)
		}
		return typed
	}
	return value
}

// promotePublicID puts a row's public id in place of its rowid - here rather than
// in the handlers, because the bodies are built from SELECT * and a handler that
// forgets would hand out the sequential number. Rows with no public_id keep their
// numeric id.
func promotePublicID(row map[string]any) {
	publicID, present := row["public_id"]
	if !present {
		return
	}
	delete(row, "public_id")
	// An empty value would replace a working id with nothing.
	if text, ok := publicID.(string); ok && text != "" {
		row["id"] = text
	}
}

func Success(responseWriter http.ResponseWriter, data any) {
	JSON(responseWriter, http.StatusOK, Envelope{Success: true, Message: "OK", Data: data})
}

func SuccessMessage(responseWriter http.ResponseWriter, data any, message string) {
	JSON(responseWriter, http.StatusOK, Envelope{Success: true, Message: message, Data: data})
}

func SuccessStatus(responseWriter http.ResponseWriter, status int, data any, message string) {
	JSON(responseWriter, status, Envelope{Success: true, Message: message, Data: data})
}

// Error writes the {error:"..."} envelope. The message is an i18n key
// ("error.not_found"), optionally followed by ":" and one parameter - a design
// name, a platform key, or a ready-made English sentence. The frontend splits at
// the first colon, translates the front and passes the rest as {platform}.
// TestErrorKeysAreTranslated enforces that every key exists in both dictionaries.
func Error(responseWriter http.ResponseWriter, status int, message string) {
	JSON(responseWriter, status, map[string]string{"error": message})
}
