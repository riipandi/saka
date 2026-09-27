package jobs

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/pkg/testutils"
)

// auditRecordAge writes one record and ages it. The column is written directly
// because the writer stamps it from the database's own clock — which is the
// point of the column, and also why a test cannot make an old record by
// waiting.
func auditRecordAge(t *testing.T, pool *datastore.Postgres, event string, age time.Duration) {
	t.Helper()

	_, err := pool.Exec(t.Context(),
		`INSERT INTO public.audit_logs (event, trigger_type, created_at) VALUES ($1, 'user', now() - $2::interval)`,
		event, age.String())
	require.NoError(t, err)
}

// countAuditRecords answers how many records the table holds, which is what
// the retention is asserted on.
func countAuditRecords(t *testing.T, pool *datastore.Postgres) int {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs`).Scan(&count))
	return count
}

// auditEventNames answers the events the table holds, sorted, so a test can
// assert on which records survived.
func auditEventNames(t *testing.T, pool *datastore.Postgres) []string {
	t.Helper()

	rows, err := pool.Query(t.Context(), `SELECT event FROM public.audit_logs ORDER BY event`)
	require.NoError(t, err)
	defer rows.Close()

	var events []string
	for rows.Next() {
		var event string
		require.NoError(t, rows.Scan(&event))
		events = append(events, event)
	}
	require.NoError(t, rows.Err())
	return events
}

// auditPool opens a migrated database for the retention tests.
func auditPool(t *testing.T) (*datastore.Postgres, *queue.Client) {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)
	return migratedClient(t, dsn)
}

// runRetention executes one retention run through the queue's own dispatcher,
// the way a serve run does, and waits for the task to reach a terminal state.
//
// The task is added with a long interval, so the successor it queues sits far
// enough out that a test asserting on the pending count sees one schedule
// rather than a loop.
func runRetention(t *testing.T, pool *datastore.Postgres, client *queue.Client, retentionDays int) {
	t.Helper()

	// The processors are wired first, the way a serve run wires them: a task
	// whose processor is not registered never runs, and the successor this
	// helper waits for is the processor's own last step.
	Register(client, time.Hour, nil, nil, pool, "", false, false, nil)

	_, err := client.Add(AuditCleanupTask{
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

	// The successor is the signal: it is queued by the processor as its last
	// step, so a row whose wait lands an hour out means the run finished and
	// rescheduled. The task's own row is gone by then, replaced by this one.
	require.Eventually(t, func() bool {
		var waitUntil *time.Time
		err := pool.QueryRow(t.Context(),
			`SELECT wait_until FROM public.queue_tasks WHERE queue = $1`,
			AuditCleanupName).Scan(&waitUntil)
		return err == nil && waitUntil != nil && waitUntil.After(time.Now().Add(30*time.Minute))
	}, 20*time.Second, 25*time.Millisecond,
		"the retention run must finish and queue its successor")
}

// TestTheRetentionDeletesWhatAgedOutAndKeepsWhatDidNot is the job's whole
// contract: the window decides, and a record inside it is untouched.
func TestTheRetentionDeletesWhatAgedOutAndKeepsWhatDidNot(t *testing.T) {
	pool, client := auditPool(t)

	auditRecordAge(t, pool, "ancient", 120*24*time.Hour)
	auditRecordAge(t, pool, "yesterday", 30*24*time.Hour)
	auditRecordAge(t, pool, "now", time.Minute)
	require.Equal(t, 3, countAuditRecords(t, pool))

	runRetention(t, pool, client, 90)

	assert.Equal(t, []string{"now", "yesterday"}, auditEventNames(t, pool),
		"only the record past the window may go")
}

// TestTheRetentionKeepsEveryRecordInsideTheWindow keeps a fresh deployment's
// table from being emptied: nothing has aged out, so nothing goes.
func TestTheRetentionKeepsEveryRecordInsideTheWindow(t *testing.T) {
	pool, client := auditPool(t)

	auditRecordAge(t, pool, "sign_in", time.Hour)
	runRetention(t, pool, client, 90)

	assert.Equal(t, 1, countAuditRecords(t, pool))
}

// TestTheRetentionAppliesTheWindowTheTaskCarries pins the payload's role: the
// window is read from the task rather than from the configuration, which is
// what makes a changed retention take effect at the next run.
func TestTheRetentionAppliesTheWindowTheTaskCarries(t *testing.T) {
	pool, client := auditPool(t)

	auditRecordAge(t, pool, "old", 10*24*time.Hour)
	// A window shorter than the record's age, so it goes even though the
	// configured default would have kept it.
	runRetention(t, pool, client, 7)

	assert.Zero(t, countAuditRecords(t, pool), "the task's own window must decide")
}

// TestTheSeederSeedsTheRetentionWithTheConfiguredWindow pins the wiring the
// registry relies on: a run seeds a task carrying the window it was
// configured with.
func TestTheSeederSeedsTheRetentionWithTheConfiguredWindow(t *testing.T) {
	pool, client := auditPool(t)

	// Only the retention is seeded here: the other recurring jobs would need
	// the uploader, which this test does not build.
	seeder := NewSeeder(client, time.Hour, nil, 30, false, slog.New(slog.DiscardHandler))
	require.NoError(t, seeder.Seed(t.Context()))

	pending, err := client.Pending(t.Context(), AuditCleanupName)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending, "the seed adds exactly one schedule")

	var task []byte
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT task FROM public.queue_tasks WHERE queue = $1`, AuditCleanupName).Scan(&task))
	assert.Contains(t, string(task), `"retention_days":30`,
		"the seeded task must carry the window the run was configured with")
}

// TestTheRetentionSeedDoesNotMultiply covers the restart: a second seeder over
// the same database finds the pending task and adds nothing, so the schedule
// survives a restart without doubling.
func TestTheRetentionSeedDoesNotMultiply(t *testing.T) {
	_, client := auditPool(t)

	first := NewSeeder(client, time.Hour, nil, 90, false, slog.New(slog.DiscardHandler))
	require.NoError(t, first.Seed(t.Context()))

	second := NewSeeder(client, time.Hour, nil, 90, false, slog.New(slog.DiscardHandler))
	require.NoError(t, second.Seed(t.Context()))

	pending, err := client.Pending(t.Context(), AuditCleanupName)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending)
}
