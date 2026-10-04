package queue

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"uuid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/pkg/crypto"
)

// ctxKeyClient stores the client in a processor's context, so a task can
// enqueue the task that follows it.
type ctxKeyClient struct{}

// now returns the current time in a way tests can override.
var now = func() time.Time { return time.Now() }

type (
	// Client registers queues, adds tasks to them, and runs the dispatcher
	// that executes them. It is built once per process over the shared
	// Postgres pool, and lives and dies with the serve command.
	Client struct {
		// store is the shared database the tasks live in.
		store Store
		// log is the process logger; the queue never builds its own.
		log *slog.Logger
		// metrics is the instrumentation built with the client. It records
		// through the global meter provider, which is the no-op when the
		// metrics signal is off.
		metrics *taskMetrics
		// queues holds the registered queues tasks can be added to.
		queues queues
		// buffers is a pool of byte buffers for payload encoding.
		buffers sync.Pool
		// dispatcher claims tasks and hands them to the workers.
		dispatcher dispatcher
		// encryptor seals task payloads at rest when the client runs with one.
		encryptor *crypto.Cipher
	}

	// ClientConfig contains configuration for the Client.
	ClientConfig struct {
		// Store is the shared Postgres pool, read and written through the
		// Querier surface.
		Store Store
		// Logger is the process logger. Nil discards every line, which is
		// what a short-lived test wants.
		Logger *slog.Logger
		// NumWorkers is the number of goroutines that execute queued tasks
		// concurrently.
		NumWorkers int
		// ReleaseAfter is the duration after which a claimed task is released
		// back to the queue if it has not finished. It should be much higher
		// than every queue's Timeout, existing as the fail-safe for a worker
		// lost to a crash or a network partition.
		ReleaseAfter time.Duration
		// Encryptor seals task payloads at rest when set: every task the client
		// writes carries crypto.EncPrefix, and a claimed payload is opened
		// before its queue decodes it. The archive keeps the sealed form, so a
		// replayed task rides through the same path.
		Encryptor *crypto.Cipher
	}
)

// Client lifecycle errors, so a config that cannot run is told apart from a
// database that cannot be reached.
var (
	errMissingStore = errors.New("queue: missing store")
	errNoWorkers    = errors.New("queue: at least one worker required")
	errNoRelease    = errors.New("queue: release duration must be greater than zero")
	// errNegativePriority refuses a TaskAddOp.Priority below zero: a priority
	// is a rank among peers, and a negative rank would invert the index the
	// claim orders by.
	errNegativePriority = errors.New("queue: priority must not be negative")
)

