// Package static serves the files a feature wrote under the data directory's
// uploads subdirectory, at the route prefix app.assets_url points at.
//
// It lives under transport because it is part of the HTTP surface, beside
// middleware: it is a mount the router composes, not a service a module owns,
// and nothing outside transport imports it. It is separate from
// web.SetupStatic, which serves the compiled SPA: the two answer different
// questions, and keeping them apart is what lets this one have no fallback —
// a missing upload is a 404, never an HTML page.
//
// What a key is served from is a driver behind Upload, so the mount outlives
// the local filesystem. A driver over an object store answers the same call by
// handing the client a URL, and the route, the key, and the not-found boundary
// stay as they are.
package static

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/riipandi/saka/internal/config"
)

// Path is the route prefix the uploads are served under. It is the path
// app.assets_url defaults to, so a value written there and a file written
// under the data directory meet at the same URL.
const Path = "/static"

// ErrNotFound is what a driver answers for a key it holds no file under. A
// driver translates its own protocol's not-found shape into this one at its
// edge, which is what makes a missing upload a 404 rather than a failure.
var ErrNotFound = errors.New("static: no upload under key")

// Upload is a source of whole files the mount serves.
//
// A key is the storage key — the name internal/storage was given when the file
// was staged — so what a feature wrote and what a client fetches are named by
// one string. The driver owns the response: it decides the content type, the
// status, and whether the bytes travel at all.
type Upload interface {
	// Serve answers one request for key. The error wraps ErrNotFound when the
	// driver holds no file under it; any other error is reported as a failed
	// request. A driver returns an error only before it has written to w.
	Serve(ctx context.Context, w http.ResponseWriter, r *http.Request, key string) error
}

// Handler serves dir at Path through the local driver.
func Handler(dir string) http.Handler {
	return handler(NewLocal(dir))
}

// Mount registers the driver on the router.
func Mount(r chi.Router, uploads Upload) {
	h := handler(uploads)
	r.Handle(Path, h)
	r.Handle(Path+"/*", h)
}

// Dir returns the uploads directory under the given data directory.
func Dir(dataDir string) string {
	if dataDir == "" {
		dataDir = "."
	}
	return filepath.Join(dataDir, config.UploadsDir)
}

// handler is the one place the mount's policy lives, so every driver answers
// with the same headers and the same not-found boundary.
func handler(uploads Upload) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The uploads are immutable by convention — a stored file's name
		// carries its identity — so a client may cache one for as long as it
		// likes without a revalidation round trip. The path is what names the
		// content, so a replacement lands under a new name.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		err := uploads.Serve(r.Context(), w, r, key(r))
		switch {
		case err == nil:
			return
		case errors.Is(err, ErrNotFound):
			// The header above describes the bytes of a file that exists, so a
			// miss must not carry it: a client that kept the 404 would never
			// see the file a later write stores. The standard library strips it
			// from its own error responses; dropping it here is what makes the
			// rule this mount's, so a driver that writes its own response
			// inherits it rather than having to know it.
			w.Header().Del("Cache-Control")
			http.NotFound(w, r)
		default:
			w.Header().Del("Cache-Control")
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
	})
}

// key is the storage key a request names: the path under the mount, without a
// leading slash. The path is taken as it arrived, so a ".." segment is left
// for the filesystem to refuse rather than cleaned away here, where the
// refusal would be this function's to keep in step with.
func key(r *http.Request) string {
	rest, ok := strings.CutPrefix(r.URL.Path, Path)
	if !ok {
		return ""
	}
	return strings.TrimPrefix(rest, "/")
}
