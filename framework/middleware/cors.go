package middleware

import (
	"net/http"
	"time"

	corslib "github.com/rs/cors"
)

// CORSOptions is the cross-origin policy the middleware enforces. Every list
// is explicit — the caller passes the values its configuration resolved; this
// package reads nothing itself.
type CORSOptions struct {
	// AllowedOrigins lists the origins a browser may call from. Each is a
	// scheme://host origin without a path, or "*" for any origin, which
	// AllowCredentials forbids combining with.
	AllowedOrigins []string
	// AllowedMethods lists the HTTP methods a cross-origin request may use.
	AllowedMethods []string
	// AllowedHeaders lists the request headers a cross-origin call may set.
	AllowedHeaders []string
	// ExposedHeaders lists the response headers a browser script may read on
	// a cross-origin response.
	ExposedHeaders []string
	// AllowCredentials lets a cross-origin call carry cookies and credentials.
	AllowCredentials bool
	// MaxAge is how long a browser may cache a preflight answer.
	MaxAge time.Duration
}

// DefaultCORSMethods is the method list a dual REST+RPC surface needs, the
// fallback for a policy that names only its origins. An operator who narrows
// the list owns the narrowing.
var DefaultCORSMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// DefaultCORSHeaders is the header list a browser call may set on the dual
// surface: the fetch-safelist plus the Connect protocol's own headers.
var DefaultCORSHeaders = []string{
	"Accept",
	"Content-Type",
	"Authorization",
	"X-Requested-With",
	"X-Api-Key",
	"Connect-Protocol-Version",
	"Connect-Timeout-Ms",
	"X-User-Agent",
	"Grpc-Timeout",
	"X-Grpc-Web",
}

// DefaultCORSExposedHeaders is the response header list a browser script may
// read: the gRPC-web trailers a Connect error arrives as.
var DefaultCORSExposedHeaders = []string{
	"Grpc-Message",
	"Grpc-Status",
	"Grpc-Status-Details-Bin",
}

// CORS returns the cross-origin middleware the options ask for. The policy is
// applied to every route, so a preflight is answered wherever it lands and a
// caller cannot forget one route's policy.
//
// An empty origin list keeps the policy closed: the middleware passes the
// request through without cross-origin headers, which is what a same-origin
// SPA needs. The middleware itself is disabled when no origin is named, rather
// than wrapping every call in a check it can never pass.
//
// The method and header lists the options omit fall back to the defaults the
// dual surface needs, so an options set that names only its origins still
// answers a browser's preflight correctly. A list the options names is used
// as-is.
func CORS(opts CORSOptions) func(http.Handler) http.Handler {
	if len(opts.AllowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	methods := opts.AllowedMethods
	if len(methods) == 0 {
		methods = DefaultCORSMethods
	}
	headers := opts.AllowedHeaders
	if len(headers) == 0 {
		headers = DefaultCORSHeaders
	}
	exposed := opts.ExposedHeaders
	if len(exposed) == 0 {
		exposed = DefaultCORSExposedHeaders
	}

	handle := corslib.New(corslib.Options{
		AllowedOrigins:   opts.AllowedOrigins,
		AllowedMethods:   methods,
		AllowedHeaders:   headers,
		ExposedHeaders:   exposed,
		AllowCredentials: opts.AllowCredentials,
		MaxAge:           int(opts.MaxAge / time.Second),
	}).Handler

	return handle
}
