//go:build release

package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/riipandi/tango/pkg/responder"
)

//go:embed all:output
var webFS embed.FS

func SetupStatic(r chi.Router) {
	// The SPA answers only reads. A write method that names no claimed route
	// falls through to the not-found handler — chi cannot tell "no such
	// path" from "no such method for the SPA" — so the handler itself
	// refuses anything but GET and HEAD with the method-not-allowed shape.
	// TRACE in particular must never echo a request back to whoever asked.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			responder.Fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		spaHandler()(w, r)
	})
	// The not-found boundary above covers the paths nothing claimed. For a
	// path some other route claimed with another method, chi's own
	// method-not-allowed boundary answers, and it carries Allow: GET, HEAD
	// so a caller learns what a SPA path accepts without being reflected.
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		responder.Fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
	})
}

func spaHandler() http.HandlerFunc {
	webArtifact, _ := fs.Sub(webFS, "output")
	fileServer := http.FileServer(http.FS(webArtifact))

	return func(w http.ResponseWriter, r *http.Request) {
		// These endpoints should return JSON or plain text output
		if strings.HasPrefix(r.URL.Path, "/.well-known") ||
			strings.HasPrefix(r.URL.Path, "/api") ||
			strings.HasPrefix(r.URL.Path, "/rpc") ||
			strings.HasPrefix(r.URL.Path, "/metrics") ||
			strings.HasPrefix(r.URL.Path, "/static") ||
			strings.HasPrefix(r.URL.Path, "/oidc") ||
			strings.HasPrefix(r.URL.Path, "/debug") {
			responder.NotFoundJSON(w, r)
			return
		}

		reqPath := strings.TrimPrefix(r.URL.Path, "/")

		if reqPath != "" {
			cleanPath := filepath.Clean(reqPath)
			if !strings.HasPrefix(cleanPath, ".") {
				if f, err := webArtifact.Open(cleanPath); err == nil {
					f.Close()
					fileServer.ServeHTTP(w, r)
					return
				}
			}
		}

		http.ServeFileFS(w, r, webArtifact, "index.html")
	}
}
