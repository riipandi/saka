package health_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riipandi/saka/internal/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// passing and failing are the two check functions most tests need.
func passing(context.Context) error { return nil }

func failing(context.Context) error { return errors.New("boom") }

// The two vocabularies are separate: a component is up or down, the system is
// healthy or unhealthy. An unknown component is not a passing one.
func TestGlobalStatusHealthy(t *testing.T) {
	assert.True(t, health.GlobalHealthy.Healthy())
	assert.False(t, health.GlobalUnhealthy.Healthy())
}

func TestCheckReportsUpWhenEveryCheckPasses(t *testing.T) {
	checker := health.NewChecker(health.WithChecks(
		health.Check{Name: "a", Check: passing},
		health.Check{Name: "b", Check: passing},
	))

	result := checker.Check(t.Context())
	assert.Equal(t, health.GlobalHealthy, result.Status)
	assert.True(t, result.Healthy())
	assert.Empty(t, result.Failed())
	require.Len(t, result.Details, 2)
	assert.Equal(t, health.StatusUp, result.Details["a"].Status)
	assert.Empty(t, result.Details["a"].Error)
	assert.False(t, result.Details["a"].Timestamp.IsZero())
}

func TestCheckReportsDownWithTheFailingCheckNamed(t *testing.T) {
	checker := health.NewChecker(health.WithChecks(
		health.Check{Name: "a", Check: passing},
		health.Check{Name: "b", Check: failing},
	))

	result := checker.Check(t.Context())
	assert.Equal(t, health.GlobalUnhealthy, result.Status)
	assert.False(t, result.Healthy())
	assert.Equal(t, []string{"b"}, result.Failed())
	assert.Equal(t, health.StatusUp, result.Details["a"].Status)
	assert.Equal(t, "boom", result.Details["b"].Error)
}

// Every check runs, even when one of them fails, so one probe reports the state
// of every dependency.
func TestCheckRunsEveryCheckEvenAfterAFailure(t *testing.T) {
	var calls atomic.Int32

	checker := health.NewChecker(health.WithChecks(
		health.Check{Name: "a", Check: func(context.Context) error {
			calls.Add(1)
			return errors.New("boom")
		}},
		health.Check{Name: "b", Check: func(context.Context) error {
			calls.Add(1)
			return nil
		}},
	))

	checker.Check(t.Context())
	assert.Equal(t, int32(2), calls.Load())
}

// An optional component that is down is reported but does not fail the system:
// a missing optional backend must not take a service out of rotation.
func TestOptionalCheckDoesNotAffectStatus(t *testing.T) {
	checker := health.NewChecker(health.WithChecks(
		health.Check{Name: "postgres", Check: passing},
		health.Check{Name: "valkey", Check: failing, Optional: true},
	))

	result := checker.Check(t.Context())
	assert.Equal(t, health.GlobalHealthy, result.Status)
	assert.True(t, result.Healthy())
	assert.Equal(t, health.StatusDown, result.Details["valkey"].Status)
	assert.True(t, result.Details["valkey"].Optional)
}

// A required check that is down must fail the system even when an optional one
// passed, so Optional cannot mask a real outage.
func TestRequiredFailureBeatsOptionalSuccess(t *testing.T) {
	checker := health.NewChecker(health.WithChecks(
		health.Check{Name: "postgres", Check: failing},
		health.Check{Name: "valkey", Check: passing, Optional: true},
	))

	result := checker.Check(t.Context())
	assert.Equal(t, health.GlobalUnhealthy, result.Status)
	assert.Equal(t, []string{"postgres"}, result.Failed())
}

func TestCheckTimesOutSlowCheck(t *testing.T) {
	checker := health.NewChecker(
		health.WithTimeout(50*time.Millisecond),
		health.WithCheck(health.Check{Name: "slow", Check: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}}),
	)

	result := checker.Check(t.Context())
	assert.Equal(t, health.GlobalUnhealthy, result.Status)
	assert.Equal(t, health.ErrCheckTimeout.Error(), result.Details["slow"].Error)
}

