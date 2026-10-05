package middleware

import (
	"context"
	"net/http"
	"time"
)

// Timeout bounds one request with a context deadline.
//
// The deadline is what a handler sees: a query that takes the ctx, a queue
// publish that waits on it, anything that honours context cancellation, is
// abandoned when it fires. The response is the handler's to write — a handler
// that ignores its context and writes late finds the connection already
// closed by the server's own write timeout, which bounds the same window at
// the transport level.
//
// A duration of zero or less leaves the request unbounded, which is what a
// configuration that named no timeout means.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if d <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
