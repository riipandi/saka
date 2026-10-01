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
	"github.com/riipandi/tango/internal/storage"
	"github.com/riipandi/tango/pkg/testutils"
)

// expiredRecord inserts a completed record whose retention ran out, the row
// the maintenance job exists to remove.
func expiredRecord(ctx context.Context, t *testing.T, pool *datastore.Postgres) {
	t.Helper()

	_, err := pool.Exec(ctx,
		`INSERT INTO public.queue_tasks_completed
			(queue, attempts, last_duration_micro, succeeded, expires_at, last_executed_at)
		 VALUES ('retained', 1, 100, true, now() - interval '1 hour', now())`)
	require.NoError(t, err)
}

// defaultRetentionDays is the audit window the tests seed with, matching the
// configuration default.
const defaultRetentionDays = 90

// seeded wires the processors onto the client and seeds the recurring jobs —
// the two steps a serve run takes between building the queue and starting it.
func seeded(ctx context.Context, client *queue.Client, pool *datastore.Postgres, cleanupInterval time.Duration, uploader *storage.Manager) error {
	// The audit retention is seeded too, so a test that counts the recurring
	// tasks sees every one a serve run seeds. It runs on the pool the
	// migrated client was built over.
	Register(client, cleanupInterval, uploader, nil, pool, "", false, false, nil, nil, nil, nil, nil, nil)
	return NewSeeder(client, cleanupInterval, uploader, defaultRetentionDays, false, false, slog.New(slog.DiscardHandler)).Seed(ctx)
}

func TestRegisterSeedsOneMaintenanceTask(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)

	pool, client := migratedClient(t, dsn)
	require.NoError(t, seeded(t.Context(), client, pool, time.Hour, nil))

	pending, err := client.Pending(t.Context(), CleanupName)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending, "the seed adds exactly one schedule")

	// A second client over the same database — the state a restart lands in —
	// finds the pending seed and adds nothing, so the schedule never
	// multiplies.
	_, restarted := migratedClient(t, dsn)
	require.NoError(t, seeded(t.Context(), restarted, pool, time.Hour, nil))

	pending, err = client.Pending(t.Context(), CleanupName)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending)

	_ = pool
}

func TestCleanupJobPurgesExpiredRecordsAndReschedules(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)

	pool, client := migratedClient(t, dsn)
	expiredRecord(t.Context(), t, pool)

	// The interval is short, so the seeded run happens inside the test: the
	// job deletes the expired record and queues its own successor.
	require.NoError(t, seeded(t.Context(), client, pool, 50*time.Millisecond, nil))
	client.Start(t.Context())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		client.Stop(ctx)
	})

	require.Eventually(t, func() bool {
		var count int64
		err := pool.QueryRow(t.Context(),
			`SELECT count(*) FROM public.queue_tasks_completed WHERE expires_at <= now()`).Scan(&count)
		return err == nil && count == 0
	}, 5*time.Second, 20*time.Millisecond,
		"the maintenance job must delete the record its retention has expired")

	// The successor: one pending task remains, scheduled for the next
	// interval, so the chain outlives the run that seeded it.
	require.Eventually(t, func() bool {
		pending, err := client.Pending(t.Context(), CleanupName)
		return err == nil && pending == 1
	}, 5*time.Second, 20*time.Millisecond,
		"the job must queue the next run before it finishes")
}
