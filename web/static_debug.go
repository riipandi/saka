//go:build !release

package web

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/riipandi/saka/pkg/responder"
)

// viteDevServer is where `task dev` runs the Vite dev server — the
// compiler this build borrows. It is a development-only constant, not
// configuration: a deployment never serves a debug binary, and the
// release build resolves every tag from its embedded manifest. The server
// is an implementation detail behind the Go port, never an origin the
// developer opens; the fragment's tags are same-origin, and the proxy
// below is what answers them.
var viteDevServer = "http://127.0.0.1:5173"

// SetupStatic mounts the SPA surface for a debug build. The browser talks
// to this port alone: a document navigation renders the Go shell, and
// everything else a page loads — modules, the HMR socket, the public
// assets — is proxied to the dev server, which never faces the network.
func SetupStatic(r chi.Router) {
	vite, err := url.Parse(viteDevServer)
	if err != nil {
		// The constant above, so a parse failure is a programming error.
		panic("web: the vite dev server URL does not parse: " + err.Error())
	}
	// ReverseProxy carries the HMR socket too: an Upgrade request is
	// hijacked and piped both ways, so the client's websocket lands on the
	// same origin it loaded the document from.
	proxy := httputil.NewSingleHostReverseProxy(vite)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		responder.Fail(w, r, http.StatusBadGateway,
			"the vite dev server is not reachable — run task dev")
	}

	// The SPA answer is a read: a write method that names no claimed route
	// is refused here rather than passed to the not-found boundary, and
	// TRACE in particular must never echo a request back to whoever asked.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			responder.Fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		serveShellOrProxy(w, r, proxy)
	})
	// A method on a path another route claimed is refused by chi's own
	// boundary; the shape here keeps it consistent with the SPA's.
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		responder.Fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
	})
}

// serveShellOrProxy splits the unclaimed surface: a document navigation
// renders the Go shell, and every other path — a module, the Vite client,
// the HMR socket a page opened, a public asset — is the compiler's to
// answer. The JSON and protocol surfaces a fragment must never answer are
// refused with the envelope before either branch runs.
func serveShellOrProxy(w http.ResponseWriter, r *http.Request, proxy *httputil.ReverseProxy) {
	if isSurfacePrefix(r.URL.Path) {
		responder.NotFoundJSON(w, r)
		return
	}

	if isDocumentRequest(r) {
		tags, err := ViteHTMLFragment(ViteConfig{
			IsDev:        true,
			ViteEntry:    DefaultPage.Entry,
			ViteTemplate: ViteReact,
		})
		if err != nil {
			responder.Fail(w, r, http.StatusInternalServerError, "the dev fragment did not resolve: "+err.Error())
			return
		}
		html, err := RenderPage(DefaultPage, tags.Tags)
		if err != nil {
			responder.Fail(w, r, http.StatusInternalServerError, "the document failed to render")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
		return
	}

	proxy.ServeHTTP(w, r)
}

// isDocumentRequest answers whether the request is a navigation: the
// browser's Accept names text/html on a navigation and never on the
// module, asset, or websocket requests the dev server serves.
func isDocumentRequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// isSurfacePrefix lists the protocol and API prefixes the JSON envelope
// answers; the shell and the proxy never claim them.
func isSurfacePrefix(path string) bool {
	for _, prefix := range []string{"/.well-known", "/api", "/rpc", "/metrics", "/static", "oauth", "/oidc", "/debug"} {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
