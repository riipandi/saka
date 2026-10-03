package observer_test

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/observer"
)

// traceConfig returns a configuration with tracing on and nothing else, pointed
// at endpoint.
func traceConfig(endpoint string) config.Config {
	cfg := config.Default()
	cfg.OTEL.Endpoint = endpoint
	cfg.OTEL.Tracing.Enable = true
	// A batch that ships on the next shutdown rather than after a wait, so a
	// test asserts delivery without sleeping through the real interval.
	cfg.OTEL.Tracing.BatchTimeout = 10 * time.Millisecond
	cfg.OTEL.Tracing.ExportTimeout = 2 * time.Second
	return cfg
}

// metricConfig returns a configuration with metrics on and nothing else.
func metricConfig(endpoint string) config.Config {
	cfg := config.Default()
	cfg.OTEL.Endpoint = endpoint
	cfg.OTEL.Metrics.Enable = true
	cfg.OTEL.Metrics.Interval = 50 * time.Millisecond
	cfg.OTEL.Metrics.ExportTimeout = 2 * time.Second
	return cfg
}

// shutdownDraining stops an observer whose collector is unreachable.
//
// The SDK reports a failed drain, which is the honest answer for a test that
// deliberately points at a dead port: it is the state the test is about, not a
// failure of the code under test. The caller's own assertion is what the test
// checks.
func shutdownDraining(t *testing.T, obs *observer.Observer) {
	t.Helper()
	if err := obs.Shutdown(context.Background()); err != nil {
		t.Logf("drain reported an unreachable collector: %v", err)
	}
}

func TestNothingIsBuiltWhenNoSignalIsEnabled(t *testing.T) {
	// The default configuration ships no telemetry, so building an observer must
	// dial nothing and still return something Shutdown can be called on. A
	// caller that has to check for nil would forget somewhere.
	obs, err := observer.New(context.Background(), config.Default())
	require.NoError(t, err)
	require.NotNil(t, obs)

	assert.False(t, obs.Tracing())
	assert.False(t, obs.Metrics())
	assert.Nil(t, obs.MetricsHandler(), "a disabled signal exposes no endpoint")
	require.NoError(t, obs.Shutdown(context.Background()))
}

func TestShutdownIsSafeToCallTwice(t *testing.T) {
	// The root After runs once per run, but a failure path shuts down early and
	// the After still runs. A second shutdown must be a no-op, not a panic on a
	// provider that was already released.
	obs, err := observer.New(context.Background(), traceConfig("http://127.0.0.1:1"))
	require.NoError(t, err)

	require.NoError(t, obs.Shutdown(context.Background()))
	require.NoError(t, obs.Shutdown(context.Background()))
}

func TestSpansReachTheCollector(t *testing.T) {
	server := newCollector(t)
	cfg := traceConfig(server.URL)
	cfg.OTEL.Tracing.Path = "/collector/v1/traces"

	obs, err := observer.New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { shutdownDraining(t, obs) })

	_, span := obs.Tracer().Tracer("test").Start(context.Background(), "work")
	span.End()

	require.NoError(t, obs.Shutdown(context.Background()))

	received := server.requests()
	require.NotEmpty(t, received, "a shutdown flushes the batch processor")
	assert.Equal(t, "/collector/v1/traces", received[0].path)
	assert.Contains(t, string(received[0].body), "work",
		"the span name travels in the protobuf payload")
}

