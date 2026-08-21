package api

import (
	"net/http"
	"time"

	"meshdepot/internal/httpx"
	"meshdepot/internal/queuestate"
)

// QueueBlocks lists the platforms whose download queue is currently paused or
// auto-blocked. Available to every logged-in user so the UI can show an alert;
// it exposes only platform names and timing, no per-user data.
func (server *Server) QueueBlocks(responseWriter http.ResponseWriter, request *http.Request) {
	httpx.Success(responseWriter, queuestate.Active(server.DB, time.Now()))
}

// QueuePause manually pauses a platform's download queue until an admin resumes
// it. Admin only.
func (server *Server) QueuePause(responseWriter http.ResponseWriter, request *http.Request) {
	platform := request.PathValue("platform")
	if !queuestate.IsPlatform(platform) {
		httpx.Error(responseWriter, http.StatusBadRequest, "error.invalid_platform")
		return
	}
	queuestate.SetPaused(server.DB, platform, true)
	httpx.Success(responseWriter, queuestate.Get(server.DB, platform, time.Now()))
}

// QueueResume clears both the manual pause and any active automatic block of a
// platform - the single "run it again now" action. Admin only.
func (server *Server) QueueResume(responseWriter http.ResponseWriter, request *http.Request) {
	platform := request.PathValue("platform")
	if !queuestate.IsPlatform(platform) {
		httpx.Error(responseWriter, http.StatusBadRequest, "error.invalid_platform")
		return
	}
	queuestate.Resume(server.DB, platform)
	httpx.Success(responseWriter, queuestate.Get(server.DB, platform, time.Now()))
}
