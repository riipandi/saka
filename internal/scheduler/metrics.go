package scheduler

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meterName is the instrumentation scope every scheduler instrument is
// registered under, rendered into otel_scope_name by the bridge.
const meterName = "github.com/riipandi/saka/internal/scheduler"

// The outcomes a fire carries on saka.scheduler.ticks. A claimed tick
// advanced next_due and enqueued its task; a skipped one was fired by another
// replica first — that is the claim design working, not a miss.
const (
	tickClaimed  = "claimed"
	tickSkipped  = "skipped"
	tickError    = "error"
	tickReseeded = "reseeded"
)

// schedulerMetrics holds the scheduler's instruments. The scheduler builds
// one at construction from the global meter provider, which is real when the
// observer is up and the SDK's no-op when metrics are off.
type schedulerMetrics struct {
	ticks    metric.Int64Counter
	duration metric.Float64Histogram
	reseeds  metric.Int64Counter
}

func newSchedulerMetrics() *schedulerMetrics {
	meter := otel.Meter(meterName)
	m := &schedulerMetrics{}
	var err error
	if m.ticks, err = meter.Int64Counter("saka.scheduler.ticks",
		metric.WithDescription("Cron ticks by outcome: claimed here, or already claimed by another replica"),
		metric.WithUnit("{tick}")); err != nil {
		panic("scheduler: " + err.Error())
	}
	if m.duration, err = meter.Float64Histogram("saka.scheduler.tick.duration",
		metric.WithDescription("Time one fire spent claiming and enqueueing"),
		metric.WithUnit("s")); err != nil {
		panic("scheduler: " + err.Error())
	}
	if m.reseeds, err = meter.Int64Counter("saka.scheduler.reseeds",
		metric.WithDescription("State rows rebuilt after being removed under the schedule"),
		metric.WithUnit("{job}")); err != nil {
		panic("scheduler: " + err.Error())
	}
	return m
}

// recordTick counts one fire and how long it took.
func (m *schedulerMetrics) recordTick(ctx context.Context, job, outcome string, duration time.Duration) {
	m.ticks.Add(ctx, 1, metric.WithAttributes(
		attribute.String("job", job),
		attribute.String("outcome", outcome),
	))
	m.duration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("job", job)))
}

// recordReseed counts one state row rebuilt.
func (m *schedulerMetrics) recordReseed(ctx context.Context, job string) {
	m.reseeds.Add(ctx, 1, metric.WithAttributes(attribute.String("job", job)))
}
