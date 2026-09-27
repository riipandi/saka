package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/riipandi/tango/internal/datastore"
)

// defaultFallbackPoll is the longest the dispatcher stays idle between fetches
// when nothing is scheduled. The engine is event-driven — a save, a notify, or
// a scheduled task wakes it — but a task whose worker was lost is only
// reclaimable after ReleaseAfter, and nothing else fires then, so the fallback
// keeps the reclaim bounded. One cheap indexed query a minute is what a quiet
// queue costs.
//
// The dispatcher carries it as a field so a test can shorten it, the way
// releaseAfter already works: the fallback is what bounds a wakeup that could
// not be delivered, and a test of that path has to reach it without waiting a
// minute.
const defaultFallbackPoll = time.Minute

// requeueDelay is how long a task whose queue was never registered waits
// before the dispatcher looks at it again. It cannot be processed and cannot
// be dropped — the queue may be registered by a deploy on its way — so it is
// retried on this clock, and no attempt beyond the claim is spent on it.
const requeueDelay = time.Minute

// retryDelay is how long a failed fetch waits before it tries again: a
// database that just refused a claim is given a second, not another one
// right away.
const retryDelay = time.Second

// settleTimeout bounds one outcome write. A settle runs on a context no signal
// cancels, so without a bound of its own a database that stopped answering
// would hold a worker — and with it the drain — past the window the run allowed
// for shutdown.
const settleTimeout = 10 * time.Second

// dispatcher claims queued tasks and hands them to a pool of workers.
//
// One start runs one generation of goroutines: a triggerer that folds any
// number of ready signals into a single fetch, a fetcher that claims tasks
// from the database and schedules the next fetch, and the workers that
// execute them. The completed-table cleanup is not here: it is a job of its
// own (internal/jobs), because recurring maintenance belongs there, not in
// the engine.
type dispatcher struct {
	// client is the client this dispatcher belongs to.
	client *Client
	// ctx is the context the dispatcher was started with. It ends the
	// dispatcher's own goroutines — the triggerer, the fetcher, the workers —
	// and it is deliberately not the parent of a task execution: it is the
	// run's signal context, which is already cancelled by the time a graceful
	// stop begins, and a task that inherited it would be abandoned rather
	// than drained.
	ctx context.Context
	// taskCtx is the parent of every task execution, and the context every
	// outcome write runs on. It is cancelled when the process must stop at
	// once, never by the signal that starts a graceful drain, so an in-flight
	// task keeps a live context until the drain window expires. It is created
	// in start and released in stop.
	//
	// A settle deliberately does not use the context the task ran under: that
	// one is dead exactly when the outcome most needs writing — a task whose
	// deadline expired, or one that outlived the signal — and a write on a
	// dead context leaves the task claimed until its release window. See
	// settle.
	taskCtx    context.Context
	releaseCtx context.CancelFunc
	// shutdownCtx cancels when a graceful stop is asked for. It is separate
	// from ctx so a stop is observable while the start context is still live.
	shutdownCtx  context.Context
	shutdown     context.CancelFunc
	numWorkers   int
	releaseAfter time.Duration
	// fallbackPoll is how long the dispatcher stays idle when nothing is
	// scheduled; defaultFallbackPoll unless a test shortened it.
	fallbackPoll time.Duration
	running      atomic.Bool
	// workers tracks the worker goroutines, so a stop waits for them to exit
	// rather than inferring it from the free-worker tokens: a worker that
	// leaves because the start context ended never returns its token, and
	// counting tokens therefore missed it.
	workers          sync.WaitGroup
	ticker           *time.Ticker
	tasks            chan *taskRow
	availableWorkers chan struct{}
	// workerIdle wakes a fetch that found every worker busy. It carries no
	// token: the fetcher recounts availableWorkers when the signal arrives,
	// and one buffered wakeup is the whole message.
	workerIdle chan struct{}
	// ready reports that fetching is worth considering: a task was added.
	ready chan struct{}
	// trigger carries one fetch instruction from the triggerer to the
	// fetcher. The triggered flag folds a burst of ready signals into the
	// one fetch that covers them all, so a bulk insert fetches once.
	trigger   chan struct{}
	triggered atomic.Bool
}

// init wires the dispatcher to its client. Called once, before start.
func (d *dispatcher) init(client *Client, numWorkers int, releaseAfter time.Duration) {
	d.client = client
	d.numWorkers = numWorkers
	d.releaseAfter = releaseAfter
	d.fallbackPoll = defaultFallbackPoll
}

