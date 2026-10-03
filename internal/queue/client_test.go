package queue

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/database"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

// probeTask runs on the "probe" queue: the plain success path, scheduling,
// draining, and the dispatcher races.
type probeTask struct {
	Name string `json:"name"`
}

func (t probeTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "probe",
		MaxAttempts: 3,
		Timeout:     2 * time.Second,
		Backoff:     10 * time.Millisecond,
	}
}

// retainedProbeTask keeps every completed task and its payload.
type retainedProbeTask struct {
	Name string `json:"name"`
}

func (t retainedProbeTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "retained",
		MaxAttempts: 2,
		Timeout:     2 * time.Second,
		Backoff:     10 * time.Millisecond,
		Retention: &Retention{
			Data: &RetainData{},
		},
	}
}

// retryProbeTask fails until its counter is spent, on a short backoff.
type retryProbeTask struct {
	Name string `json:"name"`
}

func (t retryProbeTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "retried",
		MaxAttempts: 3,
		Timeout:     2 * time.Second,
		Backoff:     10 * time.Millisecond,
		Retention: &Retention{
			OnlyFailed: true,
			Data:       &RetainData{OnlyFailed: true},
		},
	}
}

// doomedProbeTask never succeeds and keeps both attempts' record.
type doomedProbeTask struct {
	Name string `json:"name"`
}

func (t doomedProbeTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "doomed",
		MaxAttempts: 2,
		Timeout:     2 * time.Second,
		Backoff:     10 * time.Millisecond,
		Retention: &Retention{
			Data: &RetainData{},
		},
	}
}

// ghostProbeTask is added but its queue is never registered.
type ghostProbeTask struct {
	Name string `json:"name"`
}

func (t ghostProbeTask) Config() QueueConfig {
	return QueueConfig{Name: "ghost", MaxAttempts: 3, Backoff: 10 * time.Millisecond}
}

// migratedPool applies the migrations to a fresh test database and returns
// the pool a client runs on.
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
		ApplicationName: "queue_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })
	return pool
}

// newTestClient builds a client over a migrated database, without starting
// the dispatcher. The process logger is used, so a failing engine says so in
// the test output instead of failing silently.
func newTestClient(t *testing.T) *Client {
	t.Helper()

	client, err := NewClient(ClientConfig{
		Store:        migratedPool(t),
		Logger:       slog.Default(),
		NumWorkers:   2,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)
	return client
}

// runWith starts the dispatcher with queue registered and stops it when the
// test ends, so no test leaves goroutines behind.
func runWith(t *testing.T, client *Client, queue Queue) {
	t.Helper()

	client.Register(queue)
	client.Start(t.Context())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		client.Stop(ctx)
	})
}

// save persists the operation's tasks and returns their IDs.
func save(t *testing.T, op *TaskAddOp) []string {
	t.Helper()

	saved, err := op.Save()
	require.NoError(t, err)
	require.NotEmpty(t, saved)
	return saved
}

func TestNewClientValidatesItsConfig(t *testing.T) {
	_, err := NewClient(ClientConfig{NumWorkers: 1, ReleaseAfter: time.Minute})
	assert.ErrorIs(t, err, errMissingStore)

	_, err = NewClient(ClientConfig{Store: nopStore{}, NumWorkers: 0, ReleaseAfter: time.Minute})
	assert.ErrorIs(t, err, errNoWorkers)

	_, err = NewClient(ClientConfig{Store: nopStore{}, NumWorkers: 1, ReleaseAfter: 0})
	assert.ErrorIs(t, err, errNoRelease)
}

// nopStore is a store that answers nothing: the config tests never call it.
type nopStore struct{}

// TestRegisterRefusesATimeoutAtOrPastReleaseAfter is the invariant that keeps
// a task from running twice. ReleaseAfter is how long a claim survives without
// a heartbeat, so a task still running when it elapses is handed to another
// worker while the first keeps going. A queue Timeout that reaches it permits
// exactly that, and both values are known only here.
func TestRegisterRefusesATimeoutAtOrPastReleaseAfter(t *testing.T) {
	cases := []struct {
		name      string
		timeout   time.Duration
		release   time.Duration
		wantPanic bool
	}{
		{"below the release window", 30 * time.Second, time.Minute, false},
		{"equal to the release window", time.Minute, time.Minute, true},
		{"past the release window", 2 * time.Minute, time.Minute, true},
		{"no timeout at all", 0, time.Minute, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(ClientConfig{
				Store:        nopStore{},
				NumWorkers:   1,
				ReleaseAfter: tc.release,
			})
			require.NoError(t, err)

			queue := &stubQueue{config: QueueConfig{
				Name:        "timed_probe",
				MaxAttempts: 1,
				Timeout:     tc.timeout,
			}}

			if tc.wantPanic {
				assert.Panics(t, func() { client.Register(queue) })
			} else {
				assert.NotPanics(t, func() { client.Register(queue) })
			}
		})
	}
}

