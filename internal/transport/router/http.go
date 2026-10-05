package router

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/framework/kernel"
	fwmiddleware "github.com/riipandi/saka/framework/middleware"
	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/framework/scheduler"
	"github.com/riipandi/saka/framework/storage"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/guard"
	"github.com/riipandi/saka/internal/transport/devtools"
	"github.com/riipandi/saka/internal/transport/handler"
	"github.com/riipandi/saka/internal/transport/middleware"
	appweb "github.com/riipandi/saka/web"
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
	RateLimiter fwmiddleware.Limiter
	// RateClassify answers the bucket a request path is counted under, or
	// false when the limiter never counts it. A nil classifier counts
	// nothing, which is the state a bare test router is in — the limiter's
	// counted surface is the guard's decision, wired here.
	RateClassify fwmiddleware.Classifier
	// Modules are the feature modules whose routes and procedures the server
	// mounts.
	Modules []kernel.Module
	// Authenticator authenticates the RPC surface's requests before their
	// procedures run. A nil authenticator leaves the surface open, which is
	// the state a test that reads only responses is in.
	Authenticator middleware.Authenticator
	// Reauthentication spends the step-up proof a Reauthenticated procedure
	// demands. A nil enforcer fails closed: the guarded procedures refuse,
	// which is the state a bare test router is in.
	Reauthentication guard.ReauthConsumer
	// QueueClient is the embedded task queue the administrative surface
	// reads and acts on. A nil client mounts the queue procedures but every
	// one answers `unavailable` — the state a test that never touches the
	// engine is in.
	QueueClient *queue.Client
	// Scheduler is the durable cron scheduler the administrative surface
	// reads and triggers. A nil scheduler answers the same way.
	Scheduler *scheduler.Scheduler
	// DB is the query surface the queue and scheduler procedures write their
	// audit records through. A nil surface skips recording — the state a
	// bare test router is in — because a record is a side effect of the
	// action, never its condition.
	DB datastore.Querier
	// Audit writes the records the queue and scheduler's destructive
	// procedures leave behind. A nil recorder writes none, which is the same
	// bare-test state.
	Audit *fwaudit.Recorder
	// Injector is the samber/do container the run composed. Only the debug
	// build's devtool reads it; a release build ignores the field.
	Injector do.Injector
}

// The paths the limiter never counts: the namespaces the two surfaces answer
// are disjoint, so this list carries only the paths the REST surface is
// spared for. A prefix matches the paths under it. The limiter itself runs on
// the API surfaces only, so a static asset or a metrics scrape never reaches
// a check in the first place.
var httpRateLimitExclusions = []string{
	"/api/healthz", // liveness probes and load-balancer checks
}

