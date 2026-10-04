// Package storage serves the files the storage engine holds, at
// /storage/{bucket}/{key}. The first path segment is resolved against the
// bucket table before anything is touched, so a directory that exists on
// disk but names no bucket is a 404 — the engine's own subtrees
// (staging, logs, backup, config) can never be reached this way.
//
// It lives under transport because it is part of the HTTP surface, beside
// middleware: it is a mount the router composes, not a service a module
// owns, and nothing outside transport imports it. It is separate from
// web.SetupStatic, which serves the compiled SPA: the two answer different
// questions, and keeping them apart is what lets this one have no fallback —
// a missing object is a 404, never an HTML page.
//
// The bytes stream from the active backend through the storage engine, so a
// deployment backed by an object store serves the same URL shape as a local
// one; only what answers the read changes.
package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	engine "github.com/riipandi/saka/framework/storage"
)

// Path is the route prefix the stored files are served under. It is the path
// app.assets_url defaults to, so a value written there and a file stored in
// a bucket meet at the same URL.
const Path = "/storage"

// ManagerSource is what the mount reads through: the storage engine's
// bucket-aware read surface.
type ManagerSource interface {
	Manifest(ctx context.Context, bucket, key string) (engine.Manifest, error)
	Open(ctx context.Context, bucket, key string) (io.ReadCloser, error)
}

// Mount registers the /storage mount on the router, backed by the storage
// engine. The signer verifies the links a private object is read over; nil
// fails every private read closed — a private object answers 404 whether a
// signer is wired or not, so marking objects private without a signer has
// broken them on purpose.
func MountStorage(r chi.Router, manager ManagerSource, signer *engine.Signer) {
	h := handler(manager, signer)
	r.Handle(Path, h)
	r.Handle(Path+"/*", h)
}

// handler is the one place the mount's policy lives: the visibility check,
// the cache headers, the not-found boundary, and bucket/key parsing.
func handler(source ManagerSource, signer *engine.Signer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket, key, ok := splitRequestPath(r.URL.Path)
		if !ok {
			miss(w, r)
			return
		}

		manifest, err := source.Manifest(r.Context(), bucket, key)
		if errors.Is(err, engine.ErrNotFound) {
			miss(w, r)
			return
		}
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		// A private object is the signed link's only audience. The refusal
		// is the 404 a missing object gets — never a 403 that confirms the
		// key exists — and the response must not be cached where a later
		// holder of the link could dig the bytes out of.
		if manifest.IsPrivate {
			exp, _ := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
			if signer == nil || !signer.Verify(bucket, key, exp, r.URL.Query().Get("sig")) {
				miss(w, r)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
		} else {
			// Stored files are immutable by convention — a stored file's
			// name carries its identity — so a client may cache one for as
			// long as it likes without a revalidation round trip. The path
			// is what names the content, so a replacement lands under a new
			// name.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")

		body, err := source.Open(r.Context(), bucket, key)
		if err != nil {
			w.Header().Del("Cache-Control")
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		defer func() { _ = body.Close() }()

		contentType, _ := manifest.Metadata["content_type"].(string)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		if manifest.Size > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(manifest.Size, 10))
		}

		// A local read is seekable, so ServeContent can honour range and
		// conditional requests; an object-store stream is not, so it is
		// copied through as one response.
		if seeker, ok := body.(io.ReadSeeker); ok {
			http.ServeContent(w, r, key, manifest.StagingMtime, seeker)
			return
		}
		if _, err := io.Copy(w, body); err != nil {
			// Too late to change the status once the body has started.
			return
		}
	})
}

// splitRequestPath parses /storage/{bucket}/{key...} into its bucket name
// and key. Anything malformed — no bucket, no key, a /storage prefix miss —
// is not served.
func splitRequestPath(path string) (bucket, key string, ok bool) {
	rest, found := strings.CutPrefix(path, Path+"/")
	if !found {
		return "", "", false
	}
	bucket, key, found = strings.Cut(rest, "/")
	if !found || bucket == "" || key == "" {
		return "", "", false
	}
	return bucket, key, true
}

// miss answers a request the mount cannot serve. The cache header a served
// file carries describes its bytes, so a miss must not carry one: a client
// that kept the 404 would never see the file a later write stores.
func miss(w http.ResponseWriter, r *http.Request) {
	w.Header().Del("Cache-Control")
	http.NotFound(w, r)
}
