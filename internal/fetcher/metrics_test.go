package fetcher

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// The outcome a call carries is the same taxonomy the caller matches with
// errors.Is: each sentinel maps to one outcome label, and the mapping is what
// keeps a dashboard's breakdown and a caller's error handling the same story.
func TestOutcomeForMapsTheErrorClasses(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, outcomeSuccess},
		{fmt.Errorf("wrapped: %w", ErrCircuitOpen), outcomeCircuitOpen},
		{fmt.Errorf("wrapped: %w", ErrCanceled), outcomeCanceled},
		{fmt.Errorf("wrapped: %w", ErrTimeout), outcomeTimeout},
		{fmt.Errorf("wrapped: %w", ErrNetwork), outcomeNetwork},
		{fmt.Errorf("wrapped: %w", ErrRetryExhausted), outcomeRetryExhausted},
		{fmt.Errorf("wrapped: %w", ErrStatus), outcomeHTTPStatus},
		{errors.New("something else"), outcomeError},
	}
	for _, tc := range cases {
		if got := newFetchMetrics().outcomeFor(tc.err); got != tc.want {
			t.Errorf("outcomeFor(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// The instruments reach the reader with the host and outcome labels the
// dashboard groups by.
func TestTheFetcherInstrumentsReachTheReader(t *testing.T) {
	prev := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	m := newFetchMetrics()
	m.recordRequest(context.Background(), "api.example.com", outcomeTimeout, 2*time.Second, 3)
	m.recordBreakerOpen(context.Background(), "api.example.com")

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	for _, scope := range rm.ScopeMetrics {
		for _, sm := range scope.Metrics {
			switch sm.Name {
			case "saka.fetch.requests":
				data := sm.Data.(metricdata.Sum[int64])
				if len(data.DataPoints) != 1 {
					t.Fatalf("saka.fetch.requests: %d points, want 1", len(data.DataPoints))
				}
				dp := data.DataPoints[0]
				host, _ := dp.Attributes.Value("host")
				outcome, _ := dp.Attributes.Value("outcome")
				if host.String() != "api.example.com" || outcome.String() != outcomeTimeout {
					t.Errorf("saka.fetch.requests labels = host=%v outcome=%v", host.String(), outcome.String())
				}
			case "saka.fetch.request.duration":
				data := sm.Data.(metricdata.Histogram[float64])
				if len(data.DataPoints) != 1 || data.DataPoints[0].Count != 1 {
					t.Errorf("saka.fetch.request.duration: %d points, want 1 with one sample",
						len(data.DataPoints))
				}
			case "saka.fetch.breaker.opens":
				data := sm.Data.(metricdata.Sum[int64])
				if data.DataPoints[0].Value != 1 {
					t.Errorf("saka.fetch.breaker.opens = %v, want 1", data.DataPoints[0].Value)
				}
			case "saka.fetch.request.attempts":
				data := sm.Data.(metricdata.Histogram[int64])
				if data.DataPoints[0].Count != 1 {
					t.Error("saka.fetch.request.attempts: expected one sample")
				}
			default:
				t.Errorf("unexpected instrument %s", sm.Name)
			}
		}
	}
}
