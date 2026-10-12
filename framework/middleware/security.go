package middleware

import "net/http"

// SecurityHeaders is the response header set every route carries. They are
// written before the handler runs, so a refusal — the not-found boundary, a
// rate-limit refusal, an authentication error — answers with them too.
//
// X-Content-Type-Options keeps a browser from reinterpreting a response the
// caller asked to sniff; X-Frame-Options keeps the page out of a frame it
// did not choose (the CSP's frame-ancestors names the same rule for the
// modern parsers); Referrer-Policy keeps a token-carrying URL from naming
// the next hop. The content security policy gates every load from the
// origin — the script gate is the main fence the JS-readable token cookies
// stand behind. JSON answers carry the headers too — a policy a browser
// never acts on costs nothing.
func SecurityHeaders(next http.Handler) http.Handler {
	return withSecurityHeaders(next, "", false)
}

// SecurityHeadersWithConnect is the same header set, with the telemetry
// collector's origin allowed beside the origin in connect-src — an
// operator who named a collector pointed the browser's exporter somewhere
// that is not the deployment itself, and a policy the page's own tracer
// cannot reach is a policy that silently drops every span.
func SecurityHeadersWithConnect(extraConnect string) func(next http.Handler) http.Handler {
	return SecurityHeadersForMode("", extraConnect)
}

// SecurityHeadersForMode picks the script gate the runtime mode serves. A
// development build's shell injects the HMR preamble as an inline module
// script — a document the compiler transforms, not one a deployment ships —
// so its script gate adds the inline keyword; staging and production keep
// the strict origin-only gate the token cookies stand behind.
func SecurityHeadersForMode(mode, extraConnect string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return withSecurityHeaders(next, extraConnect, mode == "development")
	}
}

func withSecurityHeaders(next http.Handler, extraConnect string, dev bool) http.Handler {
	connectSrc := "'self'"
	if extraConnect != "" {
		connectSrc += " " + extraConnect
	}
	scriptSrc := "'self'"
	if dev {
		scriptSrc += " 'unsafe-inline'"
	}
	fontSrc := "https://cdn.jsdelivr.net"
	if dev {
		// The dev server's own fonts (the devtools' bundle) ride the
		// compiler's module graph — same origin, the /@fs/ pipeline.
		fontSrc += " 'self'"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		header.Set("Content-Security-Policy",
			"default-src 'self';"+
				" script-src "+scriptSrc+";"+
				" style-src 'self' 'unsafe-inline';"+
				" font-src "+fontSrc+";"+
				" img-src 'self' data:;"+
				" connect-src "+connectSrc+";"+
				" worker-src 'self';"+
				" base-uri 'self';"+
				" form-action 'self';"+
				" object-src 'none';"+
				" frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
