package webutil

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/riipandi/saka/pkg/jwtutils"
)

// The helpers are the ergonomic half of the kit: the extractions a REST
// handler repeats on every route, answered over the stdlib signatures. They
// are functions, not a context type — the handler keeps
// `func(w http.ResponseWriter, r *http.Request)` and takes what it needs
// (decision 13).

// PathParam returns the URL parameter the mounted route named, or empty when
// the route declared no such parameter. It reads chi's route context, the one
// Mount and every middleware share.
func PathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// Query returns the first query-string value for name, or empty when the
// request carried none.
func Query(r *http.Request, name string) string {
	return r.URL.Query().Get(name)
}

// Decode decodes the request body into dst and validates it. On failure it
// writes the envelope error itself and returns false, so the handler reads:
//
//	if !web.Decode(w, r, &in) {
//		return
//	}
//
// A body the server cannot parse is the client's to fix, so it answers 400
// with the malformed-body detail; a body that decoded but broke a rule
// answers 422 with the field list. A body is read once; a handler that needs
// it twice reads it into a buffer first.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := Request(r.Body, dst)
	if err == nil {
		return true
	}
	if IsValidationError(err) {
		WriteError(w, r, err)
		return false
	}
	Fail(w, r, http.StatusBadRequest, "the request body could not be read",
		WithError(FieldErrors(err)))
	return false
}

// Principal returns the authenticated caller the auth middleware stored in
// the request context, and whether there was one. It is the same value the
// guard's rules read, so a handler's answer and the policy's answer cannot
// disagree about who is acting.
func Principal(r *http.Request) (*jwtutils.Caller, bool) {
	return jwtutils.CallerFrom(r.Context())
}
