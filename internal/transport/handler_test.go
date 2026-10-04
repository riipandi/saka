package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/transport/middleware"
)

// envelopeRequestID reads the request id the envelope metadata carries.
func envelopeRequestID(body []byte) string {
	var parsed struct {
		Metadata struct {
			RequestID string `json:"request_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed.Metadata.RequestID
}

// newTestRouter builds the debug-build router a test serves: no web output is
// embedded, so the SPA surface answers not-found in the API envelope.
func newTestRouter(t *testing.T) (chi.Router, config.Config) {
	t.Helper()

	cfg := config.Default()
	router := NewRouter(Options{
		Config:  cfg,
		Checker: health.NewChecker(),
		Metrics: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}),
	})
	return router, cfg
}

func TestAPIRootNamesTheSurface(t *testing.T) {
	router, cfg := newTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Mode    string `json:"mode"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "success", body.Status)
	assert.Equal(t, config.AppIdentifier, body.Data.Name)
	assert.Equal(t, cfg.App.Mode, body.Data.Mode)
}

func TestAPIHealthzReportsTheChecker(t *testing.T) {
	router, _ := newTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))

	// A checker with no checks is healthy: the endpoint reports what backs it,
	// and nothing failing is healthy by the engine's own rule.
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestAPINotFoundComesFromTheEnvelope(t *testing.T) {
	router, _ := newTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/absent", nil))

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "\"status\":\"error\"",
		"an API path must be answered in the envelope, never with index.html")
	assert.True(t, strings.HasPrefix(envelopeRequestID(rec.Body.Bytes()), "req_"),
		"the envelope must name the request the middleware tagged")
}

func TestMetricsMountsWhereTheConfigNames(t *testing.T) {
	router, cfg := newTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, cfg.OTEL.Metrics.PrometheusPath, nil))

	assert.Equal(t, http.StatusTeapot, rec.Code, "the metrics path serves the handler it was given")
}

func TestEveryResponseCarriesARequestID(t *testing.T) {
	router, _ := newTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))

	assert.True(t, len(rec.Header().Get(middleware.RequestIDHeader)) > len("req_"),
		"a response must name its request")
}

func TestServerTakesTheTimeoutsFromTheConfig(t *testing.T) {
	cfg := config.Default()
	server := NewServer(cfg, http.NotFoundHandler())

	assert.Equal(t, "0.0.0.0:3080", server.Addr)
	assert.Equal(t, cfg.Server.ReadTimeout, server.ReadTimeout)
	assert.Equal(t, cfg.Server.WriteTimeout, server.WriteTimeout)
	assert.Equal(t, cfg.Server.IdleTimeout, server.IdleTimeout)
}

func TestRouterServesAProbeRequestContext(t *testing.T) {
	router, _ := newTestRouter(t)

	srv := httptest.NewServer(router)
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// countingLimiter records the keys it was asked about, so a test asserts where
// the rate limit middleware ran without stubbing a backend.
type countingLimiter struct {
	calls int
}

func (c *countingLimiter) Allow(_ context.Context, _ string, _ middleware.Policy) (middleware.Result, error) {
	c.calls++
	return middleware.Result{Limit: 100, Remaining: 99, ResetAt: time.Now().Add(time.Minute)}, nil
}

// countAll classifies every path into the default bucket, the classifier a
// test uses to exercise the limiter's own behaviour.
func countAll(path string) (middleware.RateClass, bool) {
	return middleware.RateClass{Name: "default", Policy: middleware.Policy{Limit: 100, Window: time.Minute}}, true
}

func TestRateLimitRunsOnTheAPISurfaceOnly(t *testing.T) {
	limiter := &countingLimiter{}
	router := NewRouter(Options{
		Config:       config.Default(),
		RateLimiter:  limiter,
		RateClassify: countAll,
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api", nil))
	assert.Equal(t, 1, limiter.calls, "an API request is throttled")

	limiter.calls = 0
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	assert.Zero(t, limiter.calls, "a health probe spends no rate limit check")

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/anything-else", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	assert.Zero(t, limiter.calls, "the SPA surface spends no rate limit check")
}

func TestRouterSkipsTheLoggingMiddlewaresWithoutALogger(t *testing.T) {
	router := NewRouter(Options{Config: config.Default()})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))

	assert.Equal(t, http.StatusOK, rec.Code, "a router without a logger still serves")
}
