package main

import (
	"context"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/pkg/printext"
)

// otelSmokeMarker is the attribute and metric label the smoke test attaches to
// what it emits. It is fixed so the command's own output can tell a reader which
// query to run, and so a second run is distinguishable from the first only by
// its timestamp.
const otelSmokeMarker = "tango-otel-smoke"

var otelSmokeCmd = &cli.Command{
	Name:     "otel:smoke",
	Category: "Development commands",
	Usage:    "Emit one span and one measurement through the configured signals",
	Description: `Records a span and a measurement through the providers the configuration
enables, so tracing and metrics can be proved end to end against a real backend
before anything depends on them.

It prints where each signal is exported and the query that reads it back:

  task metrics:up
  OTEL_TRACING_ENABLE=true OTEL_METRICS_ENABLE=true \
  task metrics:smoke:otel

A signal that is switched off is reported as off and nothing is recorded for it.
Nothing is written to the configuration; this command only reads it.`,
	Action: runOTELSmoke,
}

// runOTELSmoke records one span and one measurement and drains them.
//
// It builds the observer through the same accessor a command uses, so the run
// proves the wiring a real command gets, not a pipeline assembled for the test.
func runOTELSmoke(ctx context.Context, cmd *cli.Command) error {
	p := printext.NewPalette(cmd.Root().Writer)

	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	obs, err := observerFrom(ctx)
	if err != nil {
		return err
	}

	if err := printOTELPlan(p, cfg); err != nil {
		return err
	}

	started := time.Now()

	// A span with an attribute rather than a bare one, so a query has something
	// to match on: the trace store is shared with every other run.
	if obs.Tracing() {
		_, span := obs.Tracer().Tracer(config.AppIdentifier).Start(ctx, "otel smoke",
			trace.WithAttributes(attribute.String("marker", otelSmokeMarker)))
		span.End()
	}

	// A counter, not a gauge: a counter is what a rate query reads, and it is
	// the instrument a service is most likely to record on a request path.
	if obs.Metrics() {
		counter, err := obs.Meter().Meter(config.AppIdentifier).Int64Counter(
			"tango_otel_smoke_total",
			metric.WithDescription("Measurements emitted by otel:smoke"),
		)
		if err != nil {
			return err
		}
		counter.Add(ctx, 1, metric.WithAttributes(attribute.String("marker", otelSmokeMarker)))
	}

	// What was recorded is captured before the drain, because a shutdown
	// releases the providers and a check afterwards would read them as off.
	var spans, measurements int
	if obs.Tracing() {
		spans = 1
	}
	if obs.Metrics() {
		measurements = 1
	}

	// The queues are drained here rather than left to the root After, so a
	// signal that failed to ship is reported by this command and not as a
	// failure of whatever ran next.
	if err := closeObserver(ctx); err != nil {
		return err
	}

	if err := printFields(p, []field{{"elapsed", p.Dim(printext.Duration(time.Since(started)))}}); err != nil {
		return err
	}

	// The summary counts what was actually recorded, so a run with a signal off
	// does not claim to have proved it.
	return printStatusLine(p, "recorded %d %s, %d %s",
		spans, printext.Plural(spans, "span"),
		measurements, printext.Plural(measurements, "measurement"))
}

// printOTELPlan reports what each signal is doing, so the command's output is
// the instruction for reading the result back.
func printOTELPlan(p printext.Palette, cfg config.Config) error {
	signal := func(enabled bool) string {
		if enabled {
			return "on"
		}
		return "off"
	}

	if err := printFields(p, []field{
		{"endpoint", cfg.OTEL.Endpoint},
		{"service", cfg.OTEL.ServiceName},
		{"tracing", signal(cfg.OTEL.Tracing.Enable)},
		{"metrics", signal(cfg.OTEL.Metrics.Enable)},
	}); err != nil {
		return err
	}

	if !cfg.OTEL.Tracing.Enable && !cfg.OTEL.Metrics.Enable {
		return p.Printf("\n%s\n", p.Dim(
			"no signal is enabled: set otel.tracing.enable or otel.metrics.enable"))
	}

	return p.Printf("\n%s\n", p.Dim(fmt.Sprintf(
		"read it back: task metrics:query -- 'marker:%q' (logs) or the Perses datasources (traces, metrics)",
		otelSmokeMarker)))
}
