//go:build !release

package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/riipandi/tango/pkg/responder"
)

// viteDevURL is where the debug build's shell points the browser for its
// assets: the Vite dev server `pnpm dev` runs. It is a development-only
// constant, not configuration — a deployment never serves a debug binary,
// and the release build resolves every tag from its embedded manifest.
// The server must be running for the page's assets to load; the API calls
// it makes stay same-origin against the Go port, which is what keeps the
// session cookie first-party.
const viteDevURL = "http://localhost:5173"

// SetupStatic mounts the SPA surface for a debug build: every unclaimed
// GET renders the Go shell, whose fragment points at the Vite dev server.
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
		serveShell(w, r)
	})
	// A method on a path another route claimed is refused by chi's own
	// boundary; the shape here keeps it consistent with the SPA's.
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		responder.Fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
	})
}

// serveShell renders the debug shell: one dev fragment per page, resolved
// against the dev server without touching a build manifest. The JSON
// surfaces a fragment must never answer are refused with the envelope
// before the document renders.
func serveShell(w http.ResponseWriter, r *http.Request) {
	if isSurfacePrefix(r.URL.Path) {
		responder.NotFoundJSON(w, r)
		return
	}

	tags, err := ViteHTMLFragment(ViteConfig{
		IsDev:        true,
		ViteURL:      viteDevURL,
		ViteEntry:    DefaultPage.Entry,
		ViteTemplate: ViteReact,
	})
	if err != nil {
		responder.Fail(w, r, http.StatusInternalServerError, "the dev fragment did not resolve: "+err.Error())
		return
	}
	html, err := renderShell(DefaultPage, tags.Tags)
	if err != nil {
		responder.Fail(w, r, http.StatusInternalServerError, "the document failed to render")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

// isSurfacePrefix lists the protocol and API prefixes the JSON envelope
// answers; the shell never claims them.
func isSurfacePrefix(path string) bool {
	for _, prefix := range []string{"/.well-known", "/api", "/rpc", "/metrics", "/static", "/oidc", "/debug"} {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
