package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riipandi/saka/framework/queue"
)

// ScimSyncName is the queue the outbound provisioning passes run on.
const ScimSyncName = "scim_sync"

// DefaultScimSyncInterval is how often every provider is pushed to, as a
// recurring job re-enqueues itself. Pocket ID's upstream contract runs the
// same cadence hourly; an external application that is minutes behind the
// directory is the trade the interval states.
const DefaultScimSyncInterval = time.Hour

// ScimSyncTask runs one provisioning pass per provider. The interval it
// re-enqueues itself with rides in the payload, so a run that changes the
// schedule takes effect at the next run rather than needing the queue
// reseeded.
type ScimSyncTask struct {
	IntervalMillis int64 `json:"interval_millis"`
}

// Interval is the wait between two runs.
func (t ScimSyncTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the passes run on. A failed pass is retried
// shortly: the rows it did not push are still there for the next one, and
// the remote's own listing fences the writes it redoes.
func (t ScimSyncTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        ScimSyncName,
		MaxAttempts: 3,
		Timeout:     10 * time.Minute,
		Backoff:     5 * time.Minute,
	}
}

// ScimSyncer is the seam the processor drives: the scimsync service's own
// SyncAll, narrowed to the one shape the job needs. The concrete service is
// resolved where the processors are wired, and a nil syncer is a deployment
// without a provider row — the pass is skipped rather than failed.
type ScimSyncer interface {
	SyncAll(ctx context.Context) error
}

// scimSyncProcessor pushes every provider and queues the next run.
type scimSyncProcessor struct {
	syncer ScimSyncer
	log    *slog.Logger
}

func (p *scimSyncProcessor) Process(ctx context.Context, task ScimSyncTask) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("scim_sync: queue client missing from context")
	}
	interval := task.Interval()
	if interval <= 0 {
		return errors.New("scim_sync: interval must be positive")
	}

	// The next run is queued before this one succeeds, so the schedule
	// never depends on the process that ran the last one — the same
	// order the other recurring jobs keep.
	_, err := client.Add(ScimSyncTask{
		IntervalMillis: task.IntervalMillis,
	}).Ctx(ctx).Wait(interval).Save()
	if err != nil {
		return err
	}

	if p.syncer == nil {
		return nil
	}
	if err := p.syncer.SyncAll(ctx); err != nil {
		// The pass's per-provider failures are joined; the log carries the
		// detail and the retry carries the work.
		p.log.ErrorContext(ctx, "queue: scim sync pass failed", "error", err)
		return fmt.Errorf("scim sync: %w", err)
	}
	return nil
}

// scimSyncSeed is the payload the first run of the schedule is seeded with.
func scimSyncSeed(interval time.Duration) ScimSyncTask {
	return ScimSyncTask{IntervalMillis: interval.Milliseconds()}
}

// ScimSyncDebouncedName is the queue a change-triggered pass runs on. Its
// task waits out the debounce window before it pushes, so several related
// writes produce one pass.
const ScimSyncDebouncedName = "scim_sync_debounced"

// DefaultScimSyncDebounce is how long a change-triggered pass waits, the
// debounce window Pocket ID's upstream contract also keeps.
const DefaultScimSyncDebounce = 5 * time.Minute

// ScimSyncDebouncedTask is one change-triggered pass. It carries no
// payload but the window it waits out.
type ScimSyncDebouncedTask struct {
	DebounceMillis int64 `json:"debounce_millis"`
}

// Config returns the queue the change-triggered passes run on. A lost
// debounce is harmless — the hourly pass picks the change up — so the
// attempts are few and the backoff short.
func (t ScimSyncDebouncedTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        ScimSyncDebouncedName,
		MaxAttempts: 2,
		Timeout:     10 * time.Minute,
		Backoff:     time.Minute,
	}
}

// scimSyncDebouncedProcessor waits out the window and pushes once.
func scimSyncDebouncedProcessor(ctx context.Context, task ScimSyncDebouncedTask, syncer ScimSyncer, log *slog.Logger) error {
	if task.DebounceMillis > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(task.DebounceMillis) * time.Millisecond):
		}
	}
	if syncer == nil {
		return nil
	}
	if err := syncer.SyncAll(ctx); err != nil {
		log.ErrorContext(ctx, "queue: debounced scim sync pass failed", "error", err)
		return fmt.Errorf("scim sync: %w", err)
	}
	return nil
}