// A check that ignores its context must not hold up the aggregate: the call
// returns at the deadline even though the check function is still running.
func TestCheckDoesNotWaitForCheckThatIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	checker := health.NewChecker(
		health.WithTimeout(50*time.Millisecond),
		health.WithCheck(health.Check{Name: "stuck", Check: func(context.Context) error {
			<-release
			return nil
		}}),
	)

	start := time.Now()
	result := checker.Check(t.Context())

	assert.Less(t, time.Since(start), time.Second, "Check must return at the deadline")
	assert.Equal(t, health.GlobalUnhealthy, result.Status)
	assert.Equal(t, health.ErrCheckTimeout.Error(), result.Details["stuck"].Error)
}

// A check-specific timeout may shorten the checker timeout, so one slow
// dependency does not consume the whole budget of the probe.
func TestCheckTimeoutShortensCheckerTimeout(t *testing.T) {
	checker := health.NewChecker(
		health.WithTimeout(5*time.Second),
		health.WithCheck(health.Check{
			Name:    "slow",
			Timeout: 50 * time.Millisecond,
			Check: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}),
	)

	start := time.Now()
	result := checker.Check(t.Context())

	assert.Less(t, time.Since(start), time.Second)
	assert.Equal(t, health.GlobalUnhealthy, result.Status)
}

// A panic in a check must become a failure, never take the process down: a
// probe must not be able to crash the server.
func TestPanickingCheckBecomesDown(t *testing.T) {
	checker := health.NewChecker(health.WithCheck(health.Check{Name: "boom", Check: func(context.Context) error {
		panic("kaboom")
	}}))

	result := checker.Check(t.Context())
	assert.Equal(t, health.GlobalUnhealthy, result.Status)
	assert.Contains(t, result.Details["boom"].Error, "panicked")
	assert.Contains(t, result.Details["boom"].Error, "kaboom")
}

// A cached result must be reused, so a probe loop does not hammer the database.
func TestCacheReusesResultWithinTTL(t *testing.T) {
	var calls atomic.Int32
	checker := health.NewChecker(
		health.WithCacheTTL(time.Minute),
		health.WithCheck(health.Check{Name: "a", Check: func(context.Context) error {
			calls.Add(1)
			return nil
		}}),
	)

	first := checker.Check(t.Context())
	second := checker.Check(t.Context())

	assert.Equal(t, int32(1), calls.Load(), "the second call must reuse the cached result")
	assert.Equal(t, first.Details["a"].Timestamp, second.Details["a"].Timestamp)
}

// The cache must not mask a failure, so a cached down result is returned as is.
func TestCacheKeepsFailure(t *testing.T) {
	checker := health.NewChecker(
		health.WithCacheTTL(time.Minute),
		health.WithCheck(health.Check{Name: "a", Check: failing}),
	)

	checker.Check(t.Context())
	result := checker.Check(t.Context())
	assert.Equal(t, health.GlobalUnhealthy, result.Status)
}

// A zero TTL disables the cache, which is what the CLI --no-cache flag needs.
func TestZeroCacheRunsEveryCheck(t *testing.T) {
	var calls atomic.Int32
	checker := health.NewChecker(
		health.WithCacheTTL(0),
		health.WithCheck(health.Check{Name: "a", Check: func(context.Context) error {
			calls.Add(1)
			return nil
		}}),
	)

	checker.Check(t.Context())
	checker.Check(t.Context())
	assert.Equal(t, int32(2), calls.Load())
}

// A stale result must be refreshed once the TTL has passed.
func TestCacheRefreshesAfterTTL(t *testing.T) {
	var calls atomic.Int32
	checker := health.NewChecker(
		health.WithCacheTTL(time.Nanosecond),
		health.WithCheck(health.Check{Name: "a", Check: func(context.Context) error {
			calls.Add(1)
			return nil
		}}),
	)

	checker.Check(t.Context())
	time.Sleep(time.Millisecond)
	checker.Check(t.Context())
	assert.Equal(t, int32(2), calls.Load())
}

