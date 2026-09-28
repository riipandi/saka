//go:build !release

package web

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/riipandi/tango/pkg/responder"
)

func SetupStatic(r chi.Router) {
	// The SPA answer is a read: a write method that names no claimed route
	// is refused here rather than passed to the not-found boundary, and
	// TRACE in particular must never echo a request back to whoever asked.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			responder.Fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
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
		http.Error(w, "not found", http.StatusNotFound)
	})
	// A method on a path another route claimed is refused by chi's own
	// boundary; the shape here keeps it consistent with the SPA's.
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		responder.Fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
	})
}
