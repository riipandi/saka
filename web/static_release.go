//go:build release

package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/riipandi/tango/pkg/responder"
)

//go:embed all:output
var webFS embed.FS

// OutputFS answers the embedded build output — the manifest a second
// page's fragment resolves against, and the hashed assets the surface
// serves. The release build is the only one that carries it.
func OutputFS() fs.FS {
	artifact, _ := fs.Sub(webFS, "output")
	return artifact
}

// The entry fragment resolves once: the manifest is embedded, so its
// answer is fixed at build time. A release binary whose output carries no
// manifest is a build-order error — Vite must run before Go — and the
// surface answers the envelope's failure rather than a half-shell.
var (
	fragmentOnce sync.Once
	fragmentTags template.HTML
	fragmentErr  error
)

func resolveFragment() (template.HTML, error) {
	fragmentOnce.Do(func() {
		webArtifact, subErr := fs.Sub(webFS, "output")
		if subErr != nil {
			fragmentErr = subErr
			return
		}
		f, err := ViteHTMLFragment(ViteConfig{
			FS:        webArtifact,
			ViteEntry: DefaultPage.Entry,
		})
		if err != nil {
			fragmentErr = err
			return
		}
		fragmentTags = f.Tags
	})
	return fragmentTags, fragmentErr
}

// SetupStatic mounts the SPA surface: the built assets from the embedded
// output, and the Go-rendered shell for every path the routes above left
// unclaimed.
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

		tags, err := resolveFragment()
		if err != nil {
			responder.Fail(w, r, http.StatusInternalServerError, "vite: the build manifest did not resolve: "+err.Error())
			return
		}
		html, err := RenderPage(DefaultPage, tags)
		if err != nil {
			responder.Fail(w, r, http.StatusInternalServerError, "the document failed to render")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}
}
