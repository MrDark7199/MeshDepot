package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"meshdepot/internal/httpx"
	"meshdepot/internal/pwmx"
)

// FilesServePwmxMesh reconstructs a surface mesh from a resin-slicer file and
// returns it as a binary STL, so the Babylon viewer can show an approximate 3D
// view of a file that holds only per-layer exposure images. The reconstruction
// costs seconds and ~19MB, so it is cached under {BASE_PATH_DATA}/pwmx_mesh/.
func (server *Server) FilesServePwmxMesh(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	designID, ok := server.designID(responseWriter, request, "designId")
	if !ok {
		return
	}
	fileID, _ := pathInt(request, "fileId")
	entryID, ok := pathInt(request, "entryId")
	if !ok {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	if _, ok := server.requireDesignAccess(responseWriter, designID, currentUserID); !ok {
		return
	}
	entry, ok := server.requireEntry(responseWriter, entryID, fileID, designID)
	if !ok {
		return
	}
	blobPath := entry.absPath(server.layout())
	if !strings.HasSuffix(strings.ToLower(entry.Filename), ".pwmx") || !fileExists(blobPath) {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.unsupported")
		return
	}

	// The result is deterministic per file and options, and the options in the name
	// invalidate the cache when they change.
	options := pwmx.DefaultMeshOptions()
	hash := entry.BlobHash
	if hash == "" {
		hash = entry.FileHash
	}
	cacheDir := filepath.Join(server.layout().Root(), "pwmx_mesh")
	cachePath := filepath.Join(cacheDir, fmt.Sprintf("%s-mc-xy%dz%dt%dm%d.stl",
		hash, options.XYStep, options.ZStep, options.Threshold, int(options.MinFill*100)))
	if hash != "" && fileExists(cachePath) {
		serveInline(responseWriter, cachePath, "model.stl")
		return
	}

	data, failure := os.ReadFile(blobPath)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusNotFound, "error.not_found")
		return
	}
	file, failure := pwmx.Parse(data)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.unsupported")
		return
	}
	stl, _, failure := file.ReconstructSTLMarchingCubes(options)
	if failure != nil {
		httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
		return
	}
	if hash != "" {
		writeCacheFile(cacheDir, cachePath, stl)
	}
	responseWriter.Header().Set("Content-Type", "application/octet-stream")
	responseWriter.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = responseWriter.Write(stl)
}

// writeCacheFile renames a temp file into place: os.WriteFile publishes the path
// the moment it creates the file, so a parallel request would find it "existing"
// and stream a truncated STL that then stays cached forever.
func writeCacheFile(cacheDir, cachePath string, data []byte) {
	if failure := os.MkdirAll(cacheDir, 0o775); failure != nil {
		return
	}
	tempFile, failure := os.CreateTemp(cacheDir, filepath.Base(cachePath)+".tmp*")
	if failure != nil {
		return
	}
	tempPath := tempFile.Name()
	_, writeFailure := tempFile.Write(data)
	closeFailure := tempFile.Close()
	if writeFailure != nil || closeFailure != nil {
		_ = os.Remove(tempPath)
		return
	}
	if os.Chmod(tempPath, 0o644) != nil || os.Rename(tempPath, cachePath) != nil {
		_ = os.Remove(tempPath)
	}
}
