package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/riipandi/saka/framework/webutil"
)

// Logger writes one line per request: method, path, status, duration, size,
// request id, and the client address.
//
// The level follows the status, so a probe of a failing service is found by
// the same filter that finds the failure: 5xx logs at error, 4xx at warn,
// everything else at info. The line is written after the response, so a
// handler that panics produces no request line — Recoverer logs the panic
// instead, and its own error line names the request.
//
// The request id comes from the context the RequestID middleware put there.
// Running Logger after RequestID is what makes the two agree.
func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if log == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			status := rec.status
			if status == 0 {
				// A handler that returned without writing is a 200 the
				// server has not stamped yet.
				status = http.StatusOK
			}
			switch {
			case status >= http.StatusInternalServerError:
				level = slog.LevelError
			case status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}

			attrs := []slog.Attr{
				slog.String("request_id", webutil.RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Duration("duration", time.Since(start)),
				slog.Int("bytes", rec.bytes),
				slog.String("remote", ClientIP(r)),
			}
			// The context of a finished request may already be cancelled; the
			// span it carried is still readable, so correlation survives.
			log.LogAttrs(context.WithoutCancel(r.Context()), level, "request", attrs...)
		})
	}
}

// responseRecorder captures the status and the size of the response the
// handler wrote, so the request line reports what was sent rather than what
// was intended. Unwrap keeps http.ResponseController working through it.
type responseRecorder struct {
	http.ResponseWriter

	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