func TestTheServiceResourceIsBuiltHere(t *testing.T) {
	// The environment is not a source: OTEL_RESOURCE_ATTRIBUTES must not be able
	// to rename the service a signal is attributed to, and OTEL_SERVICE_NAME
	// must not either.
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=from-the-shell")
	t.Setenv("OTEL_SERVICE_NAME", "from-the-shell")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer leaked")

	server := newCollector(t)
	cfg := traceConfig(server.URL)
	cfg.OTEL.ServiceName = "saka-test"
	cfg.OTEL.Environment = "test"

	obs, err := observer.New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { shutdownDraining(t, obs) })

	_, span := obs.Tracer().Tracer("test").Start(context.Background(), "work")
	span.End()
	require.NoError(t, obs.Shutdown(context.Background()))

	received := server.requests()
	require.NotEmpty(t, received)

	body := string(received[0].body)
	assert.Contains(t, body, "saka-test", "the service name comes from the configuration")
	assert.Contains(t, body, "test", "the deployment environment comes from the configuration")
	assert.NotContains(t, body, "from-the-shell",
		"a resource attribute from the environment must not be sent")
	assert.Empty(t, received[0].authorization,
		"a header from the environment must not be sent")
}

func TestSamplerFromConfiguration(t *testing.T) {
	// The sampler is the one setting that bounds what tracing costs, so each
	// name maps to the sampler it says it is.
	cases := []struct {
		sampler string
		ratio   float64
		records bool
	}{
		{config.OTELSamplerAlways, 1, true},
		{config.OTELSamplerNever, 1, false},
		{config.OTELSamplerRatio, 1, true},
		{config.OTELSamplerParentRatio, 1, true},
	}

	for _, tc := range cases {
		t.Run(tc.sampler, func(t *testing.T) {
			server := newCollector(t)
			cfg := traceConfig(server.URL)
			cfg.OTEL.Tracing.Sampler = tc.sampler
			cfg.OTEL.Tracing.Ratio = tc.ratio

			obs, err := observer.New(context.Background(), cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, obs.Shutdown(context.Background())) })

			_, span := obs.Tracer().Tracer("test").Start(context.Background(), "work")
			span.End()
			require.NoError(t, obs.Shutdown(context.Background()))

			if tc.records {
				assert.NotEmpty(t, server.requests(), "sampler %q records", tc.sampler)
				return
			}
			assert.Empty(t, server.requests(), "sampler %q records nothing", tc.sampler)
		})
	}
}

func TestAMetricIsRecordedWithoutWaitingOnTheCollector(t *testing.T) {
	// The point of the queue: recording a measurement returns immediately, and
	// the export happens on the reader's own schedule. The collector here is
	// never reached, and recording must still be fast and must not fail.
	cfg := metricConfig("http://127.0.0.1:1")
	cfg.OTEL.Metrics.Interval = time.Hour

	obs, err := observer.New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { shutdownDraining(t, obs) })

	counter, err := obs.Meter().Meter("test").Int64Counter("recorded")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		counter.Add(context.Background(), 1)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recording a measurement blocked on an unreachable collector")
	}
}

func TestThePrometheusEndpointServesWhatWasRecorded(t *testing.T) {
	// The scrape is the second export route, and it must work with no collector
	// at all: this is the deployment that runs a scraper and no collector.
	cfg := metricConfig("http://127.0.0.1:1")
	cfg.OTEL.Metrics.PrometheusPath = "/metrics"

	obs, err := observer.New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { shutdownDraining(t, obs) })

	counter, err := obs.Meter().Meter("test").Int64Counter("requests_total")
	require.NoError(t, err)
	counter.Add(context.Background(), 7)

	handler := obs.MetricsHandler()
	require.NotNil(t, handler)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	body := recorder.Body.String()
	assert.Contains(t, body, "requests_total", "the instrument appears in the exposition")
	assert.Contains(t, body, "7", "the recorded value appears in the exposition")
}

func TestThePrometheusExpositionIsPrivateToThisProcess(t *testing.T) {
	// The exposition is served from a registry this package owns. The process
	// collectors are registered deliberately in newRegistry — the runtime and
	// the process are what this service reports — and nothing a dependency
	// registered globally appears: a library's instruments on the default
	// registry would otherwise be attributed to this service.
	obs, err := observer.New(context.Background(), metricConfig("http://127.0.0.1:1"))
	require.NoError(t, err)
	t.Cleanup(func() { shutdownDraining(t, obs) })

	handler := obs.MetricsHandler()
	require.NotNil(t, handler)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := recorder.Body.String()
	assert.Contains(t, body, "go_goroutines",
		"the process collectors are registered deliberately, in one place")
	assert.NotContains(t, body, "promhttp_metric_handler_requests_total",
		"nothing the default registry holds is inherited")
}

