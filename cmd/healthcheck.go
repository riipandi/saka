package main

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/health"
	"github.com/riipandi/saka/pkg/printext"
)

// exitUnhealthy is the exit code of a failed health check. It is distinct from
// the generic failure code so a script can tell "the service is down" from
// "the command could not run".
const exitUnhealthy = 3

var healthCheckCmd = &cli.Command{
	Name:    "health",
	Usage:   "Check application health status",
	Aliases: []string{"hc"},
	Description: `Checks the application dependencies and reports the aggregated
availability status. The same result is published by the REST handler,
so the CLI and the API always agree.

It checks that Postgres answers and that the application data directory
(storage by default, or storage.local_path from the configuration) exists,
is writable, and is not world-writable. The optional Valkey backend is
checked only while kvstore.enable is on.

Output is text by default; pass --json for a machine-readable result or
--short for the aggregated status alone.

Exit codes: 0 when healthy, 3 when a required component is down, 1 on
a usage error such as a missing DATABASE_URL. A component that cannot
be reached is reported as unhealthy, not as a command failure.

The check needs DATABASE_URL; pass --env-file or export it.`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "short",
			Usage: "Print only the aggregated status: healthy or unhealthy",
		},
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Print the result as JSON instead of text",
		},
		&cli.DurationFlag{
			Name:  "timeout",
			Usage: "Deadline for the whole check",
			Value: health.DefaultTimeout,
		},
		&cli.BoolFlag{
			Name:  "no-cache",
			Usage: "Run every check now instead of reusing a cached result",
		},
	},
	Action: runHealthCheck,
}

// runHealthCheck opens the database pool, runs the checks, and prints the
// result. The pool is opened here and closed on return: the CLI owns it for the
// duration of the command and nothing else uses it.
//
// A database that cannot be reached is a result, not a command error. This
// command exists to report that state, so it prints the report and exits 3
// instead of failing to start.
func runHealthCheck(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	dsn, err := requireDatabaseURL(cfg)
	if err != nil {
		return err
	}

	result := checkHealth(ctx, cmd, cfg, dsn)

	p := printext.NewPalette(cmd.Root().Writer)
	if err := printHealth(p, cmd.Bool("short"), cmd.Bool("json"), result); err != nil {
		return err
	}
	if !result.Healthy() {
		return cli.Exit("", exitUnhealthy)
	}
	return nil
}

// checkHealth opens the pool and runs the checks. A pool that cannot be opened
// becomes an unhealthy result for the database, because the reason it failed is
// exactly what the caller wants to read.
func checkHealth(ctx context.Context, cmd *cli.Command, cfg config.Config, dsn string) health.Result {
	info := map[string]string{
		"name":    config.AppName,
		"version": config.AppVersion,
	}
	uptime := health.Uptime(processStarted)

	started := time.Now()
	// The probe makes a single attempt on purpose: this command exists to
	// report whether the database answers right now, and the report is what a
	// supervisor reads to decide. Waiting would turn a health check into a
	// second startup sequence and hide the state it is meant to publish.
	pool, err := datastore.NewPostgres(ctx, datastore.PostgresOptions{DSN: dsn})
	if err != nil {
		result := health.Failure(health.CheckNameDatabase, err)
		// The time the failed connection attempt took is part of the report:
		// it tells the reader whether the database refused or timed out.
		result.Duration = time.Since(started)
		result.Info = mergeInfo(info, uptime(ctx))
		return result
	}
	defer pool.Shutdown(context.Background())

	checks := []health.Check{
		// The CLI report is read by an operator who owns the machine, so the
		// targets may be named here; the endpoint publishes the plain checks,
		// which carry neither a connection string nor a data directory.
		health.DatabaseCheckWithTarget(pool, config.RedactDSN(dsn)),
		health.StorageCheckWithTarget(dataDir(cfg)),
	}
	// The backend is probed only while it is enabled, the way the server
	// reports it. Opening it here proves the connection up front, so a
	// refused handshake becomes the check's failure instead of the ping's.
	if cfg.KVStore.Enable {
		kv, err := datastore.NewValkey(ctx, datastore.ValkeyOptions{
			URL:             cfg.KVStore.URL,
			DB:              cfg.KVStore.DB,
			ApplicationName: config.AppIdentifier,
		})
		if err != nil {
			checks = append(checks, failedCheck(health.CheckNameKVStore, err))
		} else {
			defer kv.Shutdown(context.Background())
			checks = append(checks, health.KVStoreCheckWithTarget(kv, config.RedactKVURL(cfg.KVStore.URL)))
		}
	}

	options := []health.Option{
		health.WithTimeout(cmd.Duration("timeout")),
		health.WithChecks(checks...),
		health.WithInfo(info),
		health.WithInfoFunc(uptime),
	}
	if cmd.Bool("no-cache") {
		options = append(options, health.WithCacheTTL(0))
	}
	return health.NewChecker(options...).Check(ctx)
}

// failedCheck is a check that always reports the given error, used when a
// dependency could not even be opened: the reason it failed is exactly what
// the report should say.
func failedCheck(name string, err error) health.Check {
	return health.Check{
		Name: name,
		Check: func(context.Context) error {
			return err
		},
	}
}

// mergeInfo combines the static metadata with computed values. The computed
// values go first so a static key wins, matching what the checker does.
func mergeInfo(static, computed map[string]string) map[string]string {
	merged := make(map[string]string, len(static)+len(computed))
	maps.Copy(merged, computed)
	maps.Copy(merged, static)
	return merged
}

// processStarted is when this process began. The health command is one-shot, so
// uptime measures the process it runs in and reads as a fraction of a second;
// the value becomes useful when the same checker is wired into the server.
var processStarted = time.Now()

// printHealth writes the result in the requested format. --short wins over
// --json: a caller that asks for one word must get one word, not a document.
// Every format comes from the health package, so the CLI does not define a
// second rendering that could drift from the one the API publishes.
//
// --json is a machine format, so it is never coloured, even on a terminal.
func printHealth(p printext.Palette, short, asJSON bool, result health.Result) error {
	switch {
	case short:
		return health.WriteShort(p.Writer(), result)
	case asJSON:
		return printHealthJSON(p.Writer(), result)
	default:
		return health.WriteText(p.Writer(), result, p)
	}
}

// printHealthJSON writes the result as JSON for a machine consumer. The output
// is one line, so it can be piped or logged without reformatting.
func printHealthJSON(w io.Writer, result health.Result) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, strings.TrimSpace(string(encoded)))
	return err
}