// start starts the dispatcher. To hard-stop it, cancel the provided context;
// to stop it gracefully, call stop.
func (d *dispatcher) start(ctx context.Context) {
	// Abort if the dispatcher is already running.
	if d.running.Load() {
		return
	}

	d.ctx = ctx
	d.shutdownCtx, d.shutdown = context.WithCancel(context.Background())
	// The signal that ends the dispatcher's own goroutines must not end a
	// task in flight: WithoutCancel keeps the values and drops the
	// cancellation, so a task's context stays live until stop releases it.
	d.taskCtx, d.releaseCtx = context.WithCancel(context.WithoutCancel(ctx))
	d.tasks = make(chan *taskRow, d.numWorkers)
	d.ticker = time.NewTicker(d.fallbackPoll)
	d.ticker.Stop() // No need to poll yet.
	d.ready = make(chan struct{}, 1000)
	d.trigger = make(chan struct{}, 10)
	d.availableWorkers = make(chan struct{}, d.numWorkers)
	d.workerIdle = make(chan struct{}, 1)
	d.running.Store(true)

	for range d.numWorkers {
		d.workers.Add(1)
		go d.worker()
		d.availableWorkers <- struct{}{}
	}
	go d.triggerer()
	go d.fetcher()
	d.signalReady()
	d.client.log.InfoContext(ctx, "task dispatcher started", "workers", d.numWorkers)
}

// stop attempts to gracefully shut down the dispatcher by blocking until
// either the context is cancelled or every worker is done with its task.
func (d *dispatcher) stop(ctx context.Context) bool {
	// Cancelling the shutdown context asks the fetcher and the workers to stop
	// taking new work. A dispatcher that never started has nothing to cancel.
	if d.shutdown != nil {
		d.shutdown()
	}

	// The task context is released only once the wait below ends, so an
	// in-flight task keeps a live context across the whole drain — including
	// the outcome it writes as it finishes, which is the write a context
	// cancelled underneath it would lose. Reaching the drain deadline is the
	// one case where a task is still running when this returns: the run is out
	// of time, and the task is reclaimed by its release window rather than left
	// holding the process open.
	defer d.releaseTaskContext()

	// The wait measures the workers leaving, not the tokens they hold: a
	// worker that exits because the start context ended returns no token, so
	// counting tokens never reached the total on that path.
	//
	// It runs whether or not the dispatcher still looked running, because a
	// signal ends the fetcher first: the workers are then the only ones left
	// holding a task, and waiting for them is what keeps their outcome writes
	// alive. Treating "the fetcher has stopped" as "nothing is in flight" is
	// what cancelled a context a settling task was still using.
	done := make(chan struct{})
	go func() {
		d.workers.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return false
	case <-done:
		return true
	}
}

// releaseTaskContext releases the parent of every task execution, once. A
// dispatcher that was never started has none to release, and the two paths
// into a stop — an explicit stop and a cancelled start context — must not
// both reach it.
func (d *dispatcher) releaseTaskContext() {
	if d.releaseCtx != nil {
		d.releaseCtx()
	}
}

// triggerer listens to the ready channel and sends a trigger to the fetcher
// only when one is not already on its way, so a hundred tasks added in a
// loop cost one fetch, not a hundred.
func (d *dispatcher) triggerer() {
	for {
		select {
		case <-d.ready:
			if d.triggered.CompareAndSwap(false, true) {
				// The trigger is a wakeup, never a payload: a full buffer means
				// a trigger is already waiting, which is the same instruction
				// this one carries. Sending on it would block the one goroutine
				// that turns ready signals into fetches.
				select {
				case d.trigger <- struct{}{}:
				default:
				}
			}
		case <-d.shutdownCtx.Done():
			return
		case <-d.ctx.Done():
			return
		}
	}
}

// fetcher fetches tasks from the database when the ticker ticks or a trigger
// arrives, and stops when either context does.
func (d *dispatcher) fetcher() {
	defer func() {
		d.running.Store(false)
		d.ticker.Stop()
		close(d.tasks)
		d.client.log.InfoContext(d.ctx, "shutting down dispatcher")
	}()

	for {
		select {
		case <-d.ticker.C:
			d.ticker.Stop()
			d.fetch()
		case <-d.trigger:
			d.fetch()
		case <-d.shutdownCtx.Done():
			return
		case <-d.ctx.Done():
			return
		}
	}
}

