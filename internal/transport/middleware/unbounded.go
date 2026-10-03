package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// UnboundedFor lifts the write deadline and the request deadline off the
// paths it names, keeping the client-disconnect cancellation a raw
// `WithoutCancel` would lose.
//
// A server-streaming procedure is the one surface a request deadline is
// wrong for: the connection's whole job is to stay open past every timeout
// the configuration bounds a unary call with — the middleware deadline the
// router chain sets and the write deadline the http.Server set before the
// handler ran. The handler's context is rebuilt without the deadline (a
// child context inherits its parent's, so cancellation of the deadline
// alone cannot be dropped) and the disconnect signal is carried over by a
// watcher, so a client that walks away still ends the stream.
//
// A path the list does not name is served unchanged.
func UnboundedFor(paths ...string) func(http.Handler) http.Handler {
	named := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		named[path] = struct{}{}
	}
	return unboundedFor(func(path string) bool {
		_, ok := named[path]
		return ok
	})
}

// UnboundedForPrefix is UnboundedFor for a family of paths: every request
// whose path carries the prefix is served without the deadlines. The
// resumable uploads are the family — their paths end in the upload's
// bucket-scoped reference, which no exact list can enumerate.
func UnboundedForPrefix(prefix string) func(http.Handler) http.Handler {
	return unboundedFor(func(path string) bool {
		return strings.HasPrefix(path, prefix)
	})
}

// unboundedFor is the deadline-dropping middleware both entry points share;
// the predicate decides which requests it lifts.
func unboundedFor(match func(string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !match(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// The write deadline the http.Server set before the handler ran
			// is cleared on the response the stream writes to. A writer the
			// controller does not support leaves the deadline standing —
			// the safe side of a failure — because the error is ignored.
			_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

			ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
			defer cancel()

			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-r.Context().Done():
					cancel()
				case <-done:
				}
			}()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
