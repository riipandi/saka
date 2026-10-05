package middleware

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// defaultScope is the instrumentation scope a mount that names no namespace
// gets: a scope identifies the instrumenting code, and this package's word is
// the honest default.
const defaultScope = "middleware"

// The outcomes one check carries on the requests instrument, under the mount's
// telemetry namespace. A degraded
// check passed the request through with the limiter unable to answer — the
// pass-through the limiter contract promises, counted so a limiter that has
// quietly gone down still shows on a dashboard.
const (
	outcomeAllowed  = "allowed"
	outcomeLimited  = "limited"
	outcomeExcluded = "excluded"
	outcomeDegraded = "degraded"
)

// rateLimitMetrics holds the limiter's one counter. The middleware builds one
// per mount from the global meter provider, which is real when the observer is
// up and the SDK's no-op when metrics are off.
type rateLimitMetrics struct {
	requests metric.Int64Counter
}

func rateLimitInstrumentation(namespace string) *rateLimitMetrics {
	scope := defaultScope
	name := "http.ratelimit.requests"
	if namespace != "" {
		scope = namespace + ".transport/middleware"
		name = namespace + "." + name
	}
	meter := otel.Meter(scope)
	var err error
	requests, err := meter.Int64Counter(name,
		metric.WithDescription("Rate limit checks by surface and outcome"),
		metric.WithUnit("{request}"))
	if err != nil {
		panic("middleware: " + err.Error())
	}
	return &rateLimitMetrics{requests: requests}
}