// worker processes incoming tasks until the dispatcher stops.
func (d *dispatcher) worker() {
	defer d.workers.Done()

	for {
		select {
		case task := <-d.tasks:
			if task == nil {
				break
			}
			d.processTask(task)
			d.releaseWorker()
		case <-d.shutdownCtx.Done():
			return
		case <-d.ctx.Done():
			return
		}
	}
}

// releaseWorker returns the worker's token, or drops it when the channel is
// already full.
//
// A full channel means every worker is already counted free, so this token is
// redundant and dropping it keeps the count right. It is not merely an
// optimisation: the token is the last thing a worker does before its deferred
// Done, and a send that blocked here would keep the worker alive — a worker
// that took the nil from a closed tasks channel returns a token nothing took,
// and the stop that waits on the WaitGroup would then never return.
//
// A token that lands also wakes a fetch waiting on an empty pool. That wake
// is a signal, not a second token, and it must not block this return either.
func (d *dispatcher) releaseWorker() {
	select {
	case d.availableWorkers <- struct{}{}:
		d.signalIdle()
	default:
	}
}

// signalIdle wakes a fetch that found no free worker. A full buffer means a
// wakeup is already waiting, which carries the same news. An unstarted
// dispatcher has no channel: nothing is waiting, and a send must not block
// the worker a stop is counting.
func (d *dispatcher) signalIdle() {
	if d.workerIdle == nil {
		return
	}
	select {
	case d.workerIdle <- struct{}{}:
	default:
	}
}

// signalReady asks for another fetch without waiting for room, and reports
// whether the signal was taken.
//
// A notification dropped because the buffer is full costs nothing: the fetch an
// earlier signal armed covers the tasks that arrived since, and the fallback
// poll covers a task nothing else wakes for. Waiting for room, on the other
// hand, hangs the caller — a worker settling its last outcome, or the fetcher
// arming the next fetch — which is the stall the non-blocking send avoids.
//
// The bool is for the one caller that cannot afford to lose the wakeup: see
// schedule, which arms the fallback clock when the buffer was full.
func (d *dispatcher) signalReady() bool {
	select {
	case d.ready <- struct{}{}:
		return true
	default:
		return false
	}
}

// fetch claims the tasks the workers are free to run and schedules the next
// fetch. A failure schedules too — the fetcher retries on the fallback clock
// rather than hammering a struggling database. A run that is stopping claims
// nothing: the workers may already have left without returning a token.
func (d *dispatcher) fetch() {
	// Indicate that a task added from this point on should trigger another
	// fetch: the state this run reads is about to be settled.
	d.triggered.Store(false)

	// Every worker free to run is a task worth claiming, so the pool is never
	// handed more than it can take. The fetcher is the only receiver, so the
	// count cannot shrink before the tokens are taken below.
	workers, ok := d.awaitWorkers()
	if !ok {
		return
	}

	at := now()
	deadline := at.Add(-d.releaseAfter)
	tasks, err := claimReady(d.ctx, d.client.store, at, deadline, workers)
	if err != nil {
		d.client.log.ErrorContext(d.ctx, "queue: failed to claim tasks", "err", err.Error())
		d.client.metrics.recordClaimError(d.ctx)
		d.scheduleRetry()
		return
	}

	// One token goes with each claimed task, so the pool's free count is the
	// tokens left: a worker returning from its task puts its own back.
	for _, task := range tasks {
		<-d.availableWorkers
		d.tasks <- task
	}

	// The next fetch waits for the next claimable task, whichever queue it
	// belongs to.
	exists, wait, err := peekNext(d.ctx, d.client.store, deadline)
	if err != nil {
		d.client.log.ErrorContext(d.ctx, "queue: failed to peek next task", "err", err.Error())
	}
	d.schedule(exists, wait)
}

// awaitWorkers reports how many workers are free. It returns false when the
// run is stopping, so a fetch that found every worker busy does not outlive
// them: a worker that leaves because its context ended never returns a token.
func (d *dispatcher) awaitWorkers() (int, bool) {
	for {
		if d.ctx.Err() != nil || d.shutdownCtx.Err() != nil {
			return 0, false
		}
		if workers := len(d.availableWorkers); workers > 0 {
			return workers, true
		}
		select {
		case <-d.workerIdle:
		case <-d.shutdownCtx.Done():
			return 0, false
		case <-d.ctx.Done():
			return 0, false
		}
	}
}

// scheduleRetry arms the fallback clock after a failed fetch: the database
// that just refused a claim is given a second, not another one right away.
func (d *dispatcher) scheduleRetry() {
	d.ticker.Stop()
	d.ticker.Reset(retryDelay)
}