// restGuardRules is the authorization policy the REST surface's middleware
// applies, read from internal/guard: the same table the RPC surface's guard
// interceptor reads, so a route cannot be public for the authenticator and
// guarded for the guard.
var restGuardRules = guard.RestRules

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

	r.Use(fwmiddleware.RequestID)
	// The security headers ride every response, refusals included: they are
	// written before the handler runs, at the top of the chain, so no
	// surface — API, protocol, SPA, metrics — answers without them.
	r.Use(fwmiddleware.SecurityHeaders)
	// The client facts are captured here rather than per surface: both the
	// REST routes and the procedures read them from the context, and the
	// groups below inherit this chain, so there is one place a fact is
	// gathered instead of one per transport. The address it resolves is the
	// same one the rate limiter keys by, because both read chi's context.
	r.Use(middleware.ClientInfo(opts.Config.Server.TrustedProxyHeaders))
	r.Use(fwmiddleware.Logger(opts.Logger))
	r.Use(fwmiddleware.Recoverer(opts.Logger))
	r.Use(fwmiddleware.CORS(fwmiddleware.CORSOptions{
		AllowedOrigins:   opts.Config.Server.CORS.AllowedOrigins,
		AllowedMethods:   opts.Config.Server.CORS.AllowedMethods,
		AllowedHeaders:   opts.Config.Server.CORS.AllowedHeaders,
		ExposedHeaders:   opts.Config.Server.CORS.ExposedHeaders,
		AllowCredentials: opts.Config.Server.CORS.AllowCredentials,
		MaxAge:           opts.Config.Server.CORS.MaxAge,
	}))
	r.Use(fwmiddleware.Timeout(opts.Config.Server.WriteTimeout))

	if opts.Metrics != nil {
		r.Handle(opts.Config.OTEL.Metrics.PrometheusPath, opts.Metrics)
	}

	// The rate limiter wraps the routes a client calls, and a static asset
	// or a metrics scrape is outside it: the budget belongs to the API, not
	// to the page that embeds it. The throttled surface is two chi groups —
	// one per transport — because a limited request is refused in the
	// protocol the caller used: the responder envelope on REST, the connect
	// error on RPC. Both groups share one limiter, one classifier, and one
	// exclusion list; the classifier is what decides which paths cost a
	// check and which bucket each counted one spends from. Paths that must
	// never be throttled are listed in rateLimitExclusions.
	//
	// The SPA is mounted last: its handler answers whatever the routes above
	// it did not claim, and its own not-found rule keeps API and protocol
	// paths from being answered with index.html.
	r.Group(func(throttled chi.Router) {
		if opts.RateLimiter != nil {
			throttled.Use(fwmiddleware.RateLimit(fwmiddleware.RateLimitOptions{
				Surface: "rest", Limiter: opts.RateLimiter, Refuse: restRefuse,
				Classify: opts.RateClassify, Excluded: httpRateLimitExclusions, TelemetryNamespace: config.AppIdentifier,
			}))
		}

		throttled.Route("/api", func(api chi.Router) {
			api.Get("/", handler.APIRoot(opts.Config))
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
			// The tus uploads are the one surface a chunked transfer
			// rides: a PATCH is one chunk of a whole whose travel time no
			// unary deadline can bound, so the family is lifted out of the
			// request deadlines above it.
			mod.Use(fwmiddleware.UnboundedForPrefix("/api/uploads"))
			// The uploads mount beside the modules: the same bearer
			// middleware guards them, and the engine they drive is
			// infrastructure's to resolve. A container without the engine
			// — a test's bare router — mounts no route, the smaller
			// feature rather than a broken one.
			if opts.Injector != nil {
				if manager, err := do.Invoke[*storage.Manager](opts.Injector); err == nil && manager != nil {
					log := do.MustInvoke[*slog.Logger](opts.Injector)
					// One handler serves both routes: the per-upload locks
					// the creation-with-upload append and the PATCHes race
					// over live on the instance, not on the route.
					tus := storage.NewTusHandler(manager, log)
					mod.Handle("/api/uploads", tus)
					mod.Handle("/api/uploads/*", tus)
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
			throttled.Use(fwmiddleware.RateLimit(fwmiddleware.RateLimitOptions{
				Surface: "rpc", Limiter: opts.RateLimiter,
				Refuse:   rpcRefuseWith(opts.Config.Server.MaxRequestBytes),
				Classify: opts.RateClassify, Excluded: rpcRateLimitExclusions, TelemetryNamespace: config.AppIdentifier,
			}))
		}

		mountRPC(throttled, opts)
	})

	// The devtool sits outside the throttled and bearer-guarded groups: a
	// debug build serves the samber/do web UI and the TypeID codecs, a
	// release build answers the same paths with a 404 envelope rather than
	// letting the SPA claim them.
	devtools.Mount(r, opts.Injector)

	// Stored files are served outside the group: a page that loads an image
	// spends no rate-limit check, the budget belonging to the API a client
	// calls rather than to the assets it renders. It is mounted before the
	// SPA, whose not-found handler would otherwise answer a missing object
	// with index.html. A run without the storage engine mounts nothing —
	// the SPA keeps the paths.
	if opts.Injector != nil {
		if manager, err := do.Invoke[*storage.Manager](opts.Injector); err == nil && manager != nil {
			// A run without the signer still mounts: public files serve,
			// private ones fail closed at the handler.
			signer, _ := do.Invoke[*storage.Signer](opts.Injector)
			handler.MountStorage(r, manager, signer)
		}
	}

	// The SPA mounts last and renders the Go shell; the debug build points
	// its fragment at the Vite dev server, the release build resolves its
	// tags from the embedded manifest.
	appweb.SetupStatic(r)
	return r
}

// restRefuse answers a limited REST request with the envelope: a 429 whose
// metadata carries the X-RateLimit-* headers the middleware wrote, the shape
// a REST client parses. The Connect surface refuses its calls through
// rpcRefuseWith, in its own protocol.
func restRefuse(w http.ResponseWriter, r *http.Request) {
	webutil.Fail(w, r, http.StatusTooManyRequests, "rate limit exceeded")
}
