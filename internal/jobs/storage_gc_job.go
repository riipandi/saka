package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/framework/storage"
)

// StorageGCName is the queue the garbage collection runs on.
const StorageGCName = "storage_gc"

// DefaultStorageGCInterval is how often the garbage collection runs. An
// unreferenced object is never a correctness problem — a manifest never
// names one — so the interval trades a little wasted backend space for a
// quiet schedule.
const DefaultStorageGCInterval = 6 * time.Hour

// StorageGCTask removes the objects the backend holds that no manifest
// names: the remains of a delete that finished its rows but not its object
// removal. The interval it re-enqueues itself with rides in the payload,
// the way the cleanup task carries its own.
type StorageGCTask struct {
	IntervalMillis int64 `json:"interval_millis"`
}

// Interval is the wait between two runs.
func (t StorageGCTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the collection runs on.
func (t StorageGCTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        StorageGCName,
		MaxAttempts: 3,
		Timeout:     10 * time.Minute,
		Backoff:     time.Minute,
	}
}

// gcProcessor collects the unreferenced chunks and queues the next run.
func gcProcessor(ctx context.Context, task StorageGCTask, manager *storage.Manager) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("storage_gc: queue client missing from context")
	}

	interval := task.Interval()
	if interval <= 0 {
		return errors.New("storage_gc: interval must be positive")
	}

	removed, err := manager.CollectGarbage(ctx)
	if err != nil {
		return err
	}
	if removed > 0 {
		slog.InfoContext(ctx, "queue: storage garbage collected", "objects", removed)
	}

	// The next run is queued before this one succeeds, so the schedule
	// never depends on the process that ran the last one.
	_, err = client.Add(StorageGCTask{IntervalMillis: task.IntervalMillis}).Ctx(ctx).Wait(interval).Save()
	if err != nil {
		return err
	}
	return nil
}

// gcSeed is the payload the first run of the collection is seeded with.
func gcSeed(interval time.Duration) StorageGCTask {
	return StorageGCTask{IntervalMillis: interval.Milliseconds()}
}
