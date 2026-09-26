package main

import (
	"context"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/logger"
	"github.com/riipandi/tango/pkg/printext"
)

// smokeMarker is the field the smoke test attaches to the line it emits. It is
// fixed so the command's own output can tell a reader which query to run, and so
// a second run is distinguishable from the first only by its timestamp.
const smokeMarker = "tango-logger-smoke"

var loggerSmokeCmd = &cli.Command{
	Name:  "logger:smoke",
	Usage: "Emit one log line through every configured transport",
	Description: `Writes one line at each configured log level through the transports named by
log.transport, so the logging path can be proved end to end against a real
backend before anything depends on it.

It prints where each transport writes and the query that reads the line back,
so a run against a local VictoriaLogs can be checked without opening Perses:

  task metrics:up
  LOG_TRANSPORT=console,file,otlp \
  OTEL_ENDPOINT=http://localhost:4318 \
  task metrics:smoke

Nothing is written to the configuration; this command only reads it.`,
	Action: runLoggerSmoke,
}

// runLoggerSmoke builds the configured logger, emits the probe lines, and flushes
// them.
//
// It builds the logger itself rather than using loggerFrom, because the point is
// to report a transport that cannot be built: loggerFrom would hand back the same
// error, but the command that exists to diagnose logging should not fail before
// it can say which transport broke.
func runLoggerSmoke(ctx context.Context, cmd *cli.Command) error {
	p := printext.NewPalette(cmd.Root().Writer)

	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	err = printSmokePlan(p, cfg)
	if err != nil {
		return err
	}

	log, err := logger.New(cfg, logger.WithWriter(cmd.Root().Writer))
	if err != nil {
		return err
	}

	// The marker is what makes the line findable: the store is shared with every
	// other run, so the query names this field rather than looking for an empty
	// store. The level is carried by the entry itself, so the probe field only
	// says which line this is — naming it "level" would collide with the
	// structured transport's own level key.
	//
	// Each call is written out rather than looped, so the rendered source line
	// differs per level: that is one of the things this command exists to check.
	//
	// The logger is bound once rather than reached through log.Slog() at every
	// call, which is what a caller does when it emits more than one line.
	sl := log.Slog()
	started := time.Now()
	sl.DebugContext(ctx, "logger smoke", "marker", smokeMarker, "probe", "debug")
	sl.InfoContext(ctx, "logger smoke", "marker", smokeMarker, "probe", "info")
	sl.WarnContext(ctx, "logger smoke", "marker", smokeMarker, "probe", "warn")
	sl.ErrorContext(ctx, "logger smoke", "marker", smokeMarker, "probe", "error")

	// The queue is flushed here rather than left to the root After, so a
	// transport that failed to ship is reported by this command and not as a
	// failure of whatever ran next.
	if err := log.Shutdown(ctx); err != nil {
		return err
	}

	if err := printFields(p, []field{{"elapsed", p.Dim(printext.Duration(time.Since(started)))}}); err != nil {
		return err
	}
	return printStatusLine(p, "emitted %d %s", smokeLines, printext.Plural(smokeLines, "line"))
}

// smokeLines is how many probe lines the command emits: one per level the
// configuration accepts, so a level that is filtered out is visible as a missing
// line rather than as a silent drop.
const smokeLines = 4

// printSmokePlan reports where each configured transport writes, so the command's
// output is the instruction for reading the line back.
func printSmokePlan(p printext.Palette, cfg config.Config) error {
	if err := printFields(p, []field{
		{"transport", fmt.Sprintf("%v", cfg.Log.Transport)},
		{"level", cfg.Log.Level},
		{"format", cfg.Log.Format},
	}); err != nil {
		return err
	}

	var targets []field
	for _, name := range cfg.Log.Transport {
		switch name {
		case config.LogTransportConsole:
			targets = append(targets, field{"console", "this terminal"})
		case config.LogTransportFile:
			targets = append(targets, field{"file", logger.LogFilePath(cfg)})
		case config.LogTransportOTLP:
			targets = append(targets, field{"otlp", cfg.OTEL.Endpoint})
		}
	}
	if err := printFields(p, targets); err != nil {
		return err
	}
	return p.Printf("\n%s\n", p.Dim(fmt.Sprintf(
		"read it back: task metrics:query -- 'marker:%q'", smokeMarker)))
}
