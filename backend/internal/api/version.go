package api

// What this server speaks, for clients that are updated separately from it.

import (
	"net/http"

	"meshdepot/internal/httpx"
)

// Version reports the contracts a client can hold this server to.
//
// One route rather than one per feature: a client asking "can I talk to this
// server?" is asking a question about the server, not about the endpoint it
// happens to want. Putting the answer under the browser import made it look like
// a property of importing, and the next client would have needed a second one
// just like it.
//
// Deliberately public. A client that cannot authenticate - no key yet, or a key
// this server never issued - still has to be able to tell "your MeshDepot is too
// old" from "wrong key", and a version number is not a secret.
func (server *Server) Version(responseWriter http.ResponseWriter, _ *http.Request) {
	httpx.Success(responseWriter, map[string]any{
		// Each entry is a range: the oldest contract still accepted, and the
		// newest spoken. A client is compatible when its own number falls inside.
		"browser_import": map[string]any{
			"version":     BrowserImportAPIVersion,
			"min_version": BrowserImportAPIMinVersion,
		},
	})
}
