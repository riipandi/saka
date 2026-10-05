package observer

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
)

// metricCompression maps the configured name to the metric exporter's own value.
//
// It is a separate function from compression because the two OTLP exporters are
// separate packages with separate types, so there is no shared constant to name.
func metricCompression(opts Options) otlpmetrichttp.Compression {
	if opts.Compression == CompressionNone {
		return otlpmetrichttp.NoCompression
	}
	return otlpmetrichttp.GzipCompression
}

// newRegistry returns the registry the Prometheus exposition is served from.
//
// It is a fresh registry rather than the package default, so nothing a
// dependency registered globally appears on this endpoint: a driver's pool
// statistics or another library's own instruments would otherwise be
// attributed to this service. The process collectors are the exception, and
// they are registered here deliberately — the runtime and the process are what
// this service is, not a library that sneaks in.
func newRegistry() *prometheus.Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return registry
}

// MetricsHandler returns the HTTP handler that serves the Prometheus
// exposition, or nil when metrics are switched off.
//
// A nil handler is the honest answer for a disabled signal: a caller mounts it
// only when there is something to serve, rather than exposing an endpoint that
// reports nothing.
func (o *Observer) MetricsHandler() http.Handler {
	if o == nil || o.registry == nil {
		return nil
	}
	return promhttp.HandlerFor(o.registry, promhttp.HandlerOpts{})
}
