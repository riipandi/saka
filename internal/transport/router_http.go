package transport

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/health"
	"github.com/riipandi/tango/internal/kernel"
	"github.com/riipandi/tango/internal/storage"
	"github.com/riipandi/tango/internal/transport/middleware"
	"github.com/riipandi/tango/internal/transport/static"
	"github.com/riipandi/tango/pkg/responder"
	"github.com/riipandi/tango/web"
)

// Options is what the routers need to serve. Every field is explicit, so a
// caller cannot construct a router whose dependencies it cannot name.
type Options struct {
	// Config is the resolved configuration. The routers read the CORS policy,
	// the Prometheus path, and the application identity from it.
	Config config.Config
	// Checker reports the health the `/api/healthz` endpoint and the health
	// procedure publish. A nil checker leaves both out, rather than publishing
	// a result nothing backed.
	Checker *health.Checker
	// Metrics is the Prometheus exposition handler. A nil handler mounts no
	// metrics endpoint, which is the state a disabled signal is in.
	Metrics http.Handler
	// Logger is the process logger the request middleware writes through. A
	// nil logger leaves the request and panic middleware out, which is the
	// state a test that reads only responses is in.
	Logger *slog.Logger
	// RateLimiter is the limiter the API surface is throttled by. A nil
	// limiter mounts no throttling, which is the state a run without a
	// rate_limit driver is in.
	RateLimiter middleware.Limiter
	// Modules are the feature modules whose routes and procedures the server
	// mounts.
	Modules []kernel.Module
	// Authenticator authenticates the RPC surface's requests before their
	// procedures run. A nil authenticator leaves the surface open, which is
	// the state a test that reads only responses is in.
	Authenticator Authenticator
	// Injector is the samber/do container the run composed. Only the debug
	// build's devtool reads it; a release build ignores the field.
	Injector do.Injector
}

