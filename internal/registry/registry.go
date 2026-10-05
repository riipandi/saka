// Package registry is the composition root: the one place the shared
// dependencies are registered with samber/do and wired onto each other.
//
// The package is split along one boundary:
//
//   - infrastructure.go registers what the process runs on — the pool, the
//     cache, the queue, the storage engine, the outbound client. It names no
//     module, and importing one there would be visible in a one-line diff.
//   - modules.go registers what the application mounts. An area owns its own
//     wiring and reaches infrastructure only by invoking it from the
//     container, so this file never names an area's internals — it holds the
//     list, and a consumer can append to it.
//   - this file joins the two. The router and the server are the only things
//     that need both, so they are the only ones that name both.
//
// Each half is a `do.Package`, so `do.New` reads as the composition list
// itself: the values the command owns, the infrastructure, the modules.
//
// Services are lazy: the pool and the health checker are built when the first
// service that needs them is invoked, which is the moment serve resolves the
// server. A dependency that cannot be built — a database that is down, a data
// directory that cannot be used — fails that invocation, before the listener
// opens.
package registry

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/fetcher"
	"github.com/riipandi/saka/framework/health"
	fwmailer "github.com/riipandi/saka/framework/mailer"
	fwmiddleware "github.com/riipandi/saka/framework/middleware"
	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/framework/scheduler"
	"github.com/riipandi/saka/framework/storage"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/guard"
	"github.com/riipandi/saka/internal/jobs"
	"github.com/riipandi/saka/internal/transport"
	"github.com/riipandi/saka/internal/transport/middleware"
	"github.com/riipandi/saka/internal/transport/router"
)

// New registers the shared services of a serve run.
//
// The configuration, the logger, and the metrics handler are registered as
// values because the command lifecycle owns them; they are services like any
// other, so a provider below resolves them instead of closing over them.
//
// The areas of this application are mounted, followed by any the caller
// passes. A consumer outside this repository therefore serves its own area
// without editing this package — it appends to the list, and the last one to
// claim a route wins.
func New(ctx context.Context, cfg config.Config, metrics http.Handler, logger *slog.Logger, extra ...Area) *do.RootScope {
	areas := append(Areas(), extra...)

	return do.New(
		do.Eager(&cfg),
		do.Eager(logger),
		do.Eager(metrics),
		infrastructure(ctx),
		areaPackages(areas),
		// The area list is composition input, not a service — it reaches the
		// router through this closure, the way the run's context reaches
		// infrastructure.
		do.Lazy(func(i do.Injector) (chi.Router, error) {
			return newRouter(i, areas)
		}),
		do.Lazy(newServer),
	)
}

// newRouter builds the HTTP surface: the API, the static mount, and the
// modules mounted on the root router.
//
// This is the join point, so it is the one provider that resolves from both
// halves — the health checker and the limiter come from infrastructure, the
// module list from the areas.
func newRouter(i do.Injector, areas []Area) (chi.Router, error) {
	c := do.MustInvoke[*config.Config](i)
	checker := do.MustInvoke[*health.Checker](i)
	log := do.MustInvoke[*slog.Logger](i)
	limiter := do.MustInvoke[fwmiddleware.Limiter](i)
	metrics := do.MustInvoke[http.Handler](i)

	// The authenticator is built here, the join point, because it is the one
	// place a transport need may name an area's service: the RPC surface
	// verifies the tokens the identity area's key service signed, and the
	// transport itself receives only the function, never the service.
	mounted, err := mountAreas(i, areas)
	if err != nil {
		return nil, err
	}

	// The rate-limit classifier is the join of the guard's policy tables and
	// the configuration's budgets: the guard names which procedure counts in
	// which bucket, the configuration gives each bucket its numbers. A path
	// the tables do not name is not counted, so the classified surface is a
	// decision on record rather than a default every request falls into.
	classes := map[string]fwmiddleware.RateClass{
		guard.RateAuth: {
			Name:   guard.RateAuth,
			Policy: fwmiddleware.Policy{Limit: c.RateLimit.AuthLimit, Window: c.RateLimit.Window},
		},
		guard.RateDefault: {
			Name:   guard.RateDefault,
			Policy: fwmiddleware.Policy{Limit: c.RateLimit.Limit, Window: c.RateLimit.Window},
		},
	}
	classify := func(path string) (fwmiddleware.RateClass, bool) {
		bucket, ok := guard.RateBucketFor(path)
		if !ok {
			return fwmiddleware.RateClass{}, false
		}
		class, ok := classes[bucket]
		return class, ok
	}

	return router.NewRouter(router.Options{
		Config:        *c,
		Checker:       checker,
		Metrics:       metrics,
		Logger:        log,
		RateLimiter:   limiter,
		RateClassify:  classify,
		Modules:       mounted,
		Authenticator: do.MustInvoke[middleware.Authenticator](i),
		// The step-up proofs are spent here, once per guarded call. The
		// consumer is registered as the interface the guard defines — the
		// registry never learns which feature mints the tokens.
		Reauthentication: do.MustInvoke[guard.ReauthConsumer](i),
		// The engines' own operational surface reads them the same way: the
		// queue procedures answer from the client, the scheduler ones from
		// its state rows. Their destructive procedures record through the
		// pool and the recorder — the action is a single statement, so the
		// record rides the pool.
		QueueClient: do.MustInvoke[*queue.Client](i),
		Scheduler:   do.MustInvoke[*scheduler.Scheduler](i),
		DB:          do.MustInvoke[*datastore.Postgres](i),
		Audit:       do.MustInvoke[*fwaudit.Recorder](i),
		Injector:    i,
	}), nil
}

