package observer

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otlpbridge "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/riipandi/saka/internal/config"
)

// Observer is the process's telemetry: the providers that are switched on, held
// together so Shutdown drains every queue they own.
type Observer struct {
	tracer *sdktrace.TracerProvider
	meter  *metric.MeterProvider
	// registry is what the Prometheus exposition is served from. It is held so
	// MetricsHandler can serve the same registry the bridge writes into.
	registry *prometheus.Registry
}

// Shutdown flushes every queued span and measurement and stops the exporters.
//
// It is safe to call more than once. The order matters: providers are shut down
// in reverse of the order they were built, so a signal that was enabled last is
// drained first, and the metric provider — which the Prometheus bridge reads
// through — is stopped before the tracer provider it does not depend on.
func (o *Observer) Shutdown(ctx context.Context) error {
	var errs []error
	if o.meter != nil {
		if err := o.meter.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("observer: meter shutdown: %w", err))
		}
		o.meter = nil
	}
	if o.tracer != nil {
		if err := o.tracer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("observer: tracer shutdown: %w", err))
		}
		o.tracer = nil
	}
	return errors.Join(errs...)
}

// Tracing reports whether spans are being collected.
func (o *Observer) Tracing() bool { return o != nil && o.tracer != nil }

// Metrics reports whether metrics are being collected.
func (o *Observer) Metrics() bool { return o != nil && o.meter != nil }

// newResource builds the resource every signal is attributed to.
//
// It is built rather than taken from resource.Default, which reads
// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES: the service a signal is
// attributed to is a property of this program, not of the shell that started
// it. The identifier rather than the display name, because a service name is
// matched by tooling, not read by a person.
func newResource(cfg config.Config) *resource.Resource {
	attributes := []attribute.KeyValue{
		semconv.ServiceName(cfg.OTEL.ServiceName),
		semconv.ServiceVersion(config.AppVersion),
	}
	if cfg.OTEL.Environment != "" {
		attributes = append(attributes,
			semconv.DeploymentEnvironmentNameKey.String(cfg.OTEL.Environment))
	}
	return resource.NewSchemaless(attributes...)
}

// compression maps the configured name to the exporter's own value.
func compression(cfg config.Config) otlptracehttp.Compression {
	if cfg.OTEL.Compression == config.OTELCompressionNone {
		return otlptracehttp.NoCompression
	}
	return otlptracehttp.GzipCompression
}

// newTracerProvider builds the trace pipeline the configuration describes.
//
// The exporter is a batch processor with a bounded queue, which is what keeps
// span recording off the request path: a span is enqueued and the request
// continues, while a background goroutine drains the queue. A full queue drops
// the oldest span rather than blocking the caller.
func newTracerProvider(ctx context.Context, cfg config.Config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	exporter, err := newTraceExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(samplerFor(cfg.OTEL.Tracing)),
		sdktrace.WithBatcher(exporter,
			sdktrace.WithMaxQueueSize(cfg.OTEL.Queue.MaxSize),
			sdktrace.WithMaxExportBatchSize(cfg.OTEL.Tracing.MaxBatchSize),
			sdktrace.WithBatchTimeout(cfg.OTEL.Tracing.BatchTimeout),
			sdktrace.WithExportTimeout(cfg.OTEL.Tracing.ExportTimeout),
		),
	), nil
}

// newTraceExporter builds the trace exporter.
//
// Every setting is passed explicitly, including the ones left at their default,
// so the exporter cannot read OTEL_EXPORTER_OTLP_* for its address, headers,
// compression, or TLS material. The options are applied after the exporter's
// own environment pass, so a value passed here is the one that wins.
func newTraceExporter(ctx context.Context, cfg config.Config) (sdktrace.SpanExporter, error) {
	endpoint, err := url.Parse(cfg.OTEL.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("observer: otel endpoint: %w", err)
	}

	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(cfg.OTEL.Endpoint),
		otlptracehttp.WithHeaders(cfg.OTEL.Headers),
		otlptracehttp.WithCompression(compression(cfg)),
		otlptracehttp.WithTimeout(cfg.OTEL.Tracing.ExportTimeout),
	}
	// A route is applied only when one is named or the endpoint carries none.
	// An address that already names a route says where the traces go, and
	// overriding it with the default would send them elsewhere.
	if path := SignalPath(cfg.OTEL.Tracing.Path, endpoint.Path, otlpTracesPath); path != "" {
		options = append(options, otlptracehttp.WithURLPath(path))
	}
	// A nil TLS configuration is not the same as leaving the option out: it is
	// what stops the exporter from loading OTEL_EXPORTER_OTLP_CERTIFICATE and
	// friends. An https endpoint gets the floor of TLS 1.2 and the system's root
	// certificates, because the configuration names no certificate of its own.
	options = append(options, otlptracehttp.WithTLSClientConfig(TLSConfig(cfg.CollectorSecure())))

	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("observer: trace exporter: %w", err)
	}
	return exporter, nil
}