// stubQueue is a Queue whose config a test sets directly, which NewQueue
// cannot do: it reads Config from the task's zero value.
type stubQueue struct {
	config QueueConfig
}

func (s *stubQueue) Config() *QueueConfig { return &s.config }

func (s *stubQueue) Process(context.Context, []byte) error { return nil }

func (nopStore) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (nopStore) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, pgx.ErrNoRows
}

func (nopStore) QueryRow(context.Context, string, ...any) pgx.Row {
	return nopRow{}
}

func (nopStore) WithTx(context.Context, func(context.Context, datastore.Querier) error) error {
	return nil
}

type nopRow struct{}

func (nopRow) Scan(...any) error { return pgx.ErrNoRows }

func TestClientProcessesATaskAndKeepsItsRecord(t *testing.T) {
	client := newTestClient(t)
	processed := make(chan retainedProbeTask, 1)
	runWith(t, client, NewQueue[retainedProbeTask](func(ctx context.Context, task retainedProbeTask) error {
		processed <- task
		return nil
	}))

	saved := save(t, client.Add(retainedProbeTask{Name: "ali"}))

	select {
	case task := <-processed:
		assert.Equal(t, "ali", task.Name)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the task was never processed")
	}

	// The record is archived after the processor returns, so the status is
	// eventual: the wait ends when the archive has.
	require.Eventually(t, func() bool {
		status, err := client.Status(t.Context(), mustID(t, saved[0]))
		return err == nil && status == TaskStatusSuccess
	}, 5*time.Second, 20*time.Millisecond,
		"the queue retains every completed task, so a success is readable")
}

func TestClientAddsTasksInsideTheCallerTransaction(t *testing.T) {
	pool := migratedPool(t)
	client, err := NewClient(ClientConfig{Store: pool, NumWorkers: 1, ReleaseAfter: time.Minute})
	require.NoError(t, err)

	// The tasks are inserted inside the transaction, so they are invisible
	// until it commits — and gone again when it rolls back.
	err = pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		_, saveErr := client.Add(probeTask{Name: "kept"}).Ctx(ctx).Executor(tx).Save()
		return saveErr
	})
	require.NoError(t, err)

	count, err := client.Pending(t.Context(), "probe")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	err = pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		_, saveErr := client.Add(probeTask{Name: "rolled-back"}).Ctx(ctx).Executor(tx).Save()
		require.NoError(t, saveErr)
		return errors.New("no")
	})
	assert.Error(t, err)

	count, err = client.Pending(t.Context(), "probe")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "the rolled-back operation left no task behind")
}

func TestDelayedTaskWaitsForItsClock(t *testing.T) {
	client := newTestClient(t)
	var processed atomic.Bool
	runWith(t, client, NewQueue[probeTask](func(ctx context.Context, task probeTask) error {
		processed.Store(true)
		return nil
	}))

	_, err := client.Add(probeTask{Name: "later"}).Wait(300 * time.Millisecond).Save()
	require.NoError(t, err)

	time.Sleep(120 * time.Millisecond)
	assert.False(t, processed.Load(), "the task must not run before its wait has passed")

	require.Eventually(t, processed.Load, 5*time.Second, 20*time.Millisecond,
		"the dispatcher must pick the task up once its wait has passed")
}

func TestFailedTaskRetriesUntilItSucceeds(t *testing.T) {
	client := newTestClient(t)
	var attempts atomic.Int32
	processed := make(chan struct{}, 1)
	runWith(t, client, NewQueue[retryProbeTask](func(ctx context.Context, task retryProbeTask) error {
		if attempts.Add(1) < 3 {
			return errors.New("not yet")
		}
		processed <- struct{}{}
		return nil
	}))

	save(t, client.Add(retryProbeTask{Name: "third time lucky"}))

	select {
	case <-processed:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the task never succeeded")
	}
	assert.Equal(t, int32(3), attempts.Load(), "the queue retried until the processor let it through")
}

func TestFailedTaskExhaustsItsAttempts(t *testing.T) {
	client := newTestClient(t)
	var attempts atomic.Int32
	runWith(t, client, NewQueue[doomedProbeTask](func(ctx context.Context, task doomedProbeTask) error {
		attempts.Add(1)
		return errors.New("always failing")
	}))

	saved := save(t, client.Add(doomedProbeTask{Name: "hopeless"}))

	require.Eventually(t, func() bool {
		status, err := client.Status(t.Context(), mustID(t, saved[0]))
		return err == nil && status == TaskStatusFailure
	}, 5*time.Second, 20*time.Millisecond, "the task must end as a retained failure")
	assert.Equal(t, int32(2), attempts.Load())

	pending, err := client.Pending(t.Context(), "doomed")
	require.NoError(t, err)
	assert.Zero(t, pending, "an archived task leaves the pending table")
}

