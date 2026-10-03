package scheduler

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/database"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/pkg/testutils"
)

// tickTask runs on the "tick" queue: the payload a claimed tick enqueues.
type tickTask struct {
	Name string `json:"name"`
}

func (t tickTask) Config() queue.QueueConfig {
	return queue.QueueConfig{Name: "tick", MaxAttempts: 2, Timeout: time.Second}
}

// migratedPool applies the migrations to a fresh test database and returns
// the pool the schedulers share.
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
		ApplicationName: "scheduler_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })
	return pool
}

// newScheduler builds a scheduler over the pool. A nil client makes one —
// enough for the claim tests, where the enqueue rides the caller's
// transaction and the stopped dispatcher ignores the notification. A test
// that runs the dispatcher hands its client in, so a claimed tick notifies
// the dispatcher that is actually running.
func newScheduler(t *testing.T, pool *datastore.Postgres, client *queue.Client, jobs ...Job) *Scheduler {
	t.Helper()

	if client == nil {
		var err error
		client, err = queue.NewClient(queue.ClientConfig{
			Store:        pool,
			Logger:       slog.Default(),
			NumWorkers:   1,
			ReleaseAfter: time.Hour,
		})
		require.NoError(t, err)
	}

	s, err := New(Config{
		Store:  pool,
		Client: client,
		Logger: slog.Default(),
		Jobs:   jobs,
	})
	require.NoError(t, err)
	return s
}

// stateRow reads a job's state row back.
func stateRow(t *testing.T, pool *datastore.Postgres, name string) (due time.Time, fired *time.Time) {
	t.Helper()

	err := pool.QueryRow(t.Context(),
		"SELECT next_due, last_fired FROM public.scheduler_jobs WHERE name = $1", name).
		Scan(&due, &fired)
	require.NoError(t, err)
	return due, fired
}

func TestNewRefusesBrokenJobs(t *testing.T) {
	pool := migratedPool(t)

	_, err := New(Config{Store: pool, Client: nil})
	require.ErrorIs(t, err, errMissingClient)

	dup := []Job{
		{Name: "twice", Spec: "@every 1h", Task: tickTask{}},
		{Name: "twice", Spec: "@every 1h", Task: tickTask{}},
	}
	_, err = New(Config{Store: pool, Client: &queue.Client{}, Jobs: dup})
	require.ErrorContains(t, err, "registered twice")

	for name, jobs := range map[string][]Job{
		"missing name": {{Name: "", Spec: "@every 1h", Task: tickTask{}}},
		"missing task": {{Name: "notask", Spec: "@every 1h"}},
		"bad spec":     {{Name: "bad", Spec: "not a schedule", Task: tickTask{}}},
		"empty spec":   {{Name: "empty", Spec: "", Task: tickTask{}}},
	} {
		_, err = New(Config{Store: pool, Client: &queue.Client{}, Jobs: jobs})
		require.Error(t, err, name)
	}
}

// A claimed tick enqueues the task, moves the due time forward, and stamps
// last_fired — all in the transaction the enqueue rides. Seeding puts the
// first due at the schedule's next firing, so the test pulls it to now, the
// state a fire finds when the cron time arrives.
func TestClaimEnqueuesAndAdvancesTheDueTime(t *testing.T) {
	pool := migratedPool(t)
	s := newScheduler(t, pool, nil, Job{Name: "hourly", Spec: "@every 1h", Task: tickTask{Name: "tick"}})

	require.NoError(t, s.seed(t.Context(), &s.jobs[0]))
	tag, err := pool.Exec(t.Context(),
		"UPDATE public.scheduler_jobs SET next_due = CURRENT_TIMESTAMP WHERE name = 'hourly'")
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())
	dueBefore, fired := stateRow(t, pool, "hourly")
	require.Nil(t, fired)

	err = pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		_, claimErr := s.claim(ctx, tx, &s.jobs[0])
		return claimErr
	})
	require.NoError(t, err)

	dueAfter, fired := stateRow(t, pool, "hourly")
	require.NotNil(t, fired)
	require.True(t, dueAfter.After(dueBefore), "the due time advanced")

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.queue_tasks WHERE queue = 'tick'").Scan(&count))
	require.Equal(t, 1, count)
}

