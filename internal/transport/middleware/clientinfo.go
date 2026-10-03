package middleware

import (
	"net"
	"net/http"
	"strings"

	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/riipandi/saka/internal/audit"
)

// FingerprintHeader is the header the frontend sends its browser fingerprint
// in.
//
// It is a header rather than a field in every request message because the
// fingerprint is a property of the client, not of the call: a frontend sets
// it once in its fetch interceptor and every procedure carries it, without a
// contract change per request and without a client that forgets it being
// unable to call anything. A request without the header records no
// fingerprint, which is the state every non-browser caller is in.
//
// The value is stored as it arrived. It is opaque here: the frontend computes
// it, and a server that tried to interpret it would be asserting something it
// cannot know.
const FingerprintHeader = "X-Device-Fingerprint"

// ClientInfo captures where a request came from, so a seam below can write it
// into a record without knowing it is inside a request at all.
//
// It is mounted once, on the router that holds every route and every
// procedure, rather than per surface: the facts come from the HTTP request in
// both cases, and a second mount is a second place to forget. The context it
// fills is read through audit.ClientFromContext, which is what keeps a
// service from taking four parameters it only passes on.
//
// The address is resolved by chi's own middlewares and read back through
// chimiddleware.GetClientIP, so the record and the rate limiter agree about
// who the client is. A header a caller can set is not believed: only headers
// the deployment declares its proxy overwrites are read at all.
func ClientInfo(trustedProxyHeaders []string) func(http.Handler) http.Handler {
	resolve := clientIPResolver(trustedProxyHeaders)
	return func(next http.Handler) http.Handler {
		return resolve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := audit.ClientInfo{
				IPAddress:   chimiddleware.GetClientIP(r.Context()),
				UserAgent:   r.Header.Get("User-Agent"),
				Fingerprint: r.Header.Get(FingerprintHeader),
			}
			next.ServeHTTP(w, r.WithContext(audit.WithClientInfo(r.Context(), info)))
		}))
	}
}

// clientIPResolver answers the middleware that decides the client's address.
//
// With no trusted header configured, the connection's own address is the
// answer: the process is reached directly, and X-Forwarded-For or X-Real-IP is
// a header the caller writes. With headers configured, the deployment has said
// its proxy sets them on every request, and the first one that carries a
// parseable address is the hop closest to the client.
//
// The list is ordered, and the order is the precedence: the first header is
// the most preferred, and a header later in the list is read only when every
// header before it was absent. A deployment that terminates TLS itself and
// sits behind a CDN names both — its own proxy's header first, the CDN's
// second — so the address closest to the process wins when both are present.
//
// Every listed header must be one the proxy overwrites on every request.
// Naming a header a proxy merely forwards is what lets a caller choose the
// address a rate-limit bucket and an audit record are keyed by: a header that
// reaches the process unmodified is a header the client wrote.
//
// The connection's address is the last resort, so a request that carries no
// listed header still records where it came from — a health probe on the
// loopback, a test, a client that bypassed the proxy. It never replaces an
// address a listed header supplied: the connection is the proxy's address in
// that case, which is the less useful of the two.
func clientIPResolver(trustedProxyHeaders []string) func(http.Handler) http.Handler {
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
// the rate limiter reads it through GetClientIP, and an address written under
// a second key would be one the limiter never sees. The composition is what
// keeps one address per request across the two consumers.
//
// A header that resolved an address short-circuits the rest: a later header
// must not replace an earlier one, or the order in the configuration would
// mean nothing.
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

// clientIP answers the client's address for a middleware that runs in this
// chain: chi's resolved address, and the connection's own when the ClientInfo
// middleware was not mounted in front of it, which is the state a test that
// exercises one middleware alone is in.
//
// The request log and the rate limiter both call it, so neither reads a
// header of its own and both key by the same address as the audit record.
func clientIP(r *http.Request) string {
	if addr := chimiddleware.GetClientIP(r.Context()); addr != "" {
		return addr
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