// newServer wraps the router in the configured HTTP server.
func newServer(i do.Injector) (*http.Server, error) {
	c := do.MustInvoke[*config.Config](i)
	router := do.MustInvoke[chi.Router](i)
	return transport.NewServer(*c, router), nil
}

// Prewarm resolves every service a serve run blocks on and seeds the
// recurring jobs: the components whose construction fails when a dependency
// is down or a configuration is unusable. The command calls it after New and
// before the listener opens, so such a failure is a failed run carrying the
// service's own message, not a 500 on the first request. The runners resolve
// here too — the scheduler's resolution claims onto a queue whose processors
// were wired when the queue was built — and the router's resolution builds
// the areas, whose Mount validates what they cannot work without. The
// seeding runs last of the queue's steps, against the client it resolves.
func Prewarm(ctx context.Context, i do.Injector) error {
	for _, resolve := range []func(do.Injector) error{
		func(i do.Injector) error { _, err := do.Invoke[*fetcher.Client](i); return err },
		func(i do.Injector) error { _, err := do.Invoke[*fwmailer.Service](i); return err },
		func(i do.Injector) error { _, err := do.Invoke[*queue.Client](i); return err },
		func(i do.Injector) error { _, err := do.Invoke[*scheduler.Scheduler](i); return err },
		func(i do.Injector) error {
			seeder, err := do.Invoke[*jobs.Seeder](i)
			if err != nil {
				return err
			}
			return seeder.Seed(ctx)
		},
		func(i do.Injector) error { _, err := do.Invoke[chi.Router](i); return err },
		func(i do.Injector) error { _, err := do.Invoke[*http.Server](i); return err },
	} {
		if err := resolve(i); err != nil {
			return err
		}
	}
	return nil
}

// Runner is one long-running component of a serve run. Start launches the
// component's work and returns; the component runs until the context the
// command cancels on a signal, or until Stop. Stop is the drain: it waits,
// inside the shutdown window, for the in-flight work to finish and reports
// whether everything did. A nil Stop means the component ends with the run's
// context or hands its drain to the container's shutdown walk.
type Runner struct {
	Name  string
	Start func(ctx context.Context)
	Stop  func(ctx context.Context) bool
}

// Runners are the long-running components of a serve run, in start order, and
// the stop order is the reverse of it.
//
// This list is where the ordering a serve run depends on lives: the queue is
// first, so the scheduler's first tick claims into a running dispatcher; the
// scheduler stops before the listener drains, so no fire starts while the
// listener is closing and a fire in flight joins the queue's own drain; the
// staging watcher runs only when it is switched on, because a run that does
// not watch stages nothing.
func Runners(i do.Injector) ([]Runner, error) {
	queueClient, err := do.Invoke[*queue.Client](i)
	if err != nil {
		return nil, err
	}
	jobScheduler, err := do.Invoke[*scheduler.Scheduler](i)
	if err != nil {
		return nil, err
	}

	runners := []Runner{
		// The queue outlives the listener, so its drain is the container's
		// shutdown walk: Shutdown is what the injector calls, after the
		// HTTP drain ends.
		{Name: "queue", Start: queueClient.Start},
		// The scheduler fires onto the queue, and a fire in flight finishes
		// its enqueue here — an enqueued task is durable, so the queue's own
		// drain after this cannot lose one.
		{Name: "scheduler", Start: jobScheduler.Start, Stop: jobScheduler.Stop},
	}

	// The boot-time sweep re-enqueues the staging uploads a dead run left
	// behind — the recovery the staging watcher's startup scan used to
	// carry, read from the manifest instead of walked off the filesystem.
	// It runs once and ends; its Start is synchronous because the work is
	// short. A failure here is the run's failure, the same way a queue
	// that cannot be built is.
	manager, err := do.Invoke[*storage.Manager](i)
	if err != nil {
		return nil, err
	}
	client := do.MustInvoke[*queue.Client](i)
	log := do.MustInvoke[*slog.Logger](i)
	runners = append(runners, Runner{
		Name: "upload sweep",
		Start: func(ctx context.Context) {
			if _, err := jobs.SweepUploads(ctx, manager, client, log); err != nil {
				log.ErrorContext(ctx, "serve: upload sweep failed", "err", err)
				panic(err)
			}
		},
	})
	return runners, nil
}

// uptime is the computed health metadata: how long the process has been up.
func uptime(_ context.Context) map[string]string {
	return map[string]string{"uptime": time.Since(startTime).Round(time.Second).String()}
}

// startTime is when this process began, read once so every health report
// measures the same clock.
var startTime = time.Now()
