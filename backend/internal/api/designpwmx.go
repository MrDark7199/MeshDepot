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

// FilesServePwmxMesh reconstructs a 3D surface mesh from a resin-slicer file
// (.pwmx, Anycubic Photon Workshop) and returns it as a binary STL - this lets
// the existing Babylon viewer show an (approximate) 3D view of the model even
// though the file itself only contains per-layer exposure images.
//
// The reconstruction is expensive (~seconds, ~19MB), so the result is cached
// under {BASE_PATH_DATA}/pwmx_mesh/<blobHash>.stl and streamed directly on subsequent
// requests.
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

	// Cache path from blob hash + mesh options (the result is deterministic per
	// file + options; the options in the name invalidate the cache on changes).
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

// writeCacheFile puts data at cachePath atomically: written to a unique temp file
// in the same directory, then renamed into place.
//
// os.WriteFile would publish the path the moment it creates the file, so a second
// request reconstructing the same mesh in parallel would find it "existing" and
// stream a truncated STL - which then stays in the cache forever, since nothing
// invalidates it.
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
