package queue

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainProbeTask runs long enough to be in flight when a drain begins, the
// way a chunk upload does under its own multi-minute timeout.
type drainProbeTask struct {
	Name string `json:"name"`
}

func (drainProbeTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "drain_probe",
		MaxAttempts: 1,
		Timeout:     30 * time.Minute,
	}
}

// TestASignalDoesNotCancelAnInFlightTask is the regression this pins: the
// start context is the run's signal context, so cancelling it is exactly what
// SIGTERM does. A task in flight must keep a live context and finish, rather
// than be abandoned mid-execution and left for its release window.
func TestASignalDoesNotCancelAnInFlightTask(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Store:        migratedPool(t),
		Logger:       slog.Default(),
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)

	signalCtx, cancelSignal := context.WithCancel(t.Context())
	defer cancelSignal()

	var (
		mu       sync.Mutex
		sawErr   error
		finished bool
	)
	started := make(chan struct{})

	client.Register(NewQueue[drainProbeTask](func(ctx context.Context, _ drainProbeTask) error {
		close(started)
		select {
		case <-ctx.Done():
			mu.Lock()
			sawErr = ctx.Err()
			mu.Unlock()
		case <-time.After(2 * time.Second):
		}
		mu.Lock()
		finished = true
		mu.Unlock()
		return nil
	}))
	client.Start(signalCtx)

	save(t, client.Add(drainProbeTask{Name: "slow"}))

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the task never started")
	}

	// The signal arrives while the task runs, which is the sequence a SIGTERM
	// produces: the start context is cancelled, then the drain begins.
	cancelSignal()

	// A drain bounded like serve's own window. The task must finish inside it
	// with a live context.
	stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
	defer cancelStop()
	assert.True(t, client.Stop(stopCtx), "the task must finish inside the drain window")

	mu.Lock()
	defer mu.Unlock()
	assert.NoError(t, sawErr, "the signal must not reach the task's context")
	assert.True(t, finished, "the task must run to completion")
}

// TestStoppingAfterASignalStillReleasesTheTaskContext covers the other path
// into stop: the start context was cancelled, the dispatcher ended on its
// own, and the stop that follows must still release the task context rather
// than leave it to the process's lifetime.

func TestStoppingAfterASignalStillReleasesTheTaskContext(t *testing.T) {
	client := newTestClient(t)
	client.Register(NewQueue[probeTask](func(context.Context, probeTask) error { return nil }))

	startCtx, cancelStart := context.WithCancel(t.Context())
	client.Start(startCtx)

	// The signal, with no task in flight.
	cancelStart()

	stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancelStop()

	assert.True(t, client.Stop(stopCtx), "a stop after the dispatcher ended is still a success")

	// The dispatcher ended with the signal, and the task context is released:
	// the assertion is that the call did not panic on an already-released
	// context and that a second stop is equally harmless.
	assert.True(t, client.Stop(stopCtx))
}

// TestARestartedDispatcherFetchesAgain pins the restart contract: a second
// generation built over the same dispatcher must fetch, however the first
// one ended. A stop racing a burst of saves can leave the fold flag armed
// and the free-worker pool full — a trigger the new triggerer's CAS then
// swallows, a pool the new refill would block on. Both must be reset, or the
// restarted engine never fetches again while notify reports success.
func TestARestartedDispatcherFetchesAgain(t *testing.T) {
	client := newTestClient(t)

	processed := make(chan string, 4)
	client.Register(NewQueue[probeTask](func(_ context.Context, task probeTask) error {
		processed <- task.Name
		return nil
	}))

	startCtx, cancelStart := context.WithCancel(t.Context())
	defer cancelStart()
	client.Start(startCtx)

	// End the first generation the way a stop racing a burst of saves
	// produces it: the fold flag armed, the fetcher gone before it fetched.
	client.dispatcher.triggered.Store(true)

	stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancelStop()
	assert.True(t, client.Stop(stopCtx), "the first generation must drain")

	client.Start(startCtx)
	save(t, client.Add(probeTask{Name: "after restart"}))

	select {
	case name := <-processed:
		assert.Equal(t, "after restart", name)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the restarted dispatcher never fetched the task")
	}
}

// deadlineProbeTask outlives its own queue timeout, so the outcome it settles
// runs while the context it executed under is already dead.
type deadlineProbeTask struct {
	Name string `json:"name"`
}

func (deadlineProbeTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "deadline_probe",
		MaxAttempts: 1,
		Timeout:     100 * time.Millisecond,
	}
}

// TestAnOutcomeIsWrittenWhenTheTaskDeadlineExpired pins the settle context.
// The task runs past its queue timeout and then succeeds, so the delete that
// settles it would run on an expired context if the outcome reused the
// execution context — and a failed delete leaves the task claimed until its
// release window, a task that already finished.
func TestAnOutcomeIsWrittenWhenTheTaskDeadlineExpired(t *testing.T) {
	pool := migratedPool(t)
	client, err := NewClient(ClientConfig{
		Store:        pool,
		Logger:       slog.Default(),
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)

	started := make(chan struct{})
	client.Register(NewQueue[deadlineProbeTask](func(context.Context, deadlineProbeTask) error {
		close(started)
		// Past the queue's own 100ms timeout, ignoring the context the way a
		// processor that blocks on I/O does.
		time.Sleep(300 * time.Millisecond)
		return nil
	}))
	client.Start(t.Context())

	ids := save(t, client.Add(deadlineProbeTask{Name: "slow"}))

	// The task is running before the stop is asked for, so the row below is
	// about the settle that follows a task, not about a claim that a shutdown
	// took before a worker could pick it up.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the task never started")
	}

	// The stop waits for the worker, so the settle has run by the time it
	// returns.
	stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
	defer cancelStop()
	require.True(t, client.Stop(stopCtx))
	var remaining int
	row := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM public.queue_tasks WHERE id = $1", ids[0])
	require.NoError(t, row.Scan(&remaining))

	// A settled task is gone: the delete ran on a live context, so nothing is
	// left claimed. On the dead execution context the delete failed and the
	// row stayed, to be reclaimed only after the release window.
	assert.Zero(t, remaining, "the settle must run on a live context, not the expired one")
}

