// Package transport serves the application over HTTP: the server that binds
// the listener, and the composed router it serves.
//
// The package is split by role, so a reader looking for one thing opens one
// place:
//
//   - server.go (this file) binds the listener to the router and carries the
//     OpenTelemetry instrumentation around it.
//   - router/ builds the surfaces: the request pipeline, the HTTP mounts, and
//     the ConnectRPC procedure table.
//   - handler/ holds the handlers those routers mount, and nothing else — no
//     routing, no mount order.
//   - middleware/ holds the policy pieces of the pipeline: the guard, the
//     bearer, the API-key credential, the client facts, and the database
//     rate-limit driver.
//   - devtools/ holds the debug build's instruments; a release build refuses
//     their paths.
package transport

import (
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/transport/router"
)

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
	observed := otelhttp.NewHandler(handler, "saka", otelhttp.WithFilter(
		func(r *http.Request) bool {
			path := r.URL.Path
			return strings.HasPrefix(path, "/api") || strings.HasPrefix(path, router.RPCPath)
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