func TestSetGlobalsInstallsOnlyEnabledSignals(t *testing.T) {
	// A signal that is off keeps the SDK's no-op global, so recording costs a
	// nil check rather than a queue nothing drains.
	otel.SetTracerProvider(otel.GetTracerProvider())
	otel.SetMeterProvider(otel.GetMeterProvider())

	obs, err := observer.New(context.Background(), traceConfig("http://127.0.0.1:1"))
	require.NoError(t, err)
	t.Cleanup(func() { shutdownDraining(t, obs) })

	obs.SetGlobals()

	assert.IsType(t, &sdktrace.TracerProvider{}, otel.GetTracerProvider(),
		"tracing is on, so the global is the real provider")
	assert.Contains(t, otel.GetTextMapPropagator().Fields(), "traceparent")
	_, isReal := otel.GetMeterProvider().(*sdkmetric.MeterProvider)
	assert.False(t, isReal, "metrics are off, so the global stays the no-op")
}

func TestTheEndpointPathIsKeptWhenItCarriesOne(t *testing.T) {
	// An address that already names a route is not overridden with the default:
	// a collector mounted under a prefix says where traces go.
	server := newCollector(t)
	cfg := traceConfig(server.URL + "/collector/v1/traces")

	obs, err := observer.New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { shutdownDraining(t, obs) })

	_, span := obs.Tracer().Tracer("test").Start(context.Background(), "routed")
	span.End()
	require.NoError(t, obs.Shutdown(context.Background()))

	received := server.requests()
	require.NotEmpty(t, received)
	assert.Equal(t, "/collector/v1/traces", received[0].path)
}

func TestConfiguredHeadersReachTheCollector(t *testing.T) {
	// Headers are what a collector authenticating the sender reads, so the value
	// from the configuration must be the one on the wire. The other half of the
	// rule is asserted in TestTheServiceResourceIsBuiltHere: a header the
	// environment set is not sent.
	server := newCollector(t)
	cfg := traceConfig(server.URL)
	cfg.OTEL.Headers = map[string]string{"authorization": "Bearer configured"}

	obs, err := observer.New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { shutdownDraining(t, obs) })

	_, span := obs.Tracer().Tracer("test").Start(context.Background(), "work")
	span.End()
	require.NoError(t, obs.Shutdown(context.Background()))

	received := server.requests()
	require.NotEmpty(t, received)
	assert.Equal(t, "Bearer configured", received[0].authorization)
}

// collector is a stand-in for an OpenTelemetry collector. It records the path,
// the Authorization header, and the decompressed body of every export.
type collector struct {
	*httptest.Server

	mu       sync.Mutex
	recorded []collectorRequest
}

type collectorRequest struct {
	path          string
	authorization string
	body          []byte
}

// newCollector starts the stand-in. It is closed with the test.
func newCollector(t *testing.T) *collector {
	t.Helper()

	c := &collector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readBody(t, r)

		c.mu.Lock()
		c.recorded = append(c.recorded, collectorRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			body:          body,
		})
		c.mu.Unlock()

		// An empty export response is a valid protobuf message, which is what
		// the exporter reads as success.
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)

	return c
}

// requests returns what the collector has been sent so far.
func (c *collector) requests() []collectorRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]collectorRequest{}, c.recorded...)
}

// readBody reads an export body, which the exporter gzips by default.
//
// The reader is what a real collector does with Content-Encoding: without it a
// test would assert against compressed bytes and see none of the payload.
func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()

	reader := io.Reader(r.Body)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatalf("decompress export: %v", err)
		}
		defer func() { _ = gzipReader.Close() }()
		reader = gzipReader
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	return body
}