// The listener reports a change once, not on every probe that returns the same
// status.
func TestStatusListenerFiresOnChangeOnly(t *testing.T) {
	var (
		healthy atomic.Bool
		changes atomic.Int32
		last    atomic.Value
	)
	healthy.Store(true)

	checker := health.NewChecker(
		health.WithCacheTTL(0),
		health.WithCheck(health.Check{Name: "a", Check: func(context.Context) error {
			if healthy.Load() {
				return nil
			}
			return errors.New("boom")
		}}),
		health.WithStatusListener(func(_ context.Context, result health.Result) {
			changes.Add(1)
			last.Store(result.Status)
		}),
	)

	// The first result is not a change: there is nothing to compare against.
	checker.Check(t.Context())
	assert.Equal(t, int32(0), changes.Load())

	// The same status again is still not a change.
	checker.Check(t.Context())
	assert.Equal(t, int32(0), changes.Load())

	healthy.Store(false)
	checker.Check(t.Context())
	assert.Equal(t, int32(1), changes.Load())
	assert.Equal(t, health.GlobalUnhealthy, last.Load())

	// Staying down does not fire again.
	checker.Check(t.Context())
	assert.Equal(t, int32(1), changes.Load())
}

func TestChecksListsConfiguredNamesInOrder(t *testing.T) {
	checker := health.NewChecker(health.WithChecks(
		health.Check{Name: "postgres", Check: passing},
		health.Check{Name: "valkey", Check: passing, Optional: true},
	))

	assert.Equal(t, []string{"postgres", "valkey"}, checker.Checks())
}

// The constructor validates the check set, so a wiring mistake fails at startup
// rather than producing a result that looks healthy.
func TestNewCheckerRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		options []health.Option
		want    string
	}{
		{
			name:    "check without a name",
			options: []health.Option{health.WithCheck(health.Check{Check: passing})},
			want:    "every check needs a name",
		},
		{
			name:    "check without a function",
			options: []health.Option{health.WithCheck(health.Check{Name: "a"})},
			want:    `check "a" needs a function`,
		},
		{
			name: "duplicate check name",
			options: []health.Option{health.WithChecks(
				health.Check{Name: "a", Check: passing},
				health.Check{Name: "a", Check: passing},
			)},
			want: `duplicate check name "a"`,
		},
		{
			name:    "non-positive timeout",
			options: []health.Option{health.WithTimeout(0)},
			want:    "timeout must be greater than zero",
		},
		{
			name:    "negative cache TTL",
			options: []health.Option{health.WithCacheTTL(-time.Second)},
			want:    "cache TTL must not be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.PanicsWithValue(t, "health: "+tt.want, func() {
				health.NewChecker(tt.options...)
			})
		})
	}
}

// A checker with no checks is up, which is the right answer for a process with
// no dependencies configured yet.
func TestCheckerWithoutChecksIsUp(t *testing.T) {
	result := health.NewChecker().Check(t.Context())
	assert.Equal(t, health.GlobalHealthy, result.Status)
	assert.Empty(t, result.Details)
}

// Checks must run concurrently: two slow checks in a series would double the
// probe latency.
func TestChecksRunConcurrently(t *testing.T) {
	const delay = 100 * time.Millisecond

	checker := health.NewChecker(
		health.WithTimeout(5*time.Second),
		health.WithChecks(
			health.Check{Name: "a", Check: func(context.Context) error {
				time.Sleep(delay)
				return nil
			}},
			health.Check{Name: "b", Check: func(context.Context) error {
				time.Sleep(delay)
				return nil
			}},
		),
	)

	start := time.Now()
	result := checker.Check(t.Context())
	elapsed := time.Since(start)

	assert.Equal(t, health.GlobalHealthy, result.Status)
	assert.Less(t, elapsed, 2*delay, "checks must not run one after another")
}

// Concurrent probes must not race on the cached state. Run with -race.
func TestConcurrentChecksAreSafe(t *testing.T) {
	checker := health.NewChecker(
		health.WithCacheTTL(0),
		health.WithCheck(health.Check{Name: "a", Check: passing}),
	)

	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 10 {
				checker.Check(t.Context())
			}
		}()
	}
	for range 8 {
		<-done
	}
}