// NewClient initializes a new Client. The dispatcher is built but not run:
// Start runs it.
func NewClient(cfg ClientConfig) (*Client, error) {
	switch {
	case cfg.Store == nil:
		return nil, errMissingStore
	case cfg.NumWorkers < 1:
		return nil, errNoWorkers
	case cfg.ReleaseAfter <= 0:
		return nil, errNoRelease
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	c := &Client{
		store:     cfg.Store,
		log:       cfg.Logger,
		metrics:   newTaskMetrics(),
		queues:    queues{registry: make(map[string]Queue)},
		buffers:   sync.Pool{New: func() any { return bytes.NewBuffer(nil) }},
		encryptor: cfg.Encryptor,
	}
	c.dispatcher.init(c, cfg.NumWorkers, cfg.ReleaseAfter)
	return c, nil
}

// Register registers a queue so tasks can be added to it.
//
// It panics on a wiring bug rather than reporting one: an empty or duplicate
// name, or a queue whose Timeout reaches past ReleaseAfter.
//
// The last one is the invariant that keeps a task from running twice.
// ReleaseAfter is how long a claim survives without a heartbeat, so a task
// still running when it elapses is handed to another worker while the first
// one keeps going. A queue Timeout at or above it therefore permits the same
// task to execute concurrently, which for a job that uploads or charges is
// the failure the setting exists to prevent. Both values are known here and
// nowhere else, so this is where the comparison belongs.
func (c *Client) Register(queue Queue) {
	cfg := queue.Config()

	if len(cfg.Name) == 0 {
		panic("queue: queue name is missing")
	}
	if cfg.Timeout > 0 && cfg.Timeout >= c.dispatcher.releaseAfter {
		panic(fmt.Sprintf(
			"queue: queue '%s' has Timeout %s, which is not below the client's ReleaseAfter %s: "+
				"a task could still be running when its claim is released, and would run twice",
			cfg.Name, cfg.Timeout, c.dispatcher.releaseAfter))
	}

	c.queues.add(queue)
}

// Add starts an operation to add one or many tasks.
func (c *Client) Add(tasks ...Task) *TaskAddOp {
	return &TaskAddOp{client: c, tasks: tasks}
}

// Start starts the dispatcher so queued tasks are executed in the background.
// To gracefully shut it down, call Stop; to hard-stop it, cancel the context.
func (c *Client) Start(ctx context.Context) {
	c.dispatcher.start(ctx)
}

// Stop attempts to gracefully shut down the dispatcher before the provided
// context is cancelled, waiting for the workers to finish their tasks. True
// is returned when every worker finished in time.
func (c *Client) Stop(ctx context.Context) bool {
	return c.dispatcher.stop(ctx)
}

// Shutdown stops the dispatcher for the container's shutdown walk: a job in
// flight finishes when it can, and a worker that did not finish in time is
// reported rather than hidden, because the task comes back on release.
func (c *Client) Shutdown(ctx context.Context) {
	if c.dispatcher.stop(ctx) {
		return
	}
	c.log.WarnContext(ctx, "queue: shutdown left tasks running")
}

// Notify tells the dispatcher that new tasks were added. It is only needed
// for tasks added through an open transaction (TaskAddOp.Executor): the
// dispatcher cannot observe a commit it does not own.
func (c *Client) Notify() {
	c.dispatcher.notify()
}

// Status returns the state of a task with a given ID. A completed task that
// its queue did not retain reads as TaskStatusNotFound: the record is gone,
// and the queue said that is the same as never having run.
func (c *Client) Status(ctx context.Context, taskID uuid.UUID) (TaskStatus, error) {
	return taskStatus(ctx, c.store, taskID)
}

// Pending reports how many unclaimed tasks a queue holds. A recurring job
// reads it before seeding itself, so a restart never adds a second schedule.
func (c *Client) Pending(ctx context.Context, queue string) (int64, error) {
	return countPending(ctx, c.store, queue)
}

// Flush deletes all pending tasks and reports how many were removed. Claimed
// tasks are untouched: they are in flight or awaiting release, and both
// states finish their lifecycle normally.
func (c *Client) Flush(ctx context.Context) (int64, error) {
	return flushPending(ctx, c.store)
}

// Cancel removes a task that has not been claimed yet and reports whether it
// was cancelled. A claimed task is already running — or awaiting the release
// of a lost worker — and cannot be revoked, so false means too late, not
// failure; the task finishes its lifecycle either way.
func (c *Client) Cancel(ctx context.Context, taskID uuid.UUID) (bool, error) {
	cancelled, err := cancelTask(ctx, c.store, taskID)
	if err != nil {
		return false, err
	}
	return cancelled, nil
}

// FlushCompleted deletes every completed task record, retention
// notwithstanding, and reports how many were removed.
func (c *Client) FlushCompleted(ctx context.Context) (int64, error) {
	return flushCompleted(ctx, c.store)
}

// DeleteExpiredCompleted removes the completed records their retention has
// expired and reports how many were removed. It is the maintenance the
// cleanup job schedules; nothing else calls it.
func (c *Client) DeleteExpiredCompleted(ctx context.Context) (int64, error) {
	return deleteExpiredCompleted(ctx, c.store, now())
}

// Dead reports how many dead tasks a queue's archive holds: the ones that
// exhausted their attempts. They are the replay's raw material.
func (c *Client) Dead(ctx context.Context, queue string) (int64, error) {
	return countDead(ctx, c.store, queue)
}

// ReplayDead re-enqueues the dead tasks the archive still carries — the ones
// that exhausted their attempts and kept their payload, not yet expired —
// ReplayDead re-enqueues the dead tasks one queue — or every queue when the
// name is empty — keeps in its archive: the ones that exhausted their
// attempts and kept their payload, not yet expired. Each rides back under a
// fresh identity with a fresh attempt budget. A dead task whose queue did
// not retain its payload cannot come back: its content is gone, and it stays
// for the cleanup to expire.
func (c *Client) ReplayDead(ctx context.Context, queue string) (int64, error) {
	var replayed int64
	err := c.store.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		rows, err := deadRows(ctx, tx, now(), queue)
		if err != nil {
			return err
		}
		replayed = int64(len(rows))
		if replayed == 0 {
			return nil
		}

		tasks := make([]*taskRow, len(rows))
		ids := make([]uuid.UUID, len(rows))
		for i, dead := range rows {
			tasks[i] = &taskRow{
				ID:        uuid.NewV7(),
				Queue:     dead.Queue,
				Payload:   dead.Payload,
				CreatedAt: now(),
			}
			ids[i] = dead.ID
		}
		if err := insertTasks(ctx, tx, tasks); err != nil {
			return err
		}
		return deleteCompletedByIDs(ctx, tx, ids)
	})
	if err != nil {
		return 0, err
	}
	if replayed > 0 {
		c.Notify()
	}
	return replayed, nil
}

