package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/internal/storage"
)

// StorageUploadExpiryName is the queue the session expiry runs on.
const StorageUploadExpiryName = "storage_upload_expiry"

// DefaultStorageUploadExpiryInterval is how often the expiry sweeps the
// interrupted sessions. The window a session lives is the engine's
// constant; the interval only decides how long a reclaimed staging file
// stays dead weight past its death.
const DefaultStorageUploadExpiryInterval = 6 * time.Hour

// StorageUploadExpiryTask removes the interrupted tus sessions whose last
// activity rests past the engine's expiry window: the staging file and its
// manifest row go together. The interval it re-enqueues itself with rides
// in the payload, the way every recurring task carries its own.
type StorageUploadExpiryTask struct {
	IntervalMillis int64 `json:"interval_millis"`
}

// Interval is the wait between two runs.
func (t StorageUploadExpiryTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the expiry runs on.
func (t StorageUploadExpiryTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        StorageUploadExpiryName,
		MaxAttempts: 3,
		Timeout:     10 * time.Minute,
		Backoff:     time.Minute,
	}
}

// storageUploadExpiryProcessor expires the dead sessions and queues the
// next run.
func storageUploadExpiryProcessor(ctx context.Context, task StorageUploadExpiryTask, manager *storage.Manager) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("storage_upload_expiry: queue client missing from context")
	}

	interval := task.Interval()
	if interval <= 0 {
		return errors.New("storage_upload_expiry: interval must be positive")
	}

	// A session is dead when its last activity — the updated_at its last
	// chunk's receipt stamped — rests a full window behind now.
	expired, err := manager.ExpireSessions(ctx, time.Now().Add(-storage.TusSessionExpiry))
	if err != nil {
		return err
	}
	if expired > 0 {
		slog.InfoContext(ctx, "queue: upload sessions expired", "sessions", expired)
	}

	// The next run is queued before this one succeeds, so the schedule
	// never depends on the process that ran the last one.
	_, err = client.Add(StorageUploadExpiryTask{IntervalMillis: task.IntervalMillis}).Ctx(ctx).Wait(interval).Save()
	return err
}

// storageUploadExpirySeed is the payload the first run is seeded with.
func storageUploadExpirySeed(interval time.Duration) StorageUploadExpiryTask {
	return StorageUploadExpiryTask{IntervalMillis: interval.Milliseconds()}
}
