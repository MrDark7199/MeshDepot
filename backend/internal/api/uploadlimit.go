package api

import (
	"errors"
	"net/http"

	"meshdepot/internal/httpx"
)

// ParseMultipartForm's argument only caps how much of the form is buffered in
// memory - everything beyond it Go streams to disk, so a single POST could fill
// /data until the container dies. MaxBytesReader is what actually terminates an
// oversized request.
const (
	maxAvatarUpload = 8 << 20  // 8 MiB - profile pictures.
	maxImageUpload  = 32 << 20 // 32 MiB - design gallery images.
	maxModelUpload  = 2 << 30  // 2 GiB - model/G-code archives; generous on purpose.

	// maxZipEntryBytes bounds one extracted entry: the upload limit covers only the
	// compressed bytes, and a 1 MiB zip can declare gigabytes.
	maxZipEntryBytes = 512 << 20 // 512 MiB
)

// limitRequestBody must be called before anything reads the body; the limit takes
// effect at that read and surfaces there as an error - pass it to uploadError.
func limitRequestBody(responseWriter http.ResponseWriter, request *http.Request, limit int64) {
	request.Body = http.MaxBytesReader(responseWriter, request.Body, limit)
}

// bodyTooLarge reports whether failure came from the MaxBytesReader limit.
func bodyTooLarge(failure error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(failure, &tooLarge)
}

// uploadError answers 413 when the body exceeded the limit, otherwise the
// handler's own message.
func uploadError(responseWriter http.ResponseWriter, failure error, message string) {
	if bodyTooLarge(failure) {
		httpx.Error(responseWriter, http.StatusRequestEntityTooLarge, "error.upload_too_large")
		return
	}
	httpx.Error(responseWriter, http.StatusUnprocessableEntity, message)
}