// FromContext returns the client a processor's context carries, so a task can
// enqueue the task that follows it. Nil outside a processor.
func FromContext(ctx context.Context) *Client {
	if client, ok := ctx.Value(ctxKeyClient{}).(*Client); ok {
		return client
	}
	return nil
}

// save encodes and inserts the tasks of one operation. Without an executor
// the inserts run in their own transaction, which save commits and then
// notifies the dispatcher about; with one, the inserts join the caller's
// transaction and the notification is the caller's to send after the commit.
func (c *Client) save(op *TaskAddOp) ([]string, error) {
	if op.ctx == nil {
		op.ctx = context.Background()
	}

	buf, _ := c.buffers.Get().(*bytes.Buffer)
	defer c.buffers.Put(buf)

	tasks := make([]*taskRow, len(op.tasks))
	ids := make([]string, len(op.tasks))
	for i, task := range op.tasks {
		if err := encode(buf, task); err != nil {
			return nil, err
		}
		// The buffer is reused across the batch, so the payload is taken out
		// before the next encode rewrites the bytes underneath it.
		payload, err := c.seal(bytes.Clone(buf.Bytes()))
		if err != nil {
			return nil, err
		}
		row, err := newTask(task, payload, op.wait, op.priority)
		if err != nil {
			return nil, err
		}
		tasks[i] = row
		ids[i] = row.ID.String()
	}

	if op.executor != nil {
		if err := insertTasks(op.ctx, op.executor, tasks); err != nil {
			return nil, err
		}
		for _, task := range tasks {
			c.metrics.recordEnqueued(op.ctx, task.Queue)
		}
		return ids, nil
	}

	err := c.store.WithTx(op.ctx, func(ctx context.Context, tx datastore.Querier) error {
		return insertTasks(ctx, tx, tasks)
	})
	if err != nil {
		return nil, err
	}

	// The transaction is committed, so the tasks are visible and the
	// dispatcher can claim them.
	for _, task := range tasks {
		c.metrics.recordEnqueued(op.ctx, task.Queue)
	}
	c.Notify()
	return ids, nil
}

// encode serializes a task payload into a pooled buffer.
func encode(buf *bytes.Buffer, task Task) error {
	buf.Reset()
	if err := json.MarshalEncode(jsontext.NewEncoder(buf), task); err != nil {
		return fmt.Errorf("queue: encode task: %w", err)
	}
	return nil
}

// seal puts a payload into its stored form: sealed when the client runs
// with an encryptor, plain otherwise. Sealing is a write-time decision only,
// and every payload a sealing client writes carries the prefix.
func (c *Client) seal(payload []byte) ([]byte, error) {
	if c.encryptor == nil {
		return payload, nil
	}
	sealed, err := c.encryptor.Encrypt(string(payload))
	if err != nil {
		return nil, fmt.Errorf("queue: seal task: %w", err)
	}
	return []byte(sealed), nil
}

// decrypt opens a payload when the queue encrypts at rest. A payload without
// the prefix was written before the deployment started sealing — a task
// still in flight when the flag flipped — and reads as the plaintext it is;
// every stored form a sealing client writes carries the prefix.
func (c *Client) decrypt(payload []byte) ([]byte, error) {
	if c.encryptor == nil || !bytes.HasPrefix(payload, []byte(crypto.EncPrefix)) {
		return payload, nil
	}
	opened, err := c.encryptor.Decrypt(string(payload))
	if err != nil {
		return nil, fmt.Errorf("queue: open task: %w", err)
	}
	return []byte(opened), nil
}
