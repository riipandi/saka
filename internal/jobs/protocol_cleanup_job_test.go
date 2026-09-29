package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/pkg/testutils"
)

// The protocol state's retention: a row whose expiry passed the grace
// window is reaped, a row inside the grace, a live row, and a row whose
// expiry is NULL all survive, and the run re-enqueues its successor.

// seedSession inserts one oauth2_sessions row directly, the expiry the
// test chooses. A nil expiresAt is the column's NULL.
func seedSession(t *testing.T, pool *datastore.Postgres, kind, key string, expiresAt any) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO public.oauth2_sessions (kind, key, request_id, request_data, expires_at)
		 VALUES ($1, $2, $2, '{"probe":true}'::jsonb, $3)`,
		kind, key, expiresAt)
	require.NoError(t, err)
}

// countSessions answers how many rows of a kind the table holds.
func countSessions(t *testing.T, pool *datastore.Postgres, kind string) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth2_sessions WHERE kind = $1`, kind).Scan(&count))
	return count
}

// seedJTI inserts one claimed JWT ID, the expiry the test chooses.
func seedJTI(t *testing.T, pool *datastore.Postgres, jti string, expiresAt time.Time) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO public.oauth2_jtis (jti, expires_at) VALUES ($1, $2)
		 ON CONFLICT (jti) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		jti, expiresAt)
	require.NoError(t, err)
}

// countJTIs answers how many claims a jti carries in the table.
func countJTIs(t *testing.T, pool *datastore.Postgres, jti string) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth2_jtis WHERE jti = $1`, jti).Scan(&count))
	return count
}

// registerProtocolQueues wires the sweep's processor the way a serve run
// does. It is called once per test — a second Register on one client
// panics on the shared queue name.
func registerProtocolQueues(t *testing.T, pool *datastore.Postgres, client *queue.Client) {
	t.Helper()
	Register(client, time.Hour, nil, nil, pool, "", false, false, nil, nil, nil, nil)
}

// runProtocolSweep adds one sweep task and waits for the successor the
// processor queues as its last step — a successor scheduled an hour out
// exists only after the run finished.
func runProtocolSweep(t *testing.T, pool *datastore.Postgres, client *queue.Client) {
	t.Helper()

	_, err := client.Add(ProtocolCleanupTask{
		IntervalMillis: time.Hour.Milliseconds(),
	}).Save()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	client.Start(ctx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer stopCancel()
		client.Stop(stopCtx)
	})

	require.Eventually(t, func() bool {
		var waitUntil *time.Time
		err := pool.QueryRow(t.Context(),
			`SELECT wait_until FROM public.queue_tasks WHERE queue = $1`,
			ProtocolCleanupName).Scan(&waitUntil)
		return err == nil && waitUntil != nil && waitUntil.After(time.Now().Add(30*time.Minute))
	}, 30*time.Second, 50*time.Millisecond,
		"the sweep must finish and queue its successor")
}

func TestProtocolCleanupReapsOnlyWhatExpiredBeyondTheGrace(t *testing.T) {
	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)
	pool, client := migratedClient(t, dsn)
	registerProtocolQueues(t, pool, client)

	// Two hours old: past the grace, reaped. Thirty minutes old: inside
	// the hour of grace, still standing. One hour out: live. NULL expiry:
	// the logout-without-client shape, never swept.
	seedSession(t, pool, "grant", "expired", time.Now().Add(-2*time.Hour))
	seedSession(t, pool, "grant", "in-grace", time.Now().Add(-30*time.Minute))
	seedSession(t, pool, "grant", "live", time.Now().Add(time.Hour))
	seedSession(t, pool, "logout", "no-expiry", nil)

	runProtocolSweep(t, pool, client)

	assert.Equal(t, 2, countSessions(t, pool, "grant"), "only the row past the grace went; the in-grace and live rows stand")
	assert.Equal(t, 1, countSessions(t, pool, "logout"), "a NULL expiry is never swept")

	var expired, inGrace, live int
	for key, dest := range map[string]*int{"expired": &expired, "in-grace": &inGrace, "live": &live} {
		require.NoError(t, pool.QueryRow(t.Context(),
			`SELECT count(*) FROM public.oauth2_sessions WHERE kind = 'grant' AND key = $1`, key).Scan(dest))
	}
	assert.Equal(t, 0, expired, "the row past the grace is reaped")
	assert.Equal(t, 1, inGrace, "a row inside the grace survives")
	assert.Equal(t, 1, live, "a live row survives")
}

func TestProtocolCleanupReapsExpiredJTIsAndKeepsTheLiveWindow(t *testing.T) {
	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)
	pool, client := migratedClient(t, dsn)
	registerProtocolQueues(t, pool, client)

	// One claimed jti past the grace — its replay window closed, the
	// claim is history — one inside it, whose replay must still be
	// refused until its own expiry passes.
	seedJTI(t, pool, "neveu", time.Now().Add(-2*time.Hour))
	seedJTI(t, pool, "vetra", time.Now().Add(10*time.Minute))

	runProtocolSweep(t, pool, client)

	assert.Equal(t, 0, countJTIs(t, pool, "neveu"), "the expired claim is reaped")
	assert.Equal(t, 1, countJTIs(t, pool, "vetra"), "a claim inside its window survives")

	// The reaped jti is claimable again, the way a fresh JWT would find
	// the table: the window's expiry is what frees the id, not the sweep.
	seedJTI(t, pool, "neveu", time.Now().Add(10*time.Minute))
	assert.Equal(t, 1, countJTIs(t, pool, "neveu"),
		"a reaped jti can be claimed again")
}

func TestProtocolCleanupKeepsAnActiveRedemptionWorking(t *testing.T) {
	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)
	pool, client := migratedClient(t, dsn)
	registerProtocolQueues(t, pool, client)

	// One sweep over a populated protocol state: the row a redemption
	// still needs outlives it, the dead row does not.
	seedSession(t, pool, "grant", "live-grant", time.Now().Add(time.Hour))
	seedSession(t, pool, "grant", "expired-grant", time.Now().Add(-2*time.Hour))
	runProtocolSweep(t, pool, client)

	var live int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth2_sessions WHERE key = $1`, "live-grant").Scan(&live))
	assert.Equal(t, 1, live, "the live row outlives the sweep")

	var expired int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth2_sessions WHERE key = $1`, "expired-grant").Scan(&expired))
	assert.Equal(t, 0, expired, "the expired row does not outlive the sweep")
}
