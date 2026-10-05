package storage

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
const defaultScope = "storage"

// The outcomes one sync carries on the uploads instrument. A skipped sync
// found no staging file — the retry that arrived after another attempt
// finished, which is the idempotence contract working.
const (
	uploadSuccess = "success"
	uploadSkipped = "skipped"
	uploadError   = "error"
)

// storageMetrics holds the storage engine's instruments. The manager and the
// watcher each build one at construction from the global meter provider,
// which is real when the observer is up and the SDK's no-op when metrics are
// off.
type storageMetrics struct {
	staged   metric.Int64Counter
	uploads  metric.Int64Counter
	duration metric.Float64Histogram
	bytes    metric.Int64Counter
	settled  metric.Int64Counter
}

// instrumentName prefixes a bare instrument name with the options' telemetry
// namespace. An empty namespace leaves the domain name bare: the series name
// is the caller's identity decision, not the framework's.
func instrumentName(opts Options, name string) string {
	if opts.TelemetryNamespace == "" {
		return name
	}
	return opts.TelemetryNamespace + "." + name
}

func newStorageMetrics(opts Options) *storageMetrics {
	scope := opts.Scope
	if scope == "" {
		scope = defaultScope
	}
	meter := otel.Meter(scope)
	m := &storageMetrics{}
	var err error
	if m.staged, err = meter.Int64Counter(instrumentName(opts, "storage.files.staged"),
		metric.WithDescription("Files written into the staging directory"),
		metric.WithUnit("{file}")); err != nil {
		panic("storage: " + err.Error())
	}
	if m.uploads, err = meter.Int64Counter(instrumentName(opts, "storage.uploads"),
		metric.WithDescription("Sync rounds by outcome: uploaded, skipped as already done, or failed"),
		metric.WithUnit("{upload}")); err != nil {
		panic("storage: " + err.Error())
	}
	if m.duration, err = meter.Float64Histogram(instrumentName(opts, "storage.upload.duration"),
		metric.WithDescription("Time one sync round spent hashing and uploading"),
		metric.WithUnit("s")); err != nil {
		panic("storage: " + err.Error())
	}
	if m.bytes, err = meter.Int64Counter(instrumentName(opts, "storage.bytes.uploaded"),
		metric.WithDescription("Staging bytes that reached the backend"),
		metric.WithUnit("By")); err != nil {
		panic("storage: " + err.Error())
	}
	if m.settled, err = meter.Int64Counter(instrumentName(opts, "storage.staging.settled"),
		metric.WithDescription("Staging files that went quiet and had their upload enqueued"),
		metric.WithUnit("{file}")); err != nil {
		panic("storage: " + err.Error())
	}
	return m
}

// recordStaged counts one file that reached the staging directory.
func (m *storageMetrics) recordStaged(ctx context.Context) {
	m.staged.Add(ctx, 1)
}

// recordSettled counts one staging file whose upload was enqueued.
func (m *storageMetrics) recordSettled(ctx context.Context) {
	m.settled.Add(ctx, 1)
}

// recordSync counts one finished sync round: the outcome, how long the round
// took, and — on success — the bytes that reached the backend.
func (m *storageMetrics) recordSync(ctx context.Context, outcome string, duration time.Duration, uploaded int64) {
	attrs := metric.WithAttributes(attribute.String("outcome", outcome))
	m.uploads.Add(ctx, 1, attrs)
	m.duration.Record(ctx, duration.Seconds())
	if outcome == uploadSuccess {
		m.bytes.Add(ctx, uploaded, attrs)
	}
}