// retryOnSignalTask fails, so its outcome is the retry path — the task is
// released for its backoff rather than deleted. Its backoff is long enough
// that the released task is not claimed again while the test reads it.
type retryOnSignalTask struct {
	Name string `json:"name"`
}

func (retryOnSignalTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "retry_on_signal",
		MaxAttempts: 3,
		Timeout:     30 * time.Minute,
		Backoff:     time.Hour,
	}
}

// TestARetryIsWrittenAfterTheSignal covers the outcome write that runs after
// the start context is cancelled, which is what a SIGTERM during a failing
// task produces. The retry must still be written: it is what releases the
// claim, and a task left claimed by a signal waits out its whole release
// window before anything runs it again.
func TestARetryIsWrittenAfterTheSignal(t *testing.T) {
	pool := migratedPool(t)
	client, err := NewClient(ClientConfig{
		Store:        pool,
		Logger:       slog.Default(),
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)

	signalCtx, cancelSignal := context.WithCancel(t.Context())
	defer cancelSignal()

	var (
		started = make(chan struct{})
		fail    = make(chan struct{})
	)
	client.Register(NewQueue[retryOnSignalTask](func(context.Context, retryOnSignalTask) error {
		close(started)
		<-fail
		return errors.New("boom")
	}))
	client.Start(signalCtx)

	ids := save(t, client.Add(retryOnSignalTask{Name: "retry"}))

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the task never started")
	}

	// The signal arrives while the task runs; the task then fails, so its
	// outcome is settled with the start context already cancelled.
	cancelSignal()
	close(fail)

	stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
	defer cancelStop()
	require.True(t, client.Stop(stopCtx))

	var (
		claimedAt *time.Time
		waitUntil *time.Time
	)
	row := pool.QueryRow(context.Background(),
		"SELECT claimed_at, wait_until FROM public.queue_tasks WHERE id = $1", ids[0])
	require.NoError(t, row.Scan(&claimedAt, &waitUntil))

	assert.Nil(t, claimedAt, "the retry must clear the claim")
	require.NotNil(t, waitUntil, "the retry must schedule the next attempt")
	assert.True(t, waitUntil.After(time.Now()), "the retry must wait out its backoff")
}

// holdOpenTask blocks until the test releases it, which keeps a worker busy
// while a stop is asked for.
type holdOpenTask struct {
	Name string `json:"name"`
}

func (holdOpenTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "hold_open",
		MaxAttempts: 1,
		Timeout:     30 * time.Minute,
	}
}

// TestAStopAfterTheSignalWaitsForTheWorkers pins what a stop reports. The
// signal ends the fetcher, and the workers are then the only ones left holding
// a task; a stop that reads "the fetcher has stopped" as "nothing is in
// flight" reports success while a task is still running — and releases the
// context that task is still writing its outcome on.
func TestAStopAfterTheSignalWaitsForTheWorkers(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Store:        migratedPool(t),
		Logger:       slog.Default(),
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)

	signalCtx, cancelSignal := context.WithCancel(t.Context())
	defer cancelSignal()

	var (
		started = make(chan struct{})
		hold    = make(chan struct{})
	)
	client.Register(NewQueue[holdOpenTask](func(context.Context, holdOpenTask) error {
		close(started)
		<-hold
		return nil
	}))
	// The task is added before the dispatcher starts, so the fetch that claims
	// it is the one start triggers. A task added afterwards keeps the fetcher
	// in its claim loop, and the state below — the fetcher at rest with a
	// worker still holding a task — is what the stop has to read correctly.
	save(t, client.Add(holdOpenTask{Name: "hold"}))
	client.Start(signalCtx)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the task never started")
	}

	// The signal ends the fetcher while the worker holds the task.
	cancelSignal()

	// Wait for the fetcher to actually exit, which is the state a stop meets
	// in a real run: the signal has been observed, and the workers are the
	// only ones left holding a task. Polling it rather than sleeping keeps the
	// test from passing for the wrong reason — a stop that ran before the
	// fetcher exited would take the waiting path and never exercise this one.
	deadline := time.Now().Add(5 * time.Second)
	for client.dispatcher.running.Load() {
		if time.Now().After(deadline) {
			require.Fail(t, "the fetcher never exited after the signal")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A drain window far shorter than the task's own life, so a stop that
	// waits has to run out of time rather than report success.
	stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(t.Context()), 300*time.Millisecond)
	defer cancelStop()
	reported := client.Stop(stopCtx)

	// Release the worker whichever way the stop went, then drain for real.
	close(hold)
	finalCtx, cancelFinal := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancelFinal()
	require.True(t, client.Stop(finalCtx), "the drain after the task finished must succeed")

	assert.False(t, reported,
		"a stop must not report success while a worker still holds a task")
}
