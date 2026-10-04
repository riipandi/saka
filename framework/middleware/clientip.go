package middleware

import (
	"net"
	"net/http"
	"strings"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

// ClientIPResolver mounts the client-address resolution every keyed
// consumer shares: the request log, the rate limiter, and an audit record all
// key by the same address because they all read it from here.
//
// Only headers the caller declares its proxy overwrites are believed. Naming
// a header a proxy merely forwards is what lets a caller choose the address a
// rate-limit bucket and an audit record are keyed by: a header that reaches
// the process unmodified is a header the client wrote.
//
// The connection's address is the last resort, so a request that carries no
// listed header still records where it came from — a health probe on the
// loopback, a test, a client that bypassed the proxy. It never replaces an
// address a listed header supplied: the connection is the proxy's address in
// that case, which is the less useful of the two.
func ClientIPResolver(trustedProxyHeaders []string) func(http.Handler) http.Handler {
	headers := normalizeHeaders(trustedProxyHeaders)
	return func(next http.Handler) http.Handler {
		return firstResolved(headers, remoteAddrFallback(next))
	}
}

// normalizeHeaders drops the entries that name nothing, so a configuration
// written with a trailing empty entry reads as the headers it actually names
// rather than as a header that can never match.
func normalizeHeaders(headers []string) []string {
	named := make([]string, 0, len(headers))
	for _, header := range headers {
		if trimmed := strings.TrimSpace(header); trimmed != "" {
			named = append(named, trimmed)
		}
	}
	return named
}

// firstResolved reads the client's address from the first header that carries
// one, in the order the headers are named.
//
// It composes chi's own ClientIPFromHeader rather than reading the header
// itself, because the context key that holds the resolved address is chi's:
// the rate limiter reads it through ClientIP, and an address written under a
// second key would be one the limiter never sees. The composition is what
// keeps one address per request across the consumers.
//
// A header that resolved an address short-circuits the rest: a later header
// must not replace an earlier one, or the order in the options would mean
// nothing.
func firstResolved(headers []string, next http.Handler) http.Handler {
	if len(headers) == 0 {
		return next
	}
	head := chimiddleware.ClientIPFromHeader(headers[0])
	tail := firstResolved(headers[1:], next)
	return head(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if chimiddleware.GetClientIP(r.Context()) != "" {
			next.ServeHTTP(w, r)
			return
		}
		tail.ServeHTTP(w, r)
	}))
}

// remoteAddrFallback uses the connection's address when no header resolved
// one. It is conditional on purpose: chi's own ClientIPFromRemoteAddr always
// writes, so mounting it unconditionally would overwrite the address a
// trusted header supplied with the proxy's own.
func remoteAddrFallback(next http.Handler) http.Handler {
	remote := chimiddleware.ClientIPFromRemoteAddr(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if chimiddleware.GetClientIP(r.Context()) != "" {
			next.ServeHTTP(w, r)
			return
		}
		remote.ServeHTTP(w, r)
	})
}

// ClientIP answers the client's address for a middleware that runs in this
// chain: chi's resolved address, and the connection's own when the resolver
// was not mounted in front of it, which is the state a test that exercises
// one middleware alone is in.
func ClientIP(r *http.Request) string {
	if addr := chimiddleware.GetClientIP(r.Context()); addr != "" {
		return addr
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
