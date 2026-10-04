package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/riipandi/saka/internal/database"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/responder"
	"github.com/riipandi/saka/pkg/testutils"
)

// valkeyClient opens the backend client a KV limiter test runs against.
func valkeyClient(url string) (valkey.Client, error) {
	opt, err := valkey.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return valkey.NewClient(opt)
}

// testPolicy is the budget the limiter tests run under, the shape a
// classifier hands the middleware.
var testPolicy = Policy{Limit: 60, Window: time.Minute}

// classifyCounted answers the one bucket every path falls into, the
// classifier a deployment whose whole surface is counted would build.
func classifyCounted(path string) (RateClass, bool) {
	return RateClass{Name: "default", Policy: testPolicy}, true
}

// classifyNothing is the classifier of a surface nothing is counted on.
func classifyNothing(path string) (RateClass, bool) {
	return RateClass{}, false
}

// stubLimiter is a Limiter a test answers with, recording the keys it was
// asked about and the policies those keys rode with.
type stubLimiter struct {
	keys     []string
	policies []Policy
	result   Result
	err      error
}

func (s *stubLimiter) Allow(_ context.Context, key string, policy Policy) (Result, error) {
	s.keys = append(s.keys, key)
	s.policies = append(s.policies, policy)
	return s.result, s.err
}

// envelopeRefuse is the refusal the REST surface passes the middleware: the
// 429 envelope the responder writes.
func envelopeRefuse(w http.ResponseWriter, r *http.Request) {
	responder.Fail(w, r, http.StatusTooManyRequests, "rate limit exceeded")
}

func TestRateLimitWritesTheHeadersAClientPacesBy(t *testing.T) {
	reset := time.Now().Add(time.Minute).Truncate(time.Second)
	limiter := &stubLimiter{result: Result{Limit: 60, Remaining: 59, ResetAt: reset}}
	handler := RateLimit("rest", limiter, envelopeRefuse, classifyCounted)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.RemoteAddr = "192.0.2.1:4711"
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "60", rec.Header().Get(RateLimitLimitHeader))
	assert.Equal(t, "59", rec.Header().Get(RateLimitRemainingHeader))
	assert.Equal(t, strconv.FormatInt(reset.Unix(), 10), rec.Header().Get(RateLimitResetHeader))

	// The key is the bucket and the address without its port, reduced to the
	// alphabet the rate_limits check allows.
	assert.Equal(t, []string{"default:ip_192_0_2_1"}, limiter.keys)
	assert.Equal(t, []Policy{testPolicy}, limiter.policies,
		"the budget the check counts against is the classifier's")
}

func TestRateLimitSparesAClassifiedAsExempt(t *testing.T) {
	limiter := &stubLimiter{}
	handler := RateLimit("rest", limiter, envelopeRefuse, classifyNothing)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users", nil))

	// An uncounted path is answered before any check runs, and its response
	// carries no headers, because no budget was spent answering it.
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, limiter.keys)
	assert.Empty(t, rec.Header().Get(RateLimitLimitHeader))
}

func TestRateLimitRefusesAStudentWhoSpentTheWindow(t *testing.T) {
	limiter := &stubLimiter{result: Result{
		Limited: true, Limit: 60, Remaining: 0,
		RetryAfter: 30 * time.Second,
	}}
	handler := RateLimit("rest", limiter, envelopeRefuse, classifyCounted)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Fail(t, "a limited request must not reach the route")
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "30", rec.Header().Get(RateLimitRetryHeader))
	assert.Equal(t, "0", rec.Header().Get(RateLimitRemainingHeader))

	var body struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "error", body.Status)
}

func TestRateLimitLetsTheRequestThroughWhenTheBackendCannotAnswer(t *testing.T) {
	limiter := &stubLimiter{err: context.DeadlineExceeded}
	handler := RateLimit("rest", limiter, envelopeRefuse, classifyCounted)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))

	// A limiter that cannot answer is a degraded protection, not a downed
	// service: the request passes, the headers stay unset.
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(RateLimitLimitHeader))
}

