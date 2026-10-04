// Package middleware holds the HTTP middlewares the transport composes the
// request pipeline from. Each one is independent of the others, so a route
// group can take only what it needs.
package middleware

import (
	"log/slog"
	"net/http"

	"go.jetify.com/typeid"

	"github.com/riipandi/saka/framework/webutil"
)

// RequestIDHeader is the header a request id is read from and written to. It
// is the header pkg/responder names in the envelope metadata.
const RequestIDHeader = webutil.RequestIDHeader

// RequestIDPrefix is the TypeID prefix of a generated request id. The `req_`
// form is what a reader sees in a header, a log line, and a trace, so a request
// can be followed across all of them.
const RequestIDPrefix = "req"

// RequestID tags every request with a TypeID.
//
// An incoming header is honoured so a caller can correlate its own records with
// the server's, and everything else gets a fresh `req_` id. The id is written
// to the response and put on the request context through the responder's own
// idiom, so every handler, log line, and envelope metadata names the same
// request the same way.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			generated, err := typeid.WithPrefix(RequestIDPrefix)
			if err != nil {
				// A prefix of lowercase letters is always valid, so this
				// cannot fail in practice; an untagged request beats a
				// failed one, and the envelope generates its own later.
				slog.ErrorContext(r.Context(), "request id generation failed", "err", err.Error())
				next.ServeHTTP(w, r)
				return
			}
			id = generated.String()
		}

		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(
			webutil.WithRequestID(r.Context(), id),
		))
	})
}

// RequestIDFrom reads the request id a request was tagged with. It is the
// responder's own reader, so middleware and envelope agree on one idiom.
func RequestIDFrom(r *http.Request) string {
	return webutil.RequestIDFromContext(r.Context())
}
