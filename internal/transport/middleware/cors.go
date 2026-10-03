package middleware

import (
	"net/http"
	"time"

	corslib "github.com/rs/cors"

	"github.com/riipandi/saka/internal/config"
)

// CORS returns the cross-origin middleware the configuration asks for. The
// policy is applied to every route, so a preflight is answered wherever it
// lands and a deployment cannot forget one route's policy.
//
// An empty origin list keeps the policy closed: the middleware passes the
// request through without cross-origin headers, which is what a same-origin
// SPA needs. The middleware itself is disabled when no origin is named, rather
// than wrapping every call in a check it can never pass.
//
// The method and header lists a configuration omits fall back to the defaults
// the dual surface needs — the Connect protocol's own headers and the REST
// surface's methods — so a configuration that names only its origins still
// answers a browser's preflight correctly. A list the configuration names is
// used as-is: an operator who narrows it owns the narrowing.
func CORS(cfg config.CORS) func(http.Handler) http.Handler {
	if len(cfg.AllowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	methods := cfg.AllowedMethods
	if len(methods) == 0 {
		methods = config.DefaultCORSMethods
	}
	headers := cfg.AllowedHeaders
	if len(headers) == 0 {
		headers = config.DefaultCORSHeaders
	}
	exposed := cfg.ExposedHeaders
	if len(exposed) == 0 {
		exposed = config.DefaultCORSExposedHeaders
	}

	handle := corslib.New(corslib.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   methods,
		AllowedHeaders:   headers,
		ExposedHeaders:   exposed,
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           int(cfg.MaxAge / time.Second),
	}).Handler

	return handle
}
