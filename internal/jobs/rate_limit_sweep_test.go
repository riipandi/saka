package jobs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/testutils"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// TestTheRateLimitSweepDeletesLapsedWindowsAndKeepsLiveOnes pins the sweep's
// edge: a window that ended more than one full window ago goes, a window that
// just lapsed stays (the next request resets it in place), and a live one is
// untouched.
func TestTheRateLimitSweepDeletesLapsedWindowsAndKeepsLiveOnes(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "rate_limit_sweep_test")
	now := time.Now()
	seed := func(key string, started time.Duration) {
		_, err := pool.Exec(t.Context(),
			`INSERT INTO public.rate_limits (key, count, max_requests, window_seconds, window_start)
			 VALUES ($1, 1, 10, 60, $2)`, key, now.Add(started))
		require.NoError(t, err)
	}
	seed("auth_ancient", -time.Hour)          // ended 59 minutes ago
	seed("auth_two_windows", -3*time.Minute)  // ended two windows ago
	seed("auth_just_lapsed", -90*time.Second) // ended half a window ago
	seed("auth_live", -10*time.Second)

	removed, err := deleteLapsedRateLimits(t.Context(), pool, now)
	require.NoError(t, err)
	assert.Equal(t, 2, removed)

	var left []string
	rows, err := pool.Query(t.Context(), `SELECT key FROM public.rate_limits ORDER BY key`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var key string
		require.NoError(t, rows.Scan(&key))
		left = append(left, key)
	}
	assert.Equal(t, []string{"auth_just_lapsed", "auth_live"}, left)
}

// TestTheRateLimitSweepWorksThroughABacklog crosses the batch size so the
// loop's continuation, not just one statement, is exercised.
func TestTheRateLimitSweepWorksThroughABacklog(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "rate_limit_backlog_test")
	_, err := pool.Exec(t.Context(),
		`INSERT INTO public.rate_limits (key, count, max_requests, window_seconds, window_start)
		 SELECT 'auth_' || g, 1, 10, 60, now() - interval '1 day' FROM generate_series(1, $1) g`,
		rateLimitSweepBatch*2+7)
	require.NoError(t, err)

	removed, err := deleteLapsedRateLimits(t.Context(), pool, time.Now())
	require.NoError(t, err)
	assert.Equal(t, rateLimitSweepBatch*2+7, removed)
}