func TestRateLimitWithoutALimiterIsAPassThrough(t *testing.T) {
	handler := RateLimit("rest", nil, envelopeRefuse, classifyCounted)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRateLimitSparesTheExcludedPrefixes(t *testing.T) {
	limiter := &stubLimiter{}
	handler := RateLimit("rest", limiter, envelopeRefuse, classifyCounted, "/api/healthz")(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	// A prefix matches the paths under it.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/healthz/deep", nil))

	assert.Empty(t, limiter.keys, "an excluded path reaches no check at all, not merely one it would pass")

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/healthcheck", nil))
	assert.Equal(t, []string{"default:ip_192_0_2_1"}, limiter.keys,
		"a path that merely shares a prefix character is still counted")
}

func TestRetryAfterFromDetailPrefersTheFunctionHint(t *testing.T) {
	detail := "Key: ip_1, Count: 61, Limit: 60, Retry after: 42 seconds"
	assert.Equal(t, 42*time.Second, retryAfterFromDetail(detail, time.Minute))
}

func TestRetryAfterFromDetailFallsBackToTheWindow(t *testing.T) {
	assert.Equal(t, time.Minute, retryAfterFromDetail("no hint", time.Minute))
}

// migratedPool applies the migrations to a fresh test database and returns
// the pool the limiter runs on. The same idiom the queue tests use.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := database.NewMigrator(t.Context(), migrationDB, database.MigratorOptions{})
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "ratelimit_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })
	return pool
}

func TestDatabaseLimiterCountsTheWindow(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	limiter := NewDatabaseLimiter(migratedPool(t))
	policy := Policy{Limit: 2, Window: time.Minute}

	first, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", policy)
	require.NoError(t, err)
	assert.False(t, first.Limited)
	assert.Equal(t, 2, first.Limit)
	assert.Equal(t, 1, first.Remaining)

	second, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", policy)
	require.NoError(t, err)
	assert.Equal(t, 0, second.Remaining)

	third, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", policy)
	require.NoError(t, err)
	assert.True(t, third.Limited)
	assert.Equal(t, 0, third.Remaining)
	assert.Greater(t, third.RetryAfter, time.Duration(0), "the function's own retry hint")
}

func TestDatabaseLimiterBucketsAreIndependent(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	limiter := NewDatabaseLimiter(migratedPool(t))

	// The same address in two buckets spends two budgets: the credential
	// attempts a client makes must not starve the rest of its traffic.
	_, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", Policy{Limit: 1, Window: time.Minute})
	require.NoError(t, err)
	_, err = limiter.Allow(t.Context(), "auth:ip_192_0_2_7", Policy{Limit: 1, Window: time.Minute})
	require.NoError(t, err)
	spent, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", Policy{Limit: 1, Window: time.Minute})
	require.NoError(t, err)
	assert.True(t, spent.Limited, "the credential bucket is spent")

	other, err := limiter.Allow(t.Context(), "default:ip_192_0_2_7", Policy{Limit: 1, Window: time.Minute})
	require.NoError(t, err)
	assert.False(t, other.Limited, "the default bucket is the client's own budget")
}

func TestDatabaseLimiterKeysAreIndependent(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	limiter := NewDatabaseLimiter(migratedPool(t))
	policy := Policy{Limit: 1, Window: time.Minute}

	_, err := limiter.Allow(t.Context(), "ip_192_0_2_7", policy)
	require.NoError(t, err)

	other, err := limiter.Allow(t.Context(), "ip_192_0_2_8", policy)
	require.NoError(t, err)
	assert.False(t, other.Limited, "a second client starts its own window")
}

func TestKVStoreLimiterCountsTheWindow(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	container := testutils.StartValkey(t.Context(), t)
	client, err := valkeyClient(container.URL)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	limiter := NewKVStoreLimiter(client)
	policy := Policy{Limit: 2, Window: time.Minute}
	key := "ip_" + sanitizeKey(strings.ToLower(t.Name()))

	first, err := limiter.Allow(t.Context(), key, policy)
	require.NoError(t, err)
	assert.False(t, first.Limited)
	assert.Equal(t, 1, first.Remaining)
	assert.False(t, first.ResetAt.IsZero())

	second, err := limiter.Allow(t.Context(), key, policy)
	require.NoError(t, err)
	assert.Equal(t, 0, second.Remaining)

	third, err := limiter.Allow(t.Context(), key, policy)
	require.NoError(t, err)
	assert.True(t, third.Limited)
	assert.Greater(t, third.RetryAfter, time.Duration(0), "the window has time left")
}

func TestKVStoreLimiterExpiresTheWindow(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	container := testutils.StartValkey(t.Context(), t)
	client, err := valkeyClient(container.URL)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	limiter := NewKVStoreLimiter(client)
	policy := Policy{Limit: 1, Window: 2 * time.Second}
	key := "ip_" + sanitizeKey(strings.ToLower(t.Name()))

	exhausted, err := limiter.Allow(t.Context(), key, policy)
	require.NoError(t, err)
	require.False(t, exhausted.Limited)

	limited, err := limiter.Allow(t.Context(), key, policy)
	require.NoError(t, err)
	require.True(t, limited.Limited)

	time.Sleep(3 * time.Second)

	renewed, err := limiter.Allow(t.Context(), key, policy)
	require.NoError(t, err)
	assert.False(t, renewed.Limited, "a spent window starts again once it expires")
}
