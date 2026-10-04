package user

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// maxPictureSize is the read cap the picture write enforces before the bytes
// reach the service: a picture is an picture, and the engine's request path
// stays cheap only while an upload is.
const maxPictureSize = 2 << 20

// The REST surface the feature claims. It exists beside the RPC procedures
// because a picture is fetched and sent by a browser — an <img> tag and a
// form's file picker — not by a client that speaks the protocol.
//
// The read is public and named in the transport's restPublicRoutes: a stored
// picture answers its bytes, and an account without one answers the bundled
// default by redirect — the relative location resolves against the host the
// client reached, so the same handler serves the dev server's proxy and the
// production binary. An unknown identifier is the one refusal, in the
// envelope the REST surface answers.
//
// The write is protected twice over, and both are the transport's: the bearer
// middleware verified the caller, and the guard rule declared for this route
// established that the account in the path is the caller's own. The body is
// the picture — the raw bytes, not a wrapper around them — and the read cap
// bounds it before it reaches the service, which owns the kind check.
func (m *Module) Mount(r chi.Router) {
	// The `/me` writes come first. chi matches the first pattern whose trie
	// node claims a segment, so a param node registered before the static
	// one would swallow `me` and answer the self-service write with the
	// identifier-addressed one.
	//
	// They are the account's own by construction: the guard's Authenticated
	// rule established the caller and refused a delegation, so the handler
	// reads the subject the bearer middleware verified. The request names no
	// identifier on purpose — an identifier would be a second identity the
	// two halves could disagree about.
	r.Put("/api/users/me/profile-picture", func(w http.ResponseWriter, r *http.Request) {
		caller, ok := jwtutils.CallerFrom(r.Context())
		if !ok {
			webutil.Fail(w, r, http.StatusUnauthorized, "authentication required")
			return
		}

		body := http.MaxBytesReader(w, r.Body, maxPictureSize)
		data, err := io.ReadAll(body)
		if err != nil {
			webutil.Fail(w, r, http.StatusRequestEntityTooLarge,
				"the picture must be at most 2 MiB")
			return
		}

		err = m.service.UpdateProfilePicture(r.Context(), caller.UserID, data)
		switch {
		case errors.Is(err, ErrUnsupportedPicture):
			webutil.Fail(w, r, http.StatusUnsupportedMediaType,
				"the picture must be a PNG, JPEG, or WebP image")
		case errors.Is(err, ErrPicturesUnavailable):
			webutil.Fail(w, r, http.StatusServiceUnavailable,
				"picture storage is not available")
		case err != nil:
			webutil.WriteError(w, r, err)
		default:
			webutil.Success(w, r, http.StatusOK, nil, webutil.WithMessage("the profile picture was updated"))
		}
	})

	r.Delete("/api/users/me/profile-picture", func(w http.ResponseWriter, r *http.Request) {
		caller, ok := jwtutils.CallerFrom(r.Context())
		if !ok {
			webutil.Fail(w, r, http.StatusUnauthorized, "authentication required")
			return
		}

		err := m.service.ResetProfilePicture(r.Context(), caller.UserID)
		switch {
		case errors.Is(err, ErrPicturesUnavailable):
			webutil.Fail(w, r, http.StatusServiceUnavailable,
				"picture storage is not available")
		case err != nil:
			webutil.WriteError(w, r, err)
		default:
			webutil.Success(w, r, http.StatusOK, nil, webutil.WithMessage("the profile picture was reset"))
		}
	})

	r.Get("/api/users/{id}/profile-picture.png", func(w http.ResponseWriter, r *http.Request) {
		picture, err := m.service.ProfilePicture(r.Context(), chi.URLParam(r, "id"))
		switch {
		case errors.Is(err, ErrUserNotFound):
			webutil.Fail(w, r, http.StatusNotFound, "user not found")
			return
		case errors.Is(err, ErrPicturesUnavailable):
			webutil.Fail(w, r, http.StatusServiceUnavailable, "picture storage is not available")
			return
		case err != nil:
			webutil.WriteError(w, r, err)
			return
		}
		defer picture.Body.Close()

		if picture.Default {
			http.Redirect(w, r, DefaultPicturePath, http.StatusFound)
			return
		}

		// The key names one account's picture and its content changes on an
		// update, so the cache holds briefly rather than forever.
		w.Header().Set("Content-Type", picture.ContentType)
		w.Header().Set("Cache-Control", "private, max-age=60")
		// A copy failure after the headers are written is a client that went
		// away mid-body; there is no answer left to send.
		if _, err := io.Copy(w, picture.Body); err != nil {
			webutil.WriteError(w, r, err)
		}
	})

	r.Put("/api/users/{id}/profile-picture", func(w http.ResponseWriter, r *http.Request) {
		body := http.MaxBytesReader(w, r.Body, maxPictureSize)
		data, err := io.ReadAll(body)
		if err != nil {
			webutil.Fail(w, r, http.StatusRequestEntityTooLarge,
				"the picture must be at most 2 MiB")
			return
		}

		err = m.service.UpdateProfilePicture(r.Context(), chi.URLParam(r, "id"), data)
		switch {
		case errors.Is(err, ErrUserNotFound):
			webutil.Fail(w, r, http.StatusNotFound, "user not found")
		case errors.Is(err, ErrUnsupportedPicture):
			webutil.Fail(w, r, http.StatusUnsupportedMediaType,
				"the picture must be a PNG, JPEG, or WebP image")
		case errors.Is(err, ErrPicturesUnavailable):
			webutil.Fail(w, r, http.StatusServiceUnavailable,
				"picture storage is not available")
		case err != nil:
			webutil.WriteError(w, r, err)
		default:
			webutil.Success(w, r, http.StatusOK, nil, webutil.WithMessage("the profile picture was updated"))
		}
	})
}
