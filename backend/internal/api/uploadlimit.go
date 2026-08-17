package api

import (
	"errors"
	"net/http"

	"meshdepot/internal/httpx"
)

// Upload ceilings. ParseMultipartForm's argument only caps how much of the form
// is buffered in memory - everything beyond it Go streams to temporary files on
// disk, so on its own it bounds nothing an attacker cares about. A single
// unauthenticated-looking POST could fill /data until the container dies.
// http.MaxBytesReader is what actually terminates an oversized request.
const (
	maxAvatarUpload = 8 << 20  // 8 MiB - profile pictures.
	maxImageUpload  = 32 << 20 // 32 MiB - design gallery images.
	maxModelUpload  = 2 << 30  // 2 GiB - model/G-code archives; generous on purpose.

	// maxZipEntryBytes bounds a single entry extracted from an uploaded archive.
	// The upload limit only covers the compressed bytes; a 1 MiB zip can declare
	// gigabytes of content.
	maxZipEntryBytes = 512 << 20 // 512 MiB
)

// limitRequestBody caps the request body. It must be called before any method
// that reads the body (ParseMultipartForm, FormFile, FormValue); the limit takes
// effect when that read happens, and surfaces there as an error - pass it to
// uploadError to turn it into a 413.
func limitRequestBody(responseWriter http.ResponseWriter, request *http.Request, limit int64) {
	request.Body = http.MaxBytesReader(responseWriter, request.Body, limit)
}

// bodyTooLarge reports whether failure came from the MaxBytesReader limit, so the
// handler can answer 413 instead of a generic "upload failed".
func bodyTooLarge(failure error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(failure, &tooLarge)
}

// uploadError writes the right status for a failed multipart parse: 413 when the
// body exceeded the limit, otherwise the handler's own message.
func uploadError(responseWriter http.ResponseWriter, failure error, message string) {
	if bodyTooLarge(failure) {
		httpx.Error(responseWriter, http.StatusRequestEntityTooLarge, "error.upload_too_large")
		return
	}
	httpx.Error(responseWriter, http.StatusUnprocessableEntity, message)
}