// NewRouter builds the HTTP surface: the request pipeline, then the endpoints
// it mounts, then the ConnectRPC surface, the uploads, and the SPA last.
//
// The pipeline runs request id first, so every response and log line can name
// its request; then the request logger and the panic recovery around every
// route; then CORS, so a policy question is answered before a route runs; then
// the request timeout.
//
// The rate limiter wraps the routes a client calls, and a static asset or a
// metrics scrape is outside it: the budget belongs to the API, not to the page
// that embeds it. The throttled surface is a chi group rather than the /api
// subrouter, because a module mounts its own routes — a feature under /api, a
// protocol endpoint at /.well-known — on the router it is handed, and
// middleware attached to the /api subrouter never reaches them. The group is
// what makes the limiter cover every mounted route without also covering the
// SPA. Paths that must never be throttled are listed in rateLimitExclusions.
//
// The SPA is mounted last: its handler answers whatever the routes above it
// did not claim, and its own not-found rule keeps API and protocol paths from
// being answered with index.html.
func NewRouter(opts Options) chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// The security headers ride every response, refusals included: they are
	// written before the handler runs, at the top of the chain, so no
	// surface — API, protocol, SPA, metrics — answers without them.
	r.Use(middleware.SecurityHeaders)
	// The client facts are captured here rather than per surface: both the
	// REST routes and the procedures read them from the context, and the
	// groups below inherit this chain, so there is one place a fact is
	// gathered instead of one per transport. The address it resolves is the
	// same one the rate limiter keys by, because both read chi's context.
	r.Use(middleware.ClientInfo(opts.Config.Server.TrustedProxyHeaders))
	r.Use(middleware.Logger(opts.Logger))
	r.Use(middleware.Recoverer(opts.Logger))
	r.Use(middleware.CORS(opts.Config.Server.CORS))
	r.Use(middleware.Timeout(opts.Config.Server.WriteTimeout))

	if opts.Metrics != nil {
		r.Handle(opts.Config.OTEL.Metrics.PrometheusPath, opts.Metrics)
	}

	// The rate limiter wraps the routes a client calls, and a static asset
	// or a metrics scrape is outside it: the budget belongs to the API, not
	// to the page that embeds it. The throttled surface is two chi groups —
	// one per transport — because a limited request is refused in the
	// protocol the caller used: the responder envelope on REST, the connect
	// error on RPC. Both groups share one limiter, one policy, and one
	// exclusion list; the groups are what make the limiter cover every
	// mounted route without also covering the SPA. Paths that must never be
	// throttled are listed in rateLimitExclusions.
	//
	// The SPA is mounted last: its handler answers whatever the routes above
	// it did not claim, and its own not-found rule keeps API and protocol
	// paths from being answered with index.html.
	r.Group(func(throttled chi.Router) {
		if opts.RateLimiter != nil {
			throttled.Use(middleware.RateLimit("rest", opts.RateLimiter, restRefuse, httpRateLimitExclusions...))
		}

		throttled.Route("/api", func(api chi.Router) {
			api.Get("/", apiRoot(opts.Config))
			if opts.Checker != nil {
				api.Get("/healthz", health.Handler(opts.Checker))
			}
		})

		// The modules mount inside the group too, so every route a module
		// claims is throttled by the same policy as the API's own. The
		// bearer middleware wraps the modules' REST routes, and the routes
		// the API mounts itself — the root and the health endpoint — stay
		// outside it: they are the surface a monitor reaches.
		throttled.Group(func(mod chi.Router) {
			mod.Use(middleware.RESTBearer(opts.Authenticator, restGuardRules))
			// The upload progress read mounts beside the modules: the same
			// bearer middleware guards it, and the storage engine it reads
			// is infrastructure's to resolve. A container without the
			// engine — a test's bare router — mounts no route, the smaller
			// feature rather than a broken one.
			if opts.Injector != nil {
				if manager, err := do.Invoke[*storage.Manager](opts.Injector); err == nil && manager != nil {
					mod.Get("/api/uploads/*", uploadProgressHandler(manager))
				}
			}
			kernel.Mount(mod, opts.Modules...)
		})
	})

	// The ConnectRPC surface is composed in rpc.go and throttled by the same
	// policy as the REST routes, refused in its own protocol. It mounts
	// before nothing else in its group: a module that claims a path under
	// the RPC prefix would be a defect, and the route-claim detection in
	// kernel.MountRPC reports it at startup rather than answering two
	// handlers for one path.
	r.Group(func(throttled chi.Router) {
		if opts.RateLimiter != nil {
			throttled.Use(middleware.RateLimit("rpc", opts.RateLimiter,
				rpcRefuseWith(opts.Config.Server.MaxRequestBytes), rpcRateLimitExclusions...))
		}

		mountRPC(throttled, opts.Checker, opts.Authenticator, opts.Modules, opts.Config.Server.MaxRequestBytes)
	})

	// The devtool sits outside the throttled and bearer-guarded groups: a
	// debug build serves the samber/do web UI and the TypeID codecs, a
	// release build answers the same paths with a 404 envelope rather than
	// letting the SPA claim them.
	mountDevtool(r, opts.Injector)

	// The uploads are served outside the group: a page that loads an image
	// spends no rate-limit check, the budget belonging to the API a client
	// calls rather than to the assets it renders. It is mounted before the
	// SPA, whose not-found handler would otherwise answer a missing upload
	// with index.html.
	static.Mount(r, static.NewLocal(static.Dir(opts.Config.Storage.LocalPath)))

	web.SetupStatic(r)
	return r
}

// restRefuse answers a limited REST request with the envelope: a 429 whose
// metadata carries the X-RateLimit-* headers the middleware wrote, the shape
// a REST client parses. The Connect surface refuses its calls through
// rpcRefuseWith, in its own protocol.
func restRefuse(w http.ResponseWriter, r *http.Request) {
	responder.Fail(w, r, http.StatusTooManyRequests, "rate limit exceeded")
}
