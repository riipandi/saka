// Package observer wires the process's traces and metrics.
//
// It follows the same shape as framework/logger: the resolved configuration is
// the only source of truth, each signal is opt-in, and a signal nothing enables
// dials nothing. Logging is not here — framework/logger owns it — but the three
// signals share one collector address and one resource, so the two packages
// describe the same service from the same configuration. The exporter plumbing
// all of them need (transport credentials, TLS, compression, signal routes)
// lives here in exporter.go, and the logger's OTLP sink consumes it rather
// than carrying a copy.
//
// # Nothing on the request path blocks
//
// Every signal is exported from an in-memory queue drained by a background
// goroutine: spans go through a batch span processor, measurements through a
// periodic reader. Recording a span or a measurement enqueues and returns, so a
// collector that is slow, unreachable, or absent costs dropped telemetry and
// never a slow request. When a queue is full the oldest item is dropped and the
// SDK counts it; nothing here waits for room.
//
// # The environment is not a source
//
// The OpenTelemetry SDK reads OTEL_* variables on its own — OTEL_EXPORTER_OTLP_*,
// OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES, OTEL_SDK_DISABLED — which would
// be a second configuration source deciding what this process reports and
// where. The configuration file is the single source of truth, so every exporter
// option and every resource attribute is passed explicitly here. A stray export
// in a shell must not be able to redirect telemetry or rename the service.

package observer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The protocol's own routes. A configuration that names no path for a signal
// gets these, which is what a collector serves; a deployment pointing straight
// at a backend whose route differs sets the path instead.
const (
	otlpTracesPath  = "/v1/traces"
	otlpMetricsPath = "/v1/metrics"
)

// New builds the observer the configuration describes.
//
// It fails rather than degrading: a signal that cannot be built is an error, not
// a silent downgrade to the no-op provider, because a deployment that asked for
// traces or metrics and did not get them has lost the telemetry it is being
// trusted with. Nothing is left open when it fails part way through.
//
// A configuration with every signal off returns a usable Observer with nothing
// switched on, so a caller never has to check whether it may call Shutdown.
func New(ctx context.Context, opts Options) (*Observer, error) {
	opts = opts.withDefaults()
	o := &Observer{}
	if !opts.Tracing.Enable && !opts.Metrics.Enable {
		return o, nil
	}

	// Exporter errors go through the same slog pipeline as everything else the
	// process writes. The SDK's own handler prints to stderr through log.Print,
	// which would leave a dead collector invisible to every configured
	// transport — in a program whose whole thesis is one pipeline. Where no
	// logger was installed, slog's default writes to stderr, which is still
	// where a person looks first.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Error("otel exporter", "err", err.Error())
	}))

	res := newResource(opts)

	// Traces first, metrics second: Shutdown drains in reverse, so the metric
	// provider is stopped before the tracer provider it does not depend on.
	if opts.Tracing.Enable {
		tracer, err := newTracerProvider(ctx, opts, res)
		if err != nil {
			return nil, errors.Join(err, o.Shutdown(context.WithoutCancel(ctx)))
		}
		o.tracer = tracer
	}

	if opts.Metrics.Enable {
		meter, err := newMeterProvider(ctx, opts, res, o)
		if err != nil {
			return nil, errors.Join(err, o.Shutdown(context.WithoutCancel(ctx)))
		}
		o.meter = meter
	}

	return o, nil
}

// withDefaults fills the zero values the exporters cannot mean: a zero queue
// would hold nothing, a zero batch would export nothing, and a zero interval
// would never fire. A caller that wants a different number names it; a caller
// that leaves it out gets the size a collector-facing pipeline needs. The
// zero-value Options stay constructible — composability (decision 16) — but
// never silently disabled.
func (o Options) withDefaults() Options {
	if o.QueueMaxSize == 0 {
		o.QueueMaxSize = 4096
	}
	if o.Tracing.MaxBatchSize == 0 {
		o.Tracing.MaxBatchSize = 512
	}
	if o.Tracing.BatchTimeout == 0 {
		o.Tracing.BatchTimeout = 5 * time.Second
	}
	if o.Tracing.ExportTimeout == 0 {
		o.Tracing.ExportTimeout = 10 * time.Second
	}
	if o.Metrics.Interval == 0 {
		o.Metrics.Interval = 60 * time.Second
	}
	if o.Metrics.ExportTimeout == 0 {
		o.Metrics.ExportTimeout = 10 * time.Second
	}
	return o
}

// samplerFor returns the sampler the options name.
//
// A sampler decides which traces are recorded, so it is the one setting that
// bounds the cost of tracing: `never` records nothing, and a ratio records a
// fraction. The parent-ratio form follows the decision of a parent span for a
// trace that was already sampled, so a service that receives a sampled request
// keeps the trace whole rather than sampling its own half away. An unknown
// name records everything, which is the loud answer.
func samplerFor(tracing Tracing) sdktrace.Sampler {
	switch tracing.Sampler {
	case SamplerNever:
		return sdktrace.NeverSample()
	case SamplerRatio:
		return sdktrace.TraceIDRatioBased(tracing.Ratio)
	case SamplerParentRatio:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(tracing.Ratio))
	default:
		return sdktrace.AlwaysSample()
	}
}
