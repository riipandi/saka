package bundler

import (
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/riipandi/saka/framework/webutil"
)

// The SPA surface mounts in two modes that were once build tags: a
// development build proxies the Vite dev server behind the Go shell, and a
// release build serves its own embedded assets. Both answer the same
// contract — a read-only surface whose unclaimed GET renders the document,
// the reserved API prefixes refused before either branch can claim them —
// so a page cannot tell which one answered.

// MountDev mounts the SPA surface for a development build. The browser
// talks to this port alone: a document navigation renders the Go shell,
// and everything else a page loads — modules, the HMR socket, the public
// assets — is proxied to the dev server named by viteURL, which never
// faces the network.
func MountDev(r chi.Router, viteURL string, page Page, reserved ...string) {
	target, err := url.Parse(viteURL)
	if err != nil {
		// The caller's constant, so a parse failure is a programming error.
		panic("bundler: the vite dev server URL does not parse: " + err.Error())
	}
	// ReverseProxy carries the HMR socket too: an Upgrade request is
	// hijacked and piped both ways, so the client's websocket lands on the
	// same origin it loaded the document from.
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		webutil.Fail(w, r, http.StatusBadGateway,
			"the vite dev server is not reachable — run task dev")
	}

	// One not-found handler for the whole surface: the reserved prefixes,
	// the shell's answer, and the compiler's surface compose in one body,
	// because chi keeps the not-found handler registered first — a second
	// NotFound call would never run, and a write that names no claimed
	// route would be answered with silence.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if reservedPath(r.URL.Path, reserved) {
			// The API and protocol prefixes are the envelope's: any method
			// an unclaimed path there carries answers the JSON 404, never a
			// method rule the static surface invented.
			webutil.NotFoundJSON(w, r)
			return
		}
		if documentRequest(r) {
			// A navigation is a read: a write method that asks for a
			// document is refused before the shell renders.
			if refuseWrite(w, r) {
				return
			}
			tags, err := ViteHTMLFragment(ViteConfig{
				IsDev:        true,
				ViteEntry:    page.Entry,
				ViteTemplate: ViteReact,
			})
			if err != nil {
				webutil.Fail(w, r, http.StatusInternalServerError, "the dev fragment did not resolve: "+err.Error())
				return
			}
			renderShell(w, r, page, tags.Tags)
			return
		}
		// Everything else on a non-reserved path is the compiler's to
		// answer, whatever the method carries — the module graph and the
		// HMR socket, but also the dev tooling's own writes, the TanStack
		// devtools console pipe among them.
		proxy.ServeHTTP(w, r)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		refuseWrite(w, r)
	})
}

// MountRelease mounts the SPA surface for a built binary: the assets come
// from the fs.FS the caller embeds, and the shell resolves its fragment
// from the same tree's build manifest once — the manifest is embedded, so
// its answer is fixed at build time. A binary whose output carries no
// manifest is a build-order error — Vite must run before Go — and the
// surface answers the envelope's failure rather than a half-shell.
func MountRelease(r chi.Router, assets fs.FS, page Page, reserved ...string) {

	var (
		once  sync.Once
		tags  template.HTML
		tagEr error
	)
	resolveFragment := func() (template.HTML, error) {
		once.Do(func() {
			f, err := ViteHTMLFragment(ViteConfig{
				FS:        assets,
				ViteEntry: page.Entry,
			})
			if err != nil {
				tagEr = err
				return
			}
			tags = f.Tags
		})
		return tags, tagEr
	}

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if refuseWrite(w, r) {
			return
		}
		if reservedPath(r.URL.Path, reserved) {
			webutil.NotFoundJSON(w, r)
			return
		}
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath != "" {
			cleanPath := filepath.Clean(reqPath)
			if !strings.HasPrefix(cleanPath, ".") {
				if f, err := assets.Open(cleanPath); err == nil {
					f.Close()
					http.FileServer(http.FS(assets)).ServeHTTP(w, r)
					return
				}
			}
		}

		resolved, err := resolveFragment()
		if err != nil {
			webutil.Fail(w, r, http.StatusInternalServerError, "vite: the build manifest did not resolve: "+err.Error())
			return
		}
		renderShell(w, r, page, resolved)
	})
}

// documentRequest answers whether the request is a navigation: the
// browser's Accept names text/html on a navigation and never on the module,
// asset, or websocket requests the dev server serves.
func documentRequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// refuseWrite keeps the navigation surface read-only: a write method that
// asks for a document is refused rather than rendered, and TRACE in
// particular must never echo a request back to whoever asked. The release
// not-found boundary, the dev navigation branch, and the method-mismatch
// handler call it — on the dev surface the compiler's own non-reserved
// paths answer before any method rule does.
func refuseWrite(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		webutil.Fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return true
	}
	return false
}

// reservedPath answers whether the path is one of the API and protocol
// prefixes the JSON envelope answers — the shell and the assets never
// claim them.
func reservedPath(path string, reserved []string) bool {
	for _, prefix := range reserved {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// renderShell writes the document for one page with one fragment.
func renderShell(w http.ResponseWriter, r *http.Request, page Page, tags template.HTML) {
	html, err := RenderPage(page, tags)
	if err != nil {
		webutil.Fail(w, r, http.StatusInternalServerError, "the document failed to render")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}
