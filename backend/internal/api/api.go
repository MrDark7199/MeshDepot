// Package api contains the HTTP router and the controller handlers.
package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"sync"

	"meshdepot/internal/auth"
	"meshdepot/internal/config"
	"meshdepot/internal/crypto"
	"meshdepot/internal/health"
	"meshdepot/internal/platforms"
	"meshdepot/internal/storage"
)

type Server struct {
	DB       *sql.DB
	Auth     *auth.Auth
	Crypto   *crypto.Crypto
	Cfg      config.Config
	Registry platforms.Registry
	Deps     platforms.Deps // for the library sync (Tor/browser/…)
	// Health holds the heartbeats of the background loops. main.go hands the same
	// registry to the workers and the scheduler; one nobody feeds makes AdminHealth
	// report the loops as not running, which is the truth.
	Health         *health.Registry
	downloadTokens *dlTokenStore
	// syncsInFlight holds one entry per running library sync, keyed "userID|platform"
	// - see triggerLibrarySync.
	syncsInFlight sync.Map
}

func New(db *sql.DB, authService *auth.Auth, cryptoHelper *crypto.Crypto, configuration config.Config, registry platforms.Registry, dependencies platforms.Deps) *Server {
	return &Server{DB: db, Auth: authService, Crypto: cryptoHelper, Cfg: configuration, Registry: registry, Deps: dependencies, Health: health.New(), downloadTokens: newDLTokenStore()}
}

func userID(request *http.Request) int {
	id, _ := auth.UserIDFromContext(request.Context())
	return id
}

func (server *Server) layout() storage.Layout {
	return storage.New(server.Cfg.BasePathData)
}

func publicID(request *http.Request) string {
	value, _ := auth.PublicIDFromContext(request.Context())
	return value
}

func (server *Server) userLayout(request *http.Request) storage.UserLayout {
	return server.layout().User(publicID(request))
}

func (server *Server) owner(request *http.Request) platforms.Owner {
	return platforms.Owner{ID: userID(request), Layout: server.userLayout(request)}
}

func pathInt(request *http.Request, name string) (int, bool) {
	value, failure := strconv.Atoi(request.PathValue(name))
	if failure != nil {
		return 0, false
	}
	return value, true
}

func queryStr(request *http.Request, key, defaultValue string) string {
	if value := request.URL.Query().Get(key); value != "" {
		return value
	}
	return defaultValue
}

func queryInt(request *http.Request, key string, defaultValue int) int {
	value, failure := strconv.Atoi(request.URL.Query().Get(key))
	if failure != nil {
		return defaultValue
	}
	return value
}

// queryBool: missing, "0" and "false" are false, anything else is true.
func queryBool(request *http.Request, key string) bool {
	value := request.URL.Query().Get(key)
	return value != "" && value != "0" && value != "false"
}