// The job's priority rides the enqueue: the tick's task is claimed before
// any default-priority task that arrived earlier.
func TestClaimEnqueuesAtTheJobPriority(t *testing.T) {
	pool := migratedPool(t)
	s := newScheduler(t, pool, nil, Job{
		Name: "urgent", Spec: "@every 1h", Task: tickTask{Name: "urgent"}, Priority: 7,
	})

	require.NoError(t, s.seed(t.Context(), &s.jobs[0]))
	tag, err := pool.Exec(t.Context(),
		"UPDATE public.scheduler_jobs SET next_due = CURRENT_TIMESTAMP WHERE name = 'urgent'")
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())

	err = pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		_, claimErr := s.claim(ctx, tx, &s.jobs[0])
		return claimErr
	})
	require.NoError(t, err)

	var priority int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT priority FROM public.queue_tasks WHERE queue = 'tick'").Scan(&priority))
	require.Equal(t, 7, priority)
}

// A tick that is not yet due is a no-op: the fire that finds the row already
// advanced reads a future due time and does nothing.
func TestClaimSkipsATickAnotherReplicaWon(t *testing.T) {
	pool := migratedPool(t)
	s1 := newScheduler(t, pool, nil, Job{Name: "hourly", Spec: "@every 1h", Task: tickTask{}})
	s2 := newScheduler(t, pool, nil, Job{Name: "hourly", Spec: "@every 1h", Task: tickTask{}})
	require.NoError(t, s1.seed(t.Context(), &s1.jobs[0]))
	tag, err := pool.Exec(t.Context(),
		"UPDATE public.scheduler_jobs SET next_due = CURRENT_TIMESTAMP WHERE name = 'hourly'")
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())

	// Two replicas firing the same moment, serialized by the row lock: the
	// second claims nothing, because the first moved the due time.
	for _, s := range []*Scheduler{s1, s2} {
		err := pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
			_, claimErr := s.claim(ctx, tx, &s.jobs[0])
			return claimErr
		})
		require.NoError(t, err)
	}

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.queue_tasks WHERE queue = 'tick'").Scan(&count))
	require.Equal(t, 1, count, "the second replica must not enqueue the same tick")
}

// The cron loop claims for real: a registered queue sees the task arrive,
// and the state row keeps the schedule's own bookkeeping. robfig truncates
// an @every schedule to whole seconds, so the tick fires about once a second
// however small the duration.
func TestStartFiresTheScheduleAndStopDrainsIt(t *testing.T) {
	pool := migratedPool(t)

	client, err := queue.NewClient(queue.ClientConfig{
		Store: pool, Logger: slog.Default(), NumWorkers: 1, ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)

	var executed atomic.Int64
	client.Register(queue.NewQueue(func(ctx context.Context, task tickTask) error {
		executed.Add(1)
		return nil
	}))

	// The scheduler fires onto the same client the dispatcher runs on, the
	// way serve wires them: a claimed tick notifies the running dispatcher.
	s := newScheduler(t, pool, client, Job{Name: "fast", Spec: "@every 100ms", Task: tickTask{Name: "fast"}})
	client.Start(t.Context())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		client.Stop(ctx)
	})
	s.Start(t.Context())

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && executed.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	require.Positive(t, executed.Load(), "the fired tick ran on the queue")

	_, fired := stateRow(t, pool, "fast")
	require.NotNil(t, fired)

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	require.True(t, s.Stop(stopCtx))
	before := executed.Load()
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, before, executed.Load(), "a stopped scheduler fires nothing")
}

// A schedule that was down for a while does not fire a burst: the due time
// advances from the old due, so a cron spec resumes at its next mark.
func TestClaimAdvancesFromTheOldDueNotFromNow(t *testing.T) {
	pool := migratedPool(t)
	s := newScheduler(t, pool, nil, Job{Name: "hourly", Spec: "0 3 * * *", Task: tickTask{}})

	// A row left behind by a run that died days ago.
	stale := time.Now().AddDate(0, 0, -3)
	require.NoError(t, s.seed(t.Context(), &s.jobs[0]))
	tag, err := pool.Exec(t.Context(),
		"UPDATE public.scheduler_jobs SET next_due = $1 WHERE name = 'hourly'", stale)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())

	err = pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		_, claimErr := s.claim(ctx, tx, &s.jobs[0])
		return claimErr
	})
	require.NoError(t, err)

	due, fired := stateRow(t, pool, "hourly")
	require.NotNil(t, fired)
	require.False(t, fired.Before(stale))
	// The next run is the next 03:00 after the stale due, at most a day away,
	// and never inside the stale day's already-passed window.
	require.True(t, due.After(stale))
	require.True(t, due.Before(stale.Add(25*time.Hour)), "a daily spec resumes within a day of its stale due")
}