// schedule arms the ticker for the next fetch. No next task arms only the
// fallback, so a quiet queue wakes on its own clock — that fallback is what
// reclaims a task whose worker was lost. A ready-but-unclaimed task fetches
// again right away, and a delayed one waits for it, capped by the fallback
// so a lost claim is still reclaimed on time.
func (d *dispatcher) schedule(exists bool, wait *time.Time) {
	d.ticker.Stop()

	if !exists {
		d.ticker.Reset(d.fallbackPoll)
		return
	}
	if wait == nil {
		d.fetchNow()
		return
	}

	delay := time.Until(*wait)
	if delay <= 0 {
		d.fetchNow()
		return
	}
	if delay > d.fallbackPoll {
		delay = d.fallbackPoll
	}
	d.ticker.Reset(delay)
}

// fetchNow asks for the fetch that follows a task which is already due.
//
// Unlike notify, losing this wakeup costs the task its promptness: nothing else
// is armed to fetch it, so the fallback clock is what bounds the delay. The
// signal is tried first and the clock arms only when it could not be delivered,
// which keeps the fast path free of a ticker reset while making the slow path
// bounded either way.
func (d *dispatcher) fetchNow() {
	if !d.signalReady() {
		d.ticker.Reset(d.fallbackPoll)
	}
}

// notify tells the dispatcher that new tasks were added. A notification
// dropped because the buffer is full is fine: the fetch the earlier signals
// armed covers the tasks that arrived since.
func (d *dispatcher) notify() {
	if d.running.Load() {
		d.signalReady()
	}
}

// settle returns the context an outcome write runs on: the task context,
// bounded by settleTimeout.
//
// The context a task ran under is the wrong one for this. It is derived from
// the queue's Timeout, so it is already cancelled when the task failed because
// that timeout expired — the retry would never be written, and the task would
// sit claimed until its release window. The task context is live for the whole
// drain, so the write lands whether the run is still serving or stopping.
func (d *dispatcher) settle() (context.Context, context.CancelFunc) {
	return context.WithTimeout(d.taskCtx, settleTimeout)
}

// processTask executes one claimed task and settles its outcome: success
// deletes it, a retryable failure releases it for its backoff, and a final
// one archives it. The processor's panic is a failure, not a crash.
func (d *dispatcher) processTask(task *taskRow) {
	queue, ok := d.client.queues.get(task.Queue)
	if !ok {
		// The queue is unknown to this process. The task keeps its place and
		// waits for the registration that will process it; the claim's
		// attempt is the only one it costs.
		d.client.log.WarnContext(d.ctx, "queue: task for unregistered queue",
			"id", task.ID, "queue", task.Queue)
		ctx, cancel := d.settle()
		defer cancel()
		if err := requeueTask(ctx, d.client.store, task.ID, now().Add(requeueDelay)); err != nil {
			d.client.log.ErrorContext(ctx, "queue: failed to requeue task", "err", err.Error())
		}
		d.client.metrics.recordOutcome(d.ctx, task.Queue, outcomeUnknownQueue, 0, task.Attempts)
		return
	}

	cfg := queue.Config()

	// The execution context is the task context, deadlined by the queue's
	// timeout when it sets one, and it carries the client so a task can
	// enqueue the task that follows it. It is deliberately not the start
	// context: that one is cancelled by the signal which begins a drain, and
	// a task that inherited it would be abandoned mid-flight.
	ctx := d.taskCtx
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(d.taskCtx, now().Add(cfg.Timeout))
		defer cancel()
	}
	ctx = context.WithValue(ctx, ctxKeyClient{}, d.client)

	start := now()
	payload, err := d.client.decrypt(task.Payload)
	if err == nil {
		err = d.runProcessor(ctx, queue, task, payload)
	}
	duration := time.Since(start)

	// The outcome is written on the settle context, never on the execution
	// context above: a task that failed because its deadline expired, or that
	// outlived the signal, holds a dead context exactly when its outcome needs
	// writing.
	settleCtx, cancelSettle := d.settle()
	defer cancelSettle()

	if err == nil {
		d.client.metrics.recordOutcome(settleCtx, task.Queue, outcomeSuccess, duration, task.Attempts)
		d.taskSuccess(settleCtx, queue, task, start, duration)
		return
	}
	if remaining := queue.Config().MaxAttempts - task.Attempts; remaining >= 1 {
		d.client.metrics.recordOutcome(settleCtx, task.Queue, outcomeRetry, duration, task.Attempts)
	} else {
		d.client.metrics.recordOutcome(settleCtx, task.Queue, outcomeDead, duration, task.Attempts)
	}
	d.taskFailure(settleCtx, queue, task, start, duration, err)
}

