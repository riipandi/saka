package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/samber/do/v2"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/registry"
)

var serveCmd = &cli.Command{
	Name:  "serve",
	Usage: "Start the application server",
	Description: `Starts the application HTTP server.
--host, --port, and --base-url override the config file for this run,
so a value can be tried without editing the file. Without them the
file decides.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:        "host",
			Usage:       "Host to bind to",
			DefaultText: "0.0.0.0",
		},
		&cli.UintFlag{
			Name:        "port",
			Usage:       "Port to bind to",
			DefaultText: "3000",
		},
		&cli.StringFlag{
			Name:  "base-url",
			Usage: "Base URL (e.g. http://localhost:3000)",
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		// serve is the one command that uses most of the configuration, so it is
		// where the whole configuration is checked: a server that starts on a
		// half-configured setup fails later, in a place far from the mistake.
		cfg, err := fullConfigFrom(ctx)
		if err != nil {
			return err
		}

		// The process logger is built here, before anything else runs, so every
		// line the server emits from now on goes through the configured
		// transports. It is closed by the root After, which flushes whatever is
		// still queued.
		log, err := loggerFrom(ctx)
		if err != nil {
			return err
		}

		// The observer is built beside the logger, for the same reason: a signal
		// that is switched on has to be reporting before the first request
		// arrives, and its queues are drained by the root After.
		obs, err := observerFrom(ctx)
		if err != nil {
			return err
		}

		// The container wires the pool, the health checker, the queue, and the
		// server, so the command only names what it blocks on. Prewarm
		// resolves every service the run depends on — the pool, the mailer,
		// the queue with its jobs seeded, the areas' wiring — so a
		// configuration or a dependency one of them cannot work with fails
		// the run here, before the listener opens.
		injector := registry.New(ctx, cfg, obs.MetricsHandler(), log.Slog())
		err = registry.Prewarm(ctx, injector)
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}

		// The runners are the long-running components of the run, in start
		// order: the queue, the scheduler, and the staging watcher when it is
		// enabled. The order and each runner's drain live in the registry.
		runners, err := registry.Runners(injector)
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		for _, runner := range runners {
			runner.Start(ctx)
		}

		// The listener is the one service the command resolves by name: it is
		// what the run blocks on, and its address is what the log reports.
		server, err := do.Invoke[*http.Server](injector)
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}

		// The console-less echo: a deployment that ships its logs elsewhere
		// still gets the milestones on its terminal. The logger's own echo
		// sink carries the warnings and errors; these two lines are the start
		// and the end a watcher looks for, written directly because they are
		// informational and must not pretend to be part of the log stream.
		quiet := !slices.Contains(cfg.Log.Transport, config.LogTransportConsole)
		echo := func(format string, args ...any) {
			if quiet {
				fmt.Fprintf(os.Stderr, "%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
			}
		}

		serveErr := make(chan error, 1)
		go func() {
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- err
			}
		}()
		log.Slog().InfoContext(ctx, "starting",
			"mode", cfg.App.Mode,
			"transport", cfg.Log.Transport,
			"tracing", obs.Tracing(),
			"metrics", obs.Metrics(),
			"workers", cfg.Queue.NumWorkers,
			"address", server.Addr)
		echo("serving on %s (mode %s, logs to %s)", server.Addr, cfg.App.Mode, strings.Join(cfg.Log.Transport, ","))

		select {
		case err := <-serveErr:
			return err
		case <-ctx.Done():
		}

		// The drain window bounds everything the run waits for at shutdown:
		// the scheduler's fires, the listener's requests, the queue's tasks.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.Server.ShutdownTimeout)
		defer cancel()

		// The runners stop in reverse start order, inside the drain window. A
		// runner without a Stop drains elsewhere: the queue through the
		// container's shutdown walk after the listener closes, the watcher
		// through the run's context the caller cancelled.
		for _, runner := range slices.Backward(runners) {
			if runner.Stop == nil {
				continue
			}
			if !runner.Stop(shutdownCtx) {
				log.Slog().WarnContext(ctx, "serve: runner left work running", "runner", runner.Name)
			}
		}

		// Shutdown closes the listener first, so a request that arrives during
		// the drain is refused rather than served by a server the caller gave
		// up on, then waits for the in-flight work, bounded by the configured
		// drain window.
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("serve: drain: %w", err)
		}

		// The pool outlives the listener, so it is closed after the drain: a
		// request still finishing needs its connection until the drain ends.
		if report := injector.ShutdownWithContext(shutdownCtx); report != nil && !report.Succeed {
			slog.ErrorContext(ctx, "serve: release", "err", report.Error())
		}

		// The closing line is what proves the drain finished: a run that
		// prints it released the listener, the scheduler, the pool, and the
		// queue, so a service that exits without it stopped the hard way.
		log.Slog().InfoContext(ctx, "shutdown complete", "address", server.Addr)
		echo("shutdown complete on %s", server.Addr)
		return nil
	},
}
