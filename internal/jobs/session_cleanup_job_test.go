package jobs

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/pkg/testutils"
)

// sessionUser seeds one account the session rows can hang from.
func sessionUser(t *testing.T, pool *datastore.Postgres) string {
	t.Helper()

	var id string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO public.users (username, email, first_name, last_name, display_name)
		 VALUES ('sweep'::citext, 'sweep@example.com', 'Sweep', 'Subject', 'Sweep Subject')
		 RETURNING id`).Scan(&id))
	return id
}

// sessionRowEnd writes one session row whose end is shaped by the arguments:
// the revocation stamp and the expiry travel independently, the way a real
// row's do. The expiry must lie in the future — the table's own check refuses
// a past one at insert — so the expired-never-stamped arm is exercised at the
// repository level, where the cutoff is a parameter and no wall clock rides in.
// A non-positive revokedAgo leaves the stamp unwritten: the row's revocation
// is the arm the sweep deletes by.
func sessionRowEnd(t *testing.T, pool *datastore.Postgres, userID string, hash string, expiresIn, revokedAgo time.Duration) {
	t.Helper()

	var revokedAt any
	if revokedAgo > 0 {
		revokedAt = time.Now().Add(-revokedAgo)
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableSessions)
	ib.Cols("user_id", "provider", "token_hash", "expires_at", "revoked_at")
	ib.Values(userID, "credential", hash, time.Now().Add(expiresIn), revokedAt)

	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

// countSessions answers how many session rows the table holds.
func countSessionRows(t *testing.T, pool *datastore.Postgres) int {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.sessions`).Scan(&count))
	return count
}

// sessionPool opens a migrated database for the retention tests.
func sessionPool(t *testing.T) (*datastore.Postgres, *queue.Client) {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)
	return migratedClient(t, dsn)
}

// sessionRegister wires the processors onto the client once — the serve run's
// wiring step, kept out of the run helper because a second registration over
// one client is a refused wiring.
func sessionRegister(t *testing.T, client *queue.Client, pool *datastore.Postgres) {
	t.Helper()

	Register(client, time.Hour, nil, nil, pool, "", false, false, nil, nil, nil, nil, nil, nil, nil)
}

// runSessionRetention executes one retention run through the queue's own
// dispatcher, the way a serve run does, and waits for the successor the
// processor queues as its last step — the signal that the run finished and
// rescheduled. The caller registers the processors once; a second
// registration over one client is a refused wiring, not a reschedule.
func runSessionRetention(t *testing.T, pool *datastore.Postgres, client *queue.Client, retentionDays int) {
	t.Helper()

	_, err := client.Add(SessionCleanupTask{
		IntervalMillis: time.Hour.Milliseconds(),
		RetentionDays:  retentionDays,
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
			SessionCleanupName).Scan(&waitUntil)
		return err == nil && waitUntil != nil && waitUntil.After(time.Now().Add(30*time.Minute))
	}, 20*time.Second, 25*time.Millisecond,
		"the retention run must finish and queue its successor")
}

// TestTheSessionRetentionDeletesWhatAgedOutAndKeepsWhatDidNot is the job's
// whole contract at the shapes the table can carry: the window decides, and a
// row inside it is untouched. The revoked arms are the two ends the sweep sees
// on real rows — a revocation older than the window goes, a fresh one stays,
// and a live row is never the sweep's business.
func TestTheSessionRetentionDeletesWhatAgedOutAndKeepsWhatDidNot(t *testing.T) {
	pool, client := sessionPool(t)
	userID := sessionUser(t, pool)

	sessionRowEnd(t, pool, userID, "h-live", 24*time.Hour, 0)
	sessionRowEnd(t, pool, userID, "h-revoked-old", 400*24*time.Hour, 60*24*time.Hour)
	sessionRowEnd(t, pool, userID, "h-revoked-recent", 400*24*time.Hour, time.Hour)
	require.Equal(t, 3, countSessionRows(t, pool))

	sessionRegister(t, client, pool)
	runSessionRetention(t, pool, client, 30)

	// The live row survives: its hash is absent from the delete's work, so
	// one row is left where three stood.
	assert.Equal(t, 2, countSessionRows(t, pool), "only the rows past the window may go")

	var live int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.sessions WHERE token_hash = 'h-live'`).Scan(&live))
	assert.Equal(t, 1, live, "the live row is never the sweep's business")
}

// TestTheSessionRetentionRunsTwiceWithoutDoingItAgain covers the idempotence
// the daily schedule relies on: a second run over the swept table deletes
// nothing, because the window's verdict does not change between runs.
func TestTheSessionRetentionRunsTwiceWithoutDoingItAgain(t *testing.T) {
	pool, client := sessionPool(t)
	userID := sessionUser(t, pool)

	sessionRowEnd(t, pool, userID, "h-revoked-old", 400*24*time.Hour, 60*24*time.Hour)
	sessionRegister(t, client, pool)
	runSessionRetention(t, pool, client, 30)
	assert.Equal(t, 0, countSessionRows(t, pool))

	// The second run happens on the wiring the first already made: the
	// registration is the serve run's step, done once.
	runSessionRetention(t, pool, client, 30)
	assert.Equal(t, 0, countSessionRows(t, pool))
}

// TestTheSessionRetentionAppliesTheWindowTheTaskCarries pins the payload's
// role: the window is read from the task rather than from the configuration,
// which is what makes a changed retention take effect at the next run.
func TestTheSessionRetentionAppliesTheWindowTheTaskCarries(t *testing.T) {
	pool, client := sessionPool(t)
	userID := sessionUser(t, pool)

	// A revocation nine days old survives the default month but not the
	// week the task carries.
	sessionRowEnd(t, pool, userID, "h-revoked-9d", 400*24*time.Hour, 9*24*time.Hour)
	sessionRegister(t, client, pool)
	runSessionRetention(t, pool, client, 7)

	assert.Equal(t, 0, countSessionRows(t, pool), "the task's own window must decide")
}

// TestTheSessionSeederSeedsTheSweepWithTheConfiguredWindow pins the wiring
// the registry relies on: a run seeds a task carrying the window it was
// configured with.
func TestTheSessionSeederSeedsTheSweepWithTheConfiguredWindow(t *testing.T) {
	pool, client := sessionPool(t)

	seeder := NewSeeder(client, time.Hour, nil, 90, 30, false, false, slog.New(slog.DiscardHandler))
	require.NoError(t, seeder.Seed(t.Context()))

	pending, err := client.Pending(t.Context(), SessionCleanupName)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending, "the seed adds exactly one schedule")

	var task []byte
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT task FROM public.queue_tasks WHERE queue = $1`, SessionCleanupName).Scan(&task))
	assert.Contains(t, string(task), `"retention_days":30`,
		"the seeded task must carry the window the run was configured with")
}

// TestTheSessionSeedDoesNotMultiply covers the restart: a second seeder over
// the same database finds the pending task and adds nothing.
func TestTheSessionSeedDoesNotMultiply(t *testing.T) {
	_, client := sessionPool(t)

	first := NewSeeder(client, time.Hour, nil, 90, 30, false, false, slog.New(slog.DiscardHandler))
	require.NoError(t, first.Seed(t.Context()))

	second := NewSeeder(client, time.Hour, nil, 90, 30, false, false, slog.New(slog.DiscardHandler))
	require.NoError(t, second.Seed(t.Context()))

	pending, err := client.Pending(t.Context(), SessionCleanupName)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending)
}
