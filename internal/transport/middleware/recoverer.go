package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/riipandi/saka/framework/webutil"
)

// Recoverer turns a panicking handler into a 500 response.
//
// Without it a panic closes the connection and the only trace of the failure
// is net/http's own stderr line, which carries no request id and reaches no
// configured transport. The panic value and the stack are logged through the
// process logger, and the response is the standard error envelope, so a client
// that was promised JSON never receives a bare protocol error instead.
//
// http.ErrAbortHandler is re-panicked: it is the handler's way to ask for the
// connection to be dropped, and answering it with an envelope would undo that.
//
// A panic after the response began — a streaming handler, a flush mid-write —
// cannot be answered with an envelope, so only the log line is written and the
// broken response is left as it is.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			track := &headerTracker{ResponseWriter: w}
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				if log != nil {
					log.ErrorContext(context.WithoutCancel(r.Context()), "panic recovered",
						"request_id", webutil.RequestIDFromContext(r.Context()),
						"method", r.Method,
						"path", r.URL.Path,
						"panic", fmt.Sprint(rec),
						"stack", string(debug.Stack()))
				}

				// A written response cannot be replaced: the status is gone,
				// and an envelope appended to it would corrupt the body.
				if track.wrote {
					return
				}
				webutil.Fail(track, r, http.StatusInternalServerError, "internal server error")
			}()
			next.ServeHTTP(track, r)
		})
	}
}

// headerTracker records whether the response has started, so a late panic is
// not answered with an envelope the client can no longer receive. Unwrap keeps
// http.ResponseController working through it.
type headerTracker struct {
	http.ResponseWriter

	wrote bool
}

func (h *headerTracker) WriteHeader(status int) {
	h.wrote = true
	h.ResponseWriter.WriteHeader(status)
}

func (h *headerTracker) Write(p []byte) (int, error) {
	h.wrote = true
	return h.ResponseWriter.Write(p)
}

func (h *headerTracker) Flush() {
	if flusher, ok := h.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (h *headerTracker) Unwrap() http.ResponseWriter { return h.ResponseWriter }
