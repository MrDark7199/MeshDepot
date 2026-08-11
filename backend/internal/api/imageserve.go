package api

import (
	"io"
	"net/http"
	"os"
	"strings"
)

// imageContentTypes is the allowlist of types we are willing to hand back from
// user-controlled storage (covers, gallery images, avatars).
//
// Everything under /data/covers and /data/avatars ultimately comes from a
// remote platform or an upload, so the file's own bytes decide nothing about
// whether it is safe to render - but the Content-Type we answer with does. Left
// to http.ServeFile the type is guessed from the extension, so a stored
// "cover_1.html" or ".svg" would come back as text/html or image/svg+xml and
// execute script on this origin, with the viewer's session cookie in reach.
// Serving only these four types, and only when the bytes agree, closes that off;
// nosniff stops the browser from second-guessing us.
var imageContentTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// serveImageFile streams an image whose Content-Type is derived from its own
// first bytes and checked against imageContentTypes. Anything else - HTML, SVG,
// a text file with an image extension - is answered with 404 rather than being
// rendered. Callers set their own Cache-Control before calling.
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

// sniffImage reads the detection window and reports the allowlisted type of the
// content, or false if it is not one we serve.
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
