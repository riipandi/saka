package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/internal/storage"
)

// StorageUploadName is the queue the uploads run on.
const StorageUploadName = "storage_upload"

// StorageUploadTask syncs one staging file into the backend: hash it, store
// it whole under its bucket/key, commit the ready manifest. The watcher
// enqueues it once a staging path settles, and a caller may enqueue it
// itself — the processor is idempotent, so a repeated task syncs nothing
// and returns.
type StorageUploadTask struct {
	// Key is the bucket-scoped staging reference (`bucket/key`), the path
	// the manifest and the backend both name.
	Key string `json:"key"`
}

// Config returns the queue the uploads run on. The attempts are generous
// because a backend outage is the ordinary reason for a retry, and the
// timeout bounds one file's whole sync — hashing and the PUT.
func (t StorageUploadTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        StorageUploadName,
		MaxAttempts: 5,
		Timeout:     30 * time.Minute,
		Backoff:     30 * time.Second,
	}
}

// uploadProcessor runs the sync for one file.
func uploadProcessor(ctx context.Context, task StorageUploadTask, manager *storage.Manager) error {
	if task.Key == "" {
		return errors.New("storage_upload: task carries no key")
	}
	if err := manager.Sync(ctx, task.Key); err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: storage upload done", "key", task.Key)
	return nil
}