// newMeterProvider builds the metric pipeline the configuration describes.
// Metrics leave by pull first. The Prometheus bridge is a reader, not a
// server: it registers a collector on the registry, and MetricsHandler serves
// the exposition at /metrics, which is what a scraper reads — so the metrics
// keep flowing while the collector is down. The push leg follows the other
// signals and is opt-in (otel.metrics.push): when it is off, no exporter is
// built at all.
//
// Both readers aggregate in the background: a measurement is handed to the
// instrument and returns, and the reader collects on its own schedule. A scrape
// therefore reads a snapshot the reader already holds rather than waiting on the
// application, and an unreachable collector costs dropped exports, not latency.
func newMeterProvider(ctx context.Context, cfg config.Config, res *resource.Resource, o *Observer) (*metric.MeterProvider, error) {
	// The Prometheus bridge is a reader, not a server: it registers a collector
	// on the registry, and MetricsHandler serves that registry.
	registry := newRegistry()
	bridge, err := otlpbridge.New(otlpbridge.WithRegisterer(registry))
	if err != nil {
		return nil, fmt.Errorf("observer: prometheus bridge: %w", err)
	}
	o.registry = registry

	options := []metric.Option{
		metric.WithResource(res),
		metric.WithReader(bridge),
	}
	if cfg.OTEL.Metrics.Push {
		exporter, err := newMetricExporter(ctx, cfg)
		if err != nil {
			return nil, err
		}
		options = append(options, metric.WithReader(metric.NewPeriodicReader(exporter,
			metric.WithInterval(cfg.OTEL.Metrics.Interval),
			metric.WithTimeout(cfg.OTEL.Metrics.ExportTimeout),
		)))
	}

	return metric.NewMeterProvider(options...), nil
}

// newMetricExporter builds the metric exporter.
//
// Every setting is passed explicitly, including the ones left at their default,
// so the exporter cannot read OTEL_EXPORTER_OTLP_* for its address, headers,
// compression, or TLS material.
func newMetricExporter(ctx context.Context, cfg config.Config) (metric.Exporter, error) {
	endpoint, err := url.Parse(cfg.OTEL.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("observer: otel endpoint: %w", err)
	}

	options := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(cfg.OTEL.Endpoint),
		otlpmetrichttp.WithHeaders(cfg.OTEL.Headers),
		otlpmetrichttp.WithCompression(metricCompression(cfg)),
		otlpmetrichttp.WithTimeout(cfg.OTEL.Metrics.ExportTimeout),
		otlpmetrichttp.WithTLSClientConfig(TLSConfig(cfg.CollectorSecure())),
	}
	if path := SignalPath(cfg.OTEL.Metrics.Path, endpoint.Path, otlpMetricsPath); path != "" {
		options = append(options, otlpmetrichttp.WithURLPath(path))
	}

	exporter, err := otlpmetrichttp.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("observer: metric exporter: %w", err)
	}
	return exporter, nil
}

// SetGlobals installs the providers as the process defaults, so a package that
// instruments through the OpenTelemetry API without being handed a provider
// reports into the same pipeline.
//
// A signal that is switched off keeps the SDK's own no-op global: recording a
// span or a measurement then costs a nil check, which is the honest cost of a
// signal nobody asked for.
func (o *Observer) SetGlobals() {
	if o == nil {
		return
	}
	// W3C trace context and baggage. The SDK's default propagator carries
	// no fields, so an outbound call would drop a trace it was given.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if o.tracer != nil {
		otel.SetTracerProvider(o.tracer)
	}
	if o.meter != nil {
		otel.SetMeterProvider(o.meter)
	}
}

// Tracer returns the tracer provider, or nil when tracing is off. A caller that
// needs a provider rather than the global one uses this.
func (o *Observer) Tracer() *sdktrace.TracerProvider { return o.tracer }

// Meter returns the meter provider, or nil when metrics are off.
func (o *Observer) Meter() *metric.MeterProvider { return o.meter }
