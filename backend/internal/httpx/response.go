// Package httpx contains the HTTP response conventions (envelope, errors) used
// by the PHP backend: success {success,message,data}, error {error}.
package httpx

import (
	"encoding/json"
	"net/http"
)

// Envelope is the standard success response of the backend.
type Envelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// internalKeys never reach a client. They are foreign keys and secrets that a
// SELECT * or SELECT d.* carries along by construction: the response bodies are
// deliberately built from maps so a new column reaches the frontend without a
// code change (see internal/api/rows.go), which is exactly why the removal has
// to happen on the way out rather than as a column list per query.
//
// The numeric user id in particular has to go: the API addresses accounts by
// their public id, and leaving user_id in every design and collection would
// hand out the very number that addressing is meant to hide.
//
// "id" is deliberately absent - tag, collection and file ids are legitimately
// numeric. Accounts and designs carry a public id instead, which the promotion
// below puts in place of the rowid.
var internalKeys = map[string]bool{
	"user_id":             true,
	"owner_user_id":       true,
	"shared_with_user_id": true,
	"hash":                true,
	"totp_secret":         true,
	"avatar_path":         true,
}

// scrubDepth bounds the walk below. Response bodies are a handful of levels
// deep; anything beyond this is a structure that cannot have come from a query.
const scrubDepth = 12

// JSON writes an arbitrary value as JSON with a status code, without the
// internal columns.
func JSON(responseWriter http.ResponseWriter, status int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(status)
	_ = json.NewEncoder(responseWriter).Encode(scrub(value, 0))
}

// scrub removes the internal keys from the maps and slices a handler assembled.
// Typed payloads pass through untouched: they list their fields explicitly, so
// nothing can travel along unnoticed.
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

// promotePublicID puts a row's public id in place of its rowid.
//
// Here rather than in the handlers for the same reason the removals above are:
// the bodies are built from `SELECT *`, so both columns travel by construction,
// and a handler that forgets to swap them would hand out the sequential number
// the public id exists to replace. Rows that carry no public_id - tags,
// collections, files - keep their numeric id untouched.
func promotePublicID(row map[string]any) {
	publicID, present := row["public_id"]
	if !present {
		return
	}
	delete(row, "public_id")
	// An empty value would replace a working id with nothing. It cannot happen
	// once the backfill has run, but a response is the wrong place to find out.
	if text, ok := publicID.(string); ok && text != "" {
		row["id"] = text
	}
}

// Success writes the {success:true,message,data} envelope.
func Success(responseWriter http.ResponseWriter, data any) {
	JSON(responseWriter, http.StatusOK, Envelope{Success: true, Message: "OK", Data: data})
}

// SuccessMessage writes the success envelope with a custom message.
func SuccessMessage(responseWriter http.ResponseWriter, data any, message string) {
	JSON(responseWriter, http.StatusOK, Envelope{Success: true, Message: message, Data: data})
}

// SuccessStatus writes the success envelope with a message and status code (e.g. 201).
func SuccessStatus(responseWriter http.ResponseWriter, status int, data any, message string) {
	JSON(responseWriter, status, Envelope{Success: true, Message: message, Data: data})
}

// Error writes the {error:"..."} envelope with the given status code.
//
// The message is an i18n key ("error.not_found"), optionally followed by ":" and
// one parameter - a design name, a platform key, or a ready-made English
// sentence for cases the dictionaries do not cover. The frontend splits at the
// first colon, translates the part in front of it and passes the rest in as
// {platform}; an untranslatable key falls through to the parameter, and a key
// without one ends up on screen verbatim. Plain English messages without a key
// are therefore not an option - TestErrorKeysAreTranslated enforces that every
// key emitted anywhere in this module exists in both dictionaries.
func Error(responseWriter http.ResponseWriter, status int, message string) {
	JSON(responseWriter, status, map[string]string{"error": message})
}
