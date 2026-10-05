package fetcher

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meterName is the instrumentation scope every fetcher instrument is
// registered under, rendered into otel_scope_name by the bridge.

// The outcomes one call carries on the requests instrument (the options' telemetry namespace prefixes it). They follow the
// error classes the caller matches, so a dashboard's outcome and a caller's
// errors.Is are the same taxonomy.
const (
	outcomeSuccess        = "success"
	outcomeHTTPStatus     = "http_status"
	outcomeNetwork        = "network"
	outcomeTimeout        = "timeout"
	outcomeCanceled       = "canceled"
	outcomeCircuitOpen    = "circuit_open"
	outcomeRetryExhausted = "retry_exhausted"
	outcomeInvalid        = "invalid"
	outcomeError          = "error"
)

// fetchMetrics holds the fetcher's instruments. The client builds one at
// construction from the global meter provider, which is real when the observer
// is up and the SDK's no-op when metrics are off.
type fetchMetrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
	attempts metric.Int64Histogram
	breaker  metric.Int64Counter
}

// defaultScope is the instrumentation scope a caller that names none gets: a
// scope identifies the instrumenting code, and this package's word is the
// honest default. A deployment that wants its own identity passes it in the
// options.
const defaultScope = "fetcher"

// instrumentName prefixes a bare instrument name with the options' telemetry
// namespace. An empty namespace leaves the domain name bare: the series name
// is the caller's identity decision, not the framework's.
func instrumentName(opts Options, name string) string {
	if opts.TelemetryNamespace == "" {
		return name
	}
	return opts.TelemetryNamespace + "." + name
}

func newFetchMetrics(opts Options) *fetchMetrics {
	scope := opts.Scope
	if scope == "" {
		scope = defaultScope
	}
	meter := otel.Meter(scope)
	m := &fetchMetrics{}
	var err error
	if m.requests, err = meter.Int64Counter(instrumentName(opts, "fetch.requests"),
		metric.WithDescription("Outbound calls by host and outcome class"),
		metric.WithUnit("{request}")); err != nil {
		panic("fetcher: " + err.Error())
	}
	if m.duration, err = meter.Float64Histogram(instrumentName(opts, "fetch.request.duration"),
		metric.WithDescription("Time one call spent, retries included"),
		metric.WithUnit("s")); err != nil {
		panic("fetcher: " + err.Error())
	}
	if m.attempts, err = meter.Int64Histogram(instrumentName(opts, "fetch.request.attempts"),
		metric.WithDescription("Attempts one call consumed"),
		metric.WithUnit("{attempt}"),
		metric.WithExplicitBucketBoundaries(1, 2, 3, 4, 5, 10)); err != nil {
		panic("fetcher: " + err.Error())
	}
	if m.breaker, err = meter.Int64Counter(instrumentName(opts, "fetch.breaker.opens"),
		metric.WithDescription("Times a host's circuit breaker opened"),
		metric.WithUnit("{circuit}")); err != nil {
		panic("fetcher: " + err.Error())
	}
	return m
}

// outcomeFor maps a classified error to the outcome the counter carries. The
// classes are the same errors.Is sentinels Do returns, in the order the
// classifier itself resolves them.
func (m *fetchMetrics) outcomeFor(classified error) string {
	switch {
	case classified == nil:
		return outcomeSuccess
	case errors.Is(classified, ErrCircuitOpen):
		return outcomeCircuitOpen
	case errors.Is(classified, ErrCanceled):
		return outcomeCanceled
	case errors.Is(classified, ErrTimeout):
		return outcomeTimeout
	case errors.Is(classified, ErrNetwork):
		return outcomeNetwork
	case errors.Is(classified, ErrRetryExhausted):
		return outcomeRetryExhausted
	case errors.Is(classified, ErrStatus):
		return outcomeHTTPStatus
	default:
		return outcomeError
	}
}

// recordRequest counts one finished call: the outcome, the time it took, and
// the attempts the retry budget spent on it.
func (m *fetchMetrics) recordRequest(ctx context.Context, host, outcome string, duration time.Duration, attempts int) {
	hostAttr := metric.WithAttributes(
		attribute.String("host", host),
		attribute.String("outcome", outcome),
	)
	m.requests.Add(ctx, 1, hostAttr)
	m.duration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("host", host)))
	if attempts > 0 {
		m.attempts.Record(ctx, int64(attempts), metric.WithAttributes(attribute.String("host", host)))
	}
}

// recordBreakerOpen counts one host's breaker tripping open.
func (m *fetchMetrics) recordBreakerOpen(ctx context.Context, host string) {
	m.breaker.Add(ctx, 1, metric.WithAttributes(attribute.String("host", host)))
}
