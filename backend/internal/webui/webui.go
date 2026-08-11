// Package webui serves the go:embed-embedded Solid.js SPA (frontend/dist)
// including the history-mode fallback to index.html.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the embedded SPA: existing files directly (with matching cache
// headers), otherwise index.html (SPA routing). /api/* is not handled (returns
// 404) so unknown API paths do not serve the SPA.
func Handler() http.Handler {
	sub, failure := fs.Sub(distFS, "dist")
	if failure != nil {
		panic(failure)
	}
	fileServer := http.FileServer(http.FS(sub))
	index := mustRead(sub, "index.html")

	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		clean := strings.TrimPrefix(request.URL.Path, "/")
		if strings.HasPrefix(clean, "api/") {
			http.NotFound(responseWriter, request)
			return
		}
		setSecurityHeaders(responseWriter)
		if clean != "" {
			if file, failure := sub.Open(clean); failure == nil {
				info, statFailure := file.Stat()
				_ = file.Close()
				// Directories would otherwise be listed by http.FileServer.
				if statFailure == nil && !info.IsDir() {
					setCache(responseWriter, clean)
					fileServer.ServeHTTP(responseWriter, request)
					return
				}
			}
		}
		// SPA fallback: index.html, never cache.
		responseWriter.Header().Set("Content-Type", "text/html; charset=utf-8")
		responseWriter.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		_, _ = responseWriter.Write(index)
	})
}

// setSecurityHeaders sends the policy the SPA runs under.
//
// The app stores images fetched from third-party platforms and serves them from
// its own origin, so a single sloppy render is enough to run foreign script here.
// The CSP is the backstop for that. Deliberate choices:
//
//   - script-src 'self': the Vite bundle is the only script; no inline handlers,
//     no CDN. 'wasm-unsafe-eval' is there for Babylon's WASM decoders (draco,
//     basis) - it permits WebAssembly, not eval().
//   - style-src allows 'unsafe-inline' because the UI uses style={{…}} attributes
//     throughout and Babylon injects styles at runtime. Inline style is not a
//     script-execution vector; tightening it would mean rewriting the frontend.
//   - img-src data: for the G-code plate thumbnails decoded in the browser,
//     blob: for canvas snapshots.
//   - frame-ancestors 'none' replaces X-Frame-Options for modern browsers; the
//     old header is still sent for the ones that ignore CSP.
func setSecurityHeaders(responseWriter http.ResponseWriter) {
	header := responseWriter.Header()
	header.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'wasm-unsafe-eval'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"worker-src 'self' blob:",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; "))
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
}

// setCache sets cache headers per asset type (HTML/JS/CSS never, fonts/images 7 days).
func setCache(responseWriter http.ResponseWriter, path string) {
	switch {
	case strings.HasSuffix(path, ".html"), strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".css"):
		responseWriter.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	case strings.HasSuffix(path, ".woff2"), strings.HasSuffix(path, ".woff"),
		strings.HasSuffix(path, ".png"), strings.HasSuffix(path, ".svg"), strings.HasSuffix(path, ".ico"):
		responseWriter.Header().Set("Cache-Control", "public, max-age=604800")
	}
}

// mustRead reads a file from the embedded FS (for index.html).
func mustRead(fsys fs.FS, name string) []byte {
	data, failure := fs.ReadFile(fsys, name)
	if failure != nil {
		panic(failure)
	}
	return data
}
