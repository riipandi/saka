// Package queue is the embedded task queue: type-safe queues over the
// shared Postgres pool, executed by an in-process worker pool. It is a port
// of mikestefanello/backlite (MIT) adapted to this architecture — the schema
// comes from the migrations, the queries from go-sqlbuilder, and the logs
// from log/slog — and the engine is owned here.
//
// A task is a payload type declaring the queue it belongs to; a queue is that
// declaration plus the callback that executes it. Tasks are inserted into
// public.queue_tasks and survive a restart; the dispatcher claims, runs, and
// archives them through public.queue_tasks_completed. The concrete jobs of
// the application live in internal/jobs.
package queue

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"
	"sync"
	"time"
)

type (
	// Queue is a named queue and the callback that executes its tasks.
	Queue interface {
		// Config returns the configuration for the queue.
		Config() *QueueConfig
		// Process executes one task, decoded by the queue.
		Process(ctx context.Context, payload []byte) error
	}

	// QueueConfig is the configuration options for a queue.
	QueueConfig struct {
		// Name is the name of the queue and must be unique.
		Name string
		// MaxAttempts are the maximum number of attempts to execute this task
		// before it is marked as completed.
		MaxAttempts int
		// Timeout is the duration set on the context while executing a given
		// task. Zero means no timeout.
		Timeout time.Duration
		// Backoff is the duration a failed task will be held in the queue
		// until being retried.
		Backoff time.Duration
		// Retention dictates if and how completed tasks will be retained in
		// the database. If nil, no completed tasks will be retained.
		Retention *Retention
	}

	// Retention is the policy for how completed tasks will be retained in the
	// database.
	Retention struct {
		// Duration is the amount of time to retain a task for after
		// completion. Zero retains it forever.
		Duration time.Duration
		// OnlyFailed indicates if only failed tasks should be retained.
		OnlyFailed bool
		// Data provides options for retaining task payload data. If nil, no
		// task payload data will be retained.
		Data *RetainData
	}

	// RetainData is the policy for how task payload data will be retained in
	// the database after the task is complete.
	RetainData struct {
		// OnlyFailed indicates if task payload data should only be retained
		// for failed tasks.
		OnlyFailed bool
	}

	// queue is the type-safe implementation of Queue: the callback receives
	// the decoded task, never bytes.
	queue[T Task] struct {
		config    *QueueConfig
		processor QueueProcessor[T]
	}

	// QueueProcessor is a generic processor callback for a given queue to
	// process tasks.
	QueueProcessor[T Task] func(context.Context, T) error

	// queues is the registry of queues a client can add tasks to.
	queues struct {
		registry map[string]Queue
		sync.RWMutex
	}
)

// deadLetterRetention is how long an archived dead task stays replayable
// before the cleanup retires it. A week is long enough for an operator to
// notice a dead pile through the queue's administrative surface and replay
// it, and short enough that a queue that keeps failing does not accumulate
// payloads without bound.
const deadLetterRetention = 7 * 24 * time.Hour

// DeadLetter is the retention policy of a queue whose exhausted tasks must
// stay replayable: failures only, the payload kept so the replay can re-run
// the task, retired after deadLetterRetention. A recurring maintenance job
// does not carry it — its successor re-enqueues itself, and a dead one is
// reseeded on the next start — an outbound side effect does: a mail that
// never went out, a logout that never reached its relying party.
func DeadLetter() *Retention {
	return &Retention{
		Duration:   deadLetterRetention,
		OnlyFailed: true,
		Data:       &RetainData{OnlyFailed: true},
	}
}

// NewQueue creates a new type-safe Queue of a given Task type.
func NewQueue[T Task](processor QueueProcessor[T]) Queue {
	var task T
	cfg := task.Config()
	return &queue[T]{config: &cfg, processor: processor}
}

func (q *queue[T]) Config() *QueueConfig {
	return q.config
}

func (q *queue[T]) Process(ctx context.Context, payload []byte) error {
	var obj T
	if err := json.Unmarshal(payload, &obj); err != nil {
		return fmt.Errorf("queue: decode task: %w", err)
	}
	return q.processor(ctx, obj)
}

// add adds a queue to the registry and panics if the name is empty or has
// already been registered: a queue that cannot be told apart from another is
// a configuration bug at the call site, not a run-time condition.
func (q *queues) add(queue Queue) {
	if len(queue.Config().Name) == 0 {
		panic("queue: queue name is missing")
	}

	q.Lock()
	defer q.Unlock()
	if _, exists := q.registry[queue.Config().Name]; exists {
		panic(fmt.Sprintf("queue: queue '%s' already registered", queue.Config().Name))
	}
	q.registry[queue.Config().Name] = queue
}

// get loads a queue from the registry by name. An unknown queue is reported
// to the caller rather than panicked: a task may name a queue the process has
// not registered, and that is a logging event, not a crash.
func (q *queues) get(name string) (Queue, bool) {
	q.RLock()
	defer q.RUnlock()
	queue, ok := q.registry[name]
	return queue, ok
}

// all answers every registered queue's configuration, in name order. The
// inspection surface reads it; the registry never mutates during a run —
// Register is a startup call — so the lock only guards a concurrent build.
func (q *queues) all() []*QueueConfig {
	q.RLock()
	defer q.RUnlock()
	names := make([]string, 0, len(q.registry))
	for name := range q.registry {
		names = append(names, name)
	}
	slices.Sort(names)
	configs := make([]*QueueConfig, 0, len(names))
	for _, name := range names {
		configs = append(configs, q.registry[name].Config())
	}
	return configs
}
