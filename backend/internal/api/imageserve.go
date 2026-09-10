package api

import (
	"io"
	"net/http"
	"os"
	"strings"
)

// imageContentTypes is the allowlist of types we hand back from user-controlled
// storage. Everything under covers and avatars comes from a remote platform or an
// upload, and left to http.ServeFile the type is guessed from the extension - so a
// stored "cover_1.html" or ".svg" would come back as text/html and execute script
// on this origin, with the viewer's session in reach. nosniff stops the browser
// second-guessing us.
var imageContentTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// serveImageFile derives the Content-Type from the file's own first bytes and
// checks it against the allowlist; anything else is a 404 rather than rendered.
// Callers set their own Cache-Control first.
func serveImageFile(responseWriter http.ResponseWriter, request *http.Request, fullPath string) {
	file, failure := os.Open(fullPath)
	if failure != nil {
		http.NotFound(responseWriter, request)
		return
	}
	defer file.Close()
	fileInfo, failure := file.Stat()
	if failure != nil || fileInfo.IsDir() {
		http.NotFound(responseWriter, request)
		return
	}
	contentType, ok := sniffImage(file)
	if !ok {
		http.NotFound(responseWriter, request)
		return
	}
	if _, failure := file.Seek(0, io.SeekStart); failure != nil {
		http.NotFound(responseWriter, request)
		return
	}
	responseWriter.Header().Set("Content-Type", contentType)
	responseWriter.Header().Set("X-Content-Type-Options", "nosniff")
	// Empty name: ServeContent must not re-guess the type from the extension.
	http.ServeContent(responseWriter, request, "", fileInfo.ModTime(), file)
}

// sniffImage reports the allowlisted type of the content, or false.
func sniffImage(file *os.File) (string, bool) {
	head := make([]byte, 512)
	readCount, _ := io.ReadFull(file, head)
	contentType := http.DetectContentType(head[:readCount])
	if index := strings.IndexByte(contentType, ';'); index >= 0 {
		contentType = strings.TrimSpace(contentType[:index])
	}
	contentType = strings.ToLower(contentType)
	return contentType, imageContentTypes[contentType]
}
