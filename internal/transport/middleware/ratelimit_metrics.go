package middleware

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// meterName is the instrumentation scope the rate-limit counter is registered
// under, rendered into otel_scope_name by the bridge.
const meterName = "github.com/riipandi/saka/internal/transport/middleware"

// The outcomes one check carries on saka.http.ratelimit.requests. A degraded
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

func rateLimitInstrumentation() *rateLimitMetrics {
	meter := otel.Meter(meterName)
	var err error
	requests, err := meter.Int64Counter("saka.http.ratelimit.requests",
		metric.WithDescription("Rate limit checks by surface and outcome"),
		metric.WithUnit("{request}"))
	if err != nil {
		panic("middleware: " + err.Error())
	}
	return &rateLimitMetrics{requests: requests}
}