func TestPanicBecomesAFailure(t *testing.T) {
	client := newTestClient(t)
	runWith(t, client, NewQueue[doomedProbeTask](func(ctx context.Context, task doomedProbeTask) error {
		panic("boom")
	}))

	saved := save(t, client.Add(doomedProbeTask{Name: "bomb"}))

	require.Eventually(t, func() bool {
		status, err := client.Status(t.Context(), mustID(t, saved[0]))
		return err == nil && status == TaskStatusFailure
	}, 5*time.Second, 20*time.Millisecond,
		"a panicking processor must fail its task, not the process")
}

func TestUnregisteredQueueKeepsItsTask(t *testing.T) {
	client := newTestClient(t)
	var processed atomic.Bool
	runWith(t, client, NewQueue[probeTask](func(ctx context.Context, task probeTask) error {
		processed.Store(true)
		return nil
	}))

	// The ghost queue is never registered: the task stays pending, retried
	// on the requeue clock, until a deploy registers its queue.
	saved := save(t, client.Add(ghostProbeTask{Name: "ahead of its feature"}))

	require.Never(t, processed.Load, time.Second, 50*time.Millisecond,
		"a task whose queue is not registered must not run")

	status, err := client.Status(t.Context(), mustID(t, saved[0]))
	require.NoError(t, err)
	assert.Equal(t, TaskStatusPending, status)
}

func TestTwoDispatchersNeverRunTheSameTaskTwice(t *testing.T) {
	// Two clients share one database, the way two processes of a scaled
	// deployment do: every task is claimed once, whoever gets there first.
	const tasks = 24
	var executions atomic.Int32

	first, second := newTestClient(t), newTestClient(t)
	processor := func(ctx context.Context, task probeTask) error {
		executions.Add(1)
		return nil
	}
	runWith(t, first, NewQueue[probeTask](processor))
	runWith(t, second, NewQueue[probeTask](processor))

	payload := make([]Task, 0, tasks)
	for range tasks {
		payload = append(payload, probeTask{Name: "raced"})
	}
	saved := save(t, first.Add(payload...))
	require.Len(t, saved, tasks)

	require.Eventually(t, func() bool {
		return executions.Load() == tasks
	}, 10*time.Second, 20*time.Millisecond,
		"every task must run exactly once across both dispatchers")
	assert.Equal(t, int32(tasks), executions.Load())
}

func TestStopDrainsInFlightTasks(t *testing.T) {
	client := newTestClient(t)
	finished := make(chan struct{}, 1)
	client.Register(NewQueue[probeTask](func(ctx context.Context, task probeTask) error {
		time.Sleep(300 * time.Millisecond)
		finished <- struct{}{}
		return nil
	}))

	startCtx, cancelStart := context.WithCancel(t.Context())
	defer cancelStart()
	client.Start(startCtx)

	_, err := client.Add(probeTask{Name: "in flight"}).Save()
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond) // Let the worker claim it.

	stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancelStop()
	assert.True(t, client.Stop(stopCtx), "the in-flight task must finish before the stop returns")

	select {
	case <-finished:
	default:
		require.Fail(t, "the task finished without reporting")
	}
}

func TestFlushDropsOnlyPendingTasks(t *testing.T) {
	client := newTestClient(t)

	saved := save(t, client.Add(
		probeTask{Name: "one"}, probeTask{Name: "two"},
	))
	require.Len(t, saved, 2)

	removed, err := client.Flush(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(2), removed)

	again, err := client.Flush(t.Context())
	require.NoError(t, err)
	assert.Zero(t, again, "a second flush has nothing left to drop")
}

func TestStatusOfAnUnknownTask(t *testing.T) {
	client := newTestClient(t)

	status, err := client.Status(t.Context(), mustID(t, "00000000-0000-7000-8000-000000000000"))
	require.NoError(t, err)
	assert.Equal(t, TaskStatusNotFound, status)
}

func TestRegisterPanicsOnADuplicateQueue(t *testing.T) {
	client := newTestClient(t)

	client.Register(NewQueue[probeTask](func(context.Context, probeTask) error { return nil }))
	assert.Panics(t, func() {
		client.Register(NewQueue[probeTask](func(context.Context, probeTask) error { return nil }))
	})
}

func TestClientSatisfiesTheContainerShutdown(t *testing.T) {
	// The registry hands the client to the injector's shutdown walk: the
	// interface is the wiring contract, so it is asserted where the client
	// is built rather than hoped for.
	var _ do.ShutdownerWithContext = (*Client)(nil)
}

// mustID parses a saved task ID.
func mustID(t *testing.T, saved string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(saved)
	require.NoError(t, err)
	return id
}
