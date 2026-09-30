// Package transport serves the application over HTTP: the HTTP and ConnectRPC
// routers that mount every surface, the handler behind each route, and the
// server that binds them.
//
// The package is split by role, so a reader looking for one thing opens one
// file:
//
//   - http.go registers the HTTP surface: the request pipeline and the mounts
//     it composes.
//   - rpc.go registers the ConnectRPC surface: the shared codec, the handler
//     options every procedure is registered with, and the procedure table.
//   - handler.go and handler_rpc.go hold the handlers those routers mount, and
//     nothing else — no routing, no mount order.
//   - server.go binds the listener to the router.
//   - middleware/ holds the pipeline pieces, static/ the uploads mount.
//
// This file holds what the package as a whole needs.
package transport

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/guard"
	"github.com/riipandi/tango/internal/transport/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// The paths the limiter never counts, one list per transport: the namespaces
// the two surfaces answer are disjoint, so each list carries only the paths
// its own surface is spared for. A prefix matches the paths under it. The
// limiter itself runs on the API surfaces only, so a static asset or a
// metrics scrape never reaches a check in the first place.
var httpRateLimitExclusions = []string{
	"/api/healthz", // liveness probes and load-balancer checks
}

// restGuardRules is the authorization policy the REST surface's middleware
// applies, read from internal/guard: the same table the RPC surface's guard
// interceptor reads, so a route cannot be public for the authenticator and
// guarded for the guard.
var restGuardRules = guard.RestRules

// devtoolUIPath is where the samber/do web UI mounts: served by a debug
// build, refused with a 404 envelope by a release one.
const devtoolUIPath = "/debug/do"

// webauthnProbePath is where the WebAuthn probe page mounts — the debug
// build's browser instrument; a release build refuses the path.
const webauthnProbePath = "/debug/webauthn-probe"

var rpcRateLimitExclusions = []string{
	healthCheckPath, // the same readiness a monitor watches over ConnectRPC
}

// rpcPublicProcedures lists the procedures the RPC surface answers without a
// caller, read from the same guard table the authorization interceptor uses,
// so a procedure cannot be public for the authenticator and administrative
// for the guard. The bearer middleware refuses every other path, so a new
// procedure is protected by default and a public one is a deliberate entry in
// internal/guard.
var rpcPublicProcedures = guard.PublicProcedures()

// Authenticator authenticates an RPC request before its procedure runs. The
// transport receives it through Options rather than resolving the key service
// itself: verification material belongs to the identity area, and the
// composition root is what joins the two halves.
type Authenticator = middleware.Authenticator

// NewServer builds the HTTP server the router is served through, with the
// timeouts the configuration holds. The caller owns the listener and the
// graceful drain; the server here is only the bound handler and its timeouts.
//
// The handler is wrapped in the OpenTelemetry HTTP instrumentation, so every
// API request the server answers carries a server span and the http duration
// histogram — REST and RPC together, the SPA assets and a metrics scrape left
// out: they are not API traffic, and their spans would be volume without
// signal. The wrapper reads the global providers the observer installs, so a
// signal that is switched off costs a no-op rather than a configuration check
// here.
//
// It reads the same `server` section the router's request deadline is read
// from, so a timeout is decided in one place on both sides of the boundary.
func NewServer(cfg config.Config, handler http.Handler) *http.Server {
	observed := otelhttp.NewHandler(handler, "tango", otelhttp.WithFilter(
		func(r *http.Request) bool {
			path := r.URL.Path
			return strings.HasPrefix(path, "/api") || strings.HasPrefix(path, RPCPath)
		},
	))

	return &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           observed,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}
}
