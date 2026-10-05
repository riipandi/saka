package observer

import "time"

// Options is what the observer is built from. Every field is explicit, so a
// caller cannot construct an observer whose dependencies it cannot name, and
// nothing is read from the environment or an application's configuration
// here — the caller resolves those and passes the values.
type Options struct {
	// Endpoint is the OTLP/HTTP address the exporters dial.
	Endpoint string
	// Compression is the exporter's body compression.
	Compression Compression
	// Headers are the headers every export carries.
	Headers map[string]string
	// Secure marks an https endpoint, so the exporters load TLS material.
	Secure bool
	// QueueMaxSize is how many items one signal buffers before it starts
	// dropping the oldest.
	QueueMaxSize int
	// ServiceName and Version attribute every signal to the service that
	// produced it; Environment names the deployment when one is set.
	ServiceName string
	Version     string
	Environment string

	// Tracing and Metrics carry the per-signal settings.
	Tracing Tracing
	Metrics Metrics
}

// Tracing holds the trace export settings. It is read only when Enable is
// true, so a caller that does not collect traces is not held to a sampler it
// never runs.
type Tracing struct {
	// Enable exports spans. A service that does not trace dials nothing.
	Enable bool
	// Path is the collector route for traces. Empty means the protocol's own
	// /v1/traces, which is what a collector serves.
	Path string
	// Sampler decides which traces are recorded.
	Sampler Sampler
	// Ratio is the fraction of traces recorded, read only by the two ratio
	// samplers.
	Ratio float64
	// BatchTimeout is how long a span waits in the queue before the exporter
	// ships it, and ExportTimeout bounds one export attempt.
	BatchTimeout  time.Duration
	ExportTimeout time.Duration
	// MaxBatchSize is how many spans one export carries.
	MaxBatchSize int
}

// Metrics holds the metric export settings. It is read only when Enable is
// true. Pull is the primary route: the Prometheus exposition is always
// registered when metrics are enabled, and a scrape reads a snapshot the
// bridge already holds. Push is opt-in.
type Metrics struct {
	// Enable records and exports metrics.
	Enable bool
	// Push adds the OTLP push leg on top of the pull exposition. It is read
	// only when Enable is true; the pull route needs no second switch.
	Push bool
	// Path is the collector route for metrics. Empty means the protocol's own
	// /v1/metrics. It is read only when Push is true.
	Path string
	// Interval is how often measurements are handed to the push exporter, and
	// ExportTimeout bounds one export attempt. Both are read only when Push
	// is true.
	Interval      time.Duration
	ExportTimeout time.Duration
}

// Sampler is the name of a trace sampler. The values are the configuration
// vocabulary the schema validates against; the observer maps them onto the
// SDK's own samplers.
type Sampler string

const (
	// SamplerAlways records every trace.
	SamplerAlways Sampler = "always"
	// SamplerNever records no traces.
	SamplerNever Sampler = "never"
	// SamplerRatio records a fraction of traces, by Ratio.
	SamplerRatio Sampler = "ratio"
	// SamplerParentRatio follows a parent span's sampling decision, falling
	// back to Ratio at the root.
	SamplerParentRatio Sampler = "parent_ratio"
)

// Samplers lists every sampler name, for a schema that validates the value.
func Samplers() []string {
	return []string{string(SamplerAlways), string(SamplerNever), string(SamplerRatio), string(SamplerParentRatio)}
}

// Compression is the name of an exporter body compression.
type Compression string

const (
	// CompressionGzip compresses every export body.
	CompressionGzip Compression = "gzip"
	// CompressionNone ships the bodies plain.
	CompressionNone Compression = "none"
)

// Compressions lists every compression name, for a schema that validates the
// value.
func Compressions() []string {
	return []string{string(CompressionGzip), string(CompressionNone)}
}
