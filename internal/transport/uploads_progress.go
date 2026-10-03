package transport

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/riipandi/saka/internal/storage"
	"github.com/riipandi/saka/pkg/responder"
)

// uploadProgress serves `GET /api/uploads/{bucket}/{key}` — the poll a
// client runs while its upload travels. The engine stages whole files, so
// the answer is the manifest's own state: a status word, the byte size, and
// nothing pretending to be a percentage. The bucket/key pair is the one the
// upload used; a pair nothing stored answers the not-found the file's own
// read answers.
//
// The route is the transport's own rather than a module's: the storage
// engine is infrastructure, and a feature that uploads names its keys — the
// progress read stays beside the engine that owns the facts, so no second
// progress source grows beside the manifest.
func uploadProgressHandler(manager *storage.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref := chi.URLParam(r, "*")
		bucket, key, err := storage.SplitRef(ref)
		if err != nil {
			responder.Fail(w, r, http.StatusNotFound, "upload not found")
			return
		}

		manifest, err := manager.Manifest(r.Context(), bucket, key)
		if errors.Is(err, storage.ErrNotFound) {
			responder.Fail(w, r, http.StatusNotFound, "upload not found")
			return
		}
		if err != nil {
			responder.WriteError(w, r, err)
			return
		}

		responder.Success(w, r, http.StatusOK, map[string]any{
			"key":    manifest.Bucket + "/" + manifest.Key,
			"status": manifest.Status,
			"size":   manifest.Size,
		})
	}
}