// runProcessor invokes the queue's callback with the payload opened for
// processing — the stored form stays sealed — turning a panic into the error
// the outcome settles on.
func (d *dispatcher) runProcessor(ctx context.Context, queue Queue, task *taskRow, payload []byte) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			d.client.log.ErrorContext(ctx, "queue: panic processing task",
				"id", task.ID, "queue", task.Queue, "panic", rec)
			err = fmt.Errorf("%v", rec)
		}
	}()
	return queue.Process(ctx, payload)
}

// taskSuccess settles a successful execution: the task leaves the pending
// table and, when the queue's retention asks for it, arrives in the
// completed one. One transaction covers both, so a task is never in neither.
func (d *dispatcher) taskSuccess(ctx context.Context, queue Queue, task *taskRow, started time.Time, duration time.Duration) {
	d.client.log.InfoContext(ctx, "task processed",
		"id", task.ID, "queue", task.Queue, "duration", duration, "attempt", task.Attempts)

	retention := queue.Config().Retention
	if retention == nil || retention.OnlyFailed {
		if err := deleteTask(ctx, d.client.store, task.ID); err != nil {
			d.client.log.ErrorContext(ctx, "queue: failed to delete task", "err", err.Error())
		}
		return
	}

	completed := d.completedRecord(queue, task, started, duration, nil)
	d.archive(ctx, task, completed)
}

// taskFailure settles a failed execution: a task with attempts left is
// released for its backoff, and a task that exhausted them is archived with
// the error that finished it.
func (d *dispatcher) taskFailure(ctx context.Context, queue Queue, task *taskRow, started time.Time, duration time.Duration, taskErr error) {
	remaining := queue.Config().MaxAttempts - task.Attempts
	d.client.log.ErrorContext(ctx, "task processing failed",
		"id", task.ID, "queue", task.Queue, "duration", duration,
		"attempt", task.Attempts, "remaining", remaining, "err", taskErr.Error())

	if remaining >= 1 {
		if err := requeueTask(ctx, d.client.store, task.ID, now().Add(queue.Config().Backoff)); err != nil {
			d.client.log.ErrorContext(ctx, "queue: failed to requeue task", "err", err.Error())
		}
		// The task is due again after its backoff and nothing else wakes the
		// dispatcher for it, so the wakeup is armed rather than merely
		// signalled: a signal dropped on a full buffer would leave the retry to
		// the fallback clock.
		d.fetchNow()
		return
	}

	retention := queue.Config().Retention
	if retention == nil {
		if err := deleteTask(ctx, d.client.store, task.ID); err != nil {
			d.client.log.ErrorContext(ctx, "queue: failed to delete task", "err", err.Error())
		}
		return
	}

	completed := d.completedRecord(queue, task, started, duration, taskErr)
	d.archive(ctx, task, completed)
}

// archive moves a finished task out of the pending table and into the
// completed one in one transaction.
func (d *dispatcher) archive(ctx context.Context, task *taskRow, completed *completedRow) {
	err := d.client.store.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if err := deleteTask(ctx, tx, task.ID); err != nil {
			return err
		}
		return insertCompleted(ctx, tx, completed)
	})
	if err != nil {
		d.client.log.ErrorContext(ctx, "queue: failed to archive task",
			"id", task.ID, "queue", task.Queue, "err", err.Error())
	}
}

// completedRecord builds the archived form of a finished task under the
// queue's retention policy.
func (d *dispatcher) completedRecord(queue Queue, task *taskRow, started time.Time, duration time.Duration, taskErr error) *completedRow {
	retention := queue.Config().Retention
	c := &completedRow{
		ID:             task.ID,
		Queue:          task.Queue,
		Attempts:       task.Attempts,
		Succeeded:      taskErr == nil,
		LastDuration:   duration,
		CreatedAt:      task.CreatedAt,
		LastExecutedAt: started,
	}
	if taskErr != nil {
		message := taskErr.Error()
		c.Error = &message
	}
	if retention.Duration > 0 {
		expires := now().Add(retention.Duration)
		c.ExpiresAt = &expires
	}
	if retention.Data != nil && (!retention.Data.OnlyFailed || taskErr != nil) {
		c.Payload = task.Payload
	}
	return c
}
