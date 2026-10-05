package middleware

import "net/http"

// SecurityHeaders is the response header set every route carries. They are
// written before the handler runs, so a refusal — the not-found boundary, a
// rate-limit refusal, an authentication error — answers with them too.
//
// X-Content-Type-Options keeps a browser from reinterpreting a response the
// caller asked to sniff; X-Frame-Options keeps the page out of a frame it
// did not choose; Referrer-Policy keeps a token-carrying URL from naming the
// next hop. A frame-ancestors CSP would subsume X-Frame-Options, but the
// SPA ships no CSP yet, and a header this middleware writes unconditionally
// must not promise one it cannot.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
