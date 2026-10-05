package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/framework/kernel"
	fwmiddleware "github.com/riipandi/saka/framework/middleware"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/transport/router"
)

// spendingLimiter is a limiter whose window is spent: every check answers
// limited, with the Retry-After a client waits by.
type spendingLimiter struct{ calls int }

func (s *spendingLimiter) Allow(context.Context, string, fwmiddleware.Policy) (fwmiddleware.Result, error) {
	s.calls++
	return fwmiddleware.Result{
		Limited: true, Limit: 60, Remaining: 0,
		RetryAfter: 30 * time.Second,
		ResetAt:    time.Now().Add(time.Minute),
	}, nil
}

// countPaths classifies exactly the paths the test names, the way the
// composition root's classifier answers the guard's tables.
func countPaths(paths ...string) fwmiddleware.Classifier {
	counted := map[string]struct{}{}
	for _, path := range paths {
		counted[path] = struct{}{}
	}
	return func(path string) (fwmiddleware.RateClass, bool) {
		if _, ok := counted[path]; !ok {
			return fwmiddleware.RateClass{}, false
		}
		return fwmiddleware.RateClass{
			Name:   "default",
			Policy: fwmiddleware.Policy{Limit: 60, Window: time.Minute},
		}, true
	}
}

// apiFeature mounts its own route under /api, the way an application-API
// feature does, and a protocol endpoint on the router's root.
type apiFeature struct{}

func (apiFeature) Name() string { return "api-feature" }

func (apiFeature) Mount(r chi.Router) {
	r.Get("/api/feature/thing", func(w http.ResponseWriter, _ *http.Request) {})
	r.Get("/.well-known/thing", func(w http.ResponseWriter, _ *http.Request) {})
}

// TestAClassifiedModuleRouteIsThrottled pins the regression the limiter's own
// history wrote: the middleware sits on the group every surface mounts
// inside, so a feature's route is counted exactly when the classifier names
// it — on the API, under a module, on a protocol path of the root.
func TestAClassifiedModuleRouteIsThrottled(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"the API's own route", "/api/"},
		{"a feature route under /api", "/api/feature/thing"},
		{"a protocol endpoint on the root", "/.well-known/thing"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limiter := &countingLimiter{}
			router := router.NewRouter(router.Options{
				Config:       config.Default(),
				RateLimiter:  limiter,
				RateClassify: countPaths(tc.path),
				Modules:      []kernel.Module{apiFeature{}},
			})

			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.path, nil))

			assert.Equal(t, 1, limiter.calls, "%s must be throttled", tc.path)
		})
	}
}

// TestAnUnclassifiedRouteSpendsNoCheck keeps the other half of the decision
// visible: a path the classifier does not name passes before any check runs,
// whatever surface it mounted on.
func TestAnUnclassifiedRouteSpendsNoCheck(t *testing.T) {
	limiter := &countingLimiter{}
	router := router.NewRouter(router.Options{
		Config:       config.Default(),
		RateLimiter:  limiter,
		RateClassify: countPaths("/api/other/thing"),
		Modules:      []kernel.Module{apiFeature{}},
	})

	for _, path := range []string{"/api/feature/thing", "/.well-known/thing"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	assert.Zero(t, limiter.calls, "a path the tables do not name is outside the limiter's books")
}

// TestTheSPAAndMetricsAreNotThrottled covers the other side of the boundary:
// the limiter guards the routes a client calls, not the assets it loads or a
// scrape. Those paths are outside the group the limiter wraps.
func TestTheSPAAndMetricsAreNotThrottled(t *testing.T) {
	limiter := &countingLimiter{}
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
	cfg := config.Default()
	cfg.OTEL.Metrics.PrometheusPath = "/metrics"

	router := router.NewRouter(router.Options{
		Config:       cfg,
		RateLimiter:  limiter,
		RateClassify: countAllEverywhere(),
		Metrics:      metrics,
	})

	for _, path := range []string{"/metrics", "/"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	assert.Zero(t, limiter.calls, "a metrics scrape and an SPA load spend no rate-limit check")
}

// countAllEverywhere classifies every path into the default bucket.
func countAllEverywhere() fwmiddleware.Classifier {
	return func(path string) (fwmiddleware.RateClass, bool) {
		return fwmiddleware.RateClass{
			Name:   "default",
			Policy: fwmiddleware.Policy{Limit: 60, Window: time.Minute},
		}, true
	}
}

// TestAnExcludedModulePathIsSpared keeps the exclusion list meaningful now
// that it covers module routes too.
func TestAnExcludedModulePathIsSpared(t *testing.T) {
	limiter := &countingLimiter{}
	router := router.NewRouter(router.Options{
		Config:       config.Default(),
		RateLimiter:  limiter,
		RateClassify: countAllEverywhere(),
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/healthz", nil))

	assert.Zero(t, limiter.calls, "the health probe is excluded, so it reaches no check")
}

// TestTheRPCHealthProcedureIsExcludedToo is the consistency the two health
// endpoints hold: the same readiness the REST probe is spared for is spared
// below /rpc, where a backoffice monitor calls it.
func TestTheRPCHealthProcedureIsExcludedToo(t *testing.T) {
	limiter := &countingLimiter{}
	router := router.NewRouter(router.Options{
		Config:       config.Default(),
		Checker:      health.NewChecker(),
		RateLimiter:  limiter,
		RateClassify: countAllEverywhere(),
	})

	router.ServeHTTP(httptest.NewRecorder(), rpcRequest(t, "/saka.system.v1.HealthService/Check", "{}"))

	assert.Zero(t, limiter.calls, "the RPC readiness reaches no check at all")
}

// TestALimitedProcedureIsRefusedInTheConnectProtocol pins the refusal each
// transport owns: a spent window on the RPC surface answers resource_exhausted
// — the code the specification maps to 429 — with the headers a client paces
// by, never the REST envelope a Connect client cannot parse. The procedure is
// a module's, not the readiness one: the health procedure is excluded, the
// same as its REST twin.
func TestALimitedProcedureIsRefusedInTheConnectProtocol(t *testing.T) {
	feature := &rpcFeature{}
	router := router.NewRouter(router.Options{
		Config:       config.Default(),
		Checker:      health.NewChecker(),
		RateLimiter:  &spendingLimiter{},
		RateClassify: countAllEverywhere(),
		Modules:      []kernel.Module{feature},
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.test.v1.FeatureService/Ping", "{}"))

	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())

	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "resource_exhausted", body.Code)
	assert.NotContains(t, rec.Body.String(), `"status"`,
		"the refusal is a connect error, not the REST envelope")

	assert.Equal(t, "30", rec.Header().Get(fwmiddleware.RateLimitRetryHeader))
	assert.False(t, rec.Header().Get(fwmiddleware.RateLimitLimitHeader) == "")
}

// TestALimitedRestRouteIsRefusedInTheEnvelope is the other side of the same
// rule: the REST surface's refusal is the envelope its clients read.
func TestALimitedRestRouteIsRefusedInTheEnvelope(t *testing.T) {
	router := router.NewRouter(router.Options{
		Config:       config.Default(),
		RateLimiter:  &spendingLimiter{},
		RateClassify: countAllEverywhere(),
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/", nil))

	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())

	var body struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "error", body.Status)
	assert.Equal(t, "30", rec.Header().Get(fwmiddleware.RateLimitRetryHeader))
}
