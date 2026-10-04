package queue

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// defaultScope is the instrumentation scope a caller that names none gets: a
// scope identifies the instrumenting code, and this package's word is the
// honest default.
const defaultScope = "queue"

// The outcomes a settled task carries on the processed instrument. A task
// that succeeded left the queue, one still holding attempts went back for its
// backoff, and one that exhausted them is dead — the archive's replay stock.
const (
	outcomeSuccess      = "success"
	outcomeRetry        = "retry"
	outcomeDead         = "dead"
	outcomeUnknownQueue = "unknown_queue"
)

// taskMetrics holds the queue's instruments.
//
// The client builds one at construction from the global meter provider, which
// is real when the observer is up and the SDK's no-op when metrics are off —
// recording then costs a nil check, which is the honest cost of a signal
// nobody asked for. A build error is a programming bug — a bad instrument
// name — and panics the way Register panics on a wiring bug.
type taskMetrics struct {
	enqueued  metric.Int64Counter
	processed metric.Int64Counter
	duration  metric.Float64Histogram
	attempts  metric.Int64Histogram
	claimErrs metric.Int64Counter
}

// instrumentName prefixes a bare instrument name with the options' telemetry
// namespace. An empty namespace leaves the domain name bare: the series name
// is the caller's identity decision, not the framework's.
func instrumentName(cfg ClientConfig, name string) string {
	if cfg.TelemetryNamespace == "" {
		return name
	}
	return cfg.TelemetryNamespace + "." + name
}

func newTaskMetrics(cfg ClientConfig) *taskMetrics {
	scope := cfg.Scope
	if scope == "" {
		scope = defaultScope
	}
	meter := otel.Meter(scope)
	m := &taskMetrics{}
	var err error
	if m.enqueued, err = meter.Int64Counter(instrumentName(cfg, "queue.tasks.enqueued"),
		metric.WithDescription("Tasks inserted into the pending table"),
		metric.WithUnit("{task}")); err != nil {
		panic("queue: " + err.Error())
	}
	if m.processed, err = meter.Int64Counter(instrumentName(cfg, "queue.tasks.processed"),
		metric.WithDescription("Tasks settled by the dispatcher, by outcome"),
		metric.WithUnit("{task}")); err != nil {
		panic("queue: " + err.Error())
	}
	if m.duration, err = meter.Float64Histogram(instrumentName(cfg, "queue.task.duration"),
		metric.WithDescription("Task execution duration"),
		metric.WithUnit("s")); err != nil {
		panic("queue: " + err.Error())
	}
	if m.attempts, err = meter.Int64Histogram(instrumentName(cfg, "queue.task.attempts"),
		metric.WithDescription("Attempts a settled task consumed"),
		metric.WithUnit("{attempt}"),
		metric.WithExplicitBucketBoundaries(1, 2, 3, 4, 5, 10)); err != nil {
		panic("queue: " + err.Error())
	}
	if m.claimErrs, err = meter.Int64Counter(instrumentName(cfg, "queue.claims.failed"),
		metric.WithDescription("Failed claims: the dispatcher could not read the pending table"),
		metric.WithUnit("{error}")); err != nil {
		panic("queue: " + err.Error())
	}
	return m
}

// recordEnqueued counts one task that reached the pending table.
func (m *taskMetrics) recordEnqueued(ctx context.Context, queue string) {
	m.enqueued.Add(ctx, 1, metric.WithAttributes(attribute.String("queue", queue)))
}

// recordClaimError counts one failed claim round.
func (m *taskMetrics) recordClaimError(ctx context.Context) {
	m.claimErrs.Add(ctx, 1)
}

// recordOutcome counts a settled task and its cost: the execution duration and
// the attempts the task consumed to reach that outcome.
func (m *taskMetrics) recordOutcome(ctx context.Context, queue, outcome string, duration time.Duration, attempts int) {
	m.processed.Add(ctx, 1, metric.WithAttributes(
		attribute.String("queue", queue),
		attribute.String("outcome", outcome),
	))
	m.duration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("queue", queue)))
	m.attempts.Record(ctx, int64(attempts), metric.WithAttributes(attribute.String("queue", queue)))
}
