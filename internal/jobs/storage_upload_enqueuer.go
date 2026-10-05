package jobs

import (
	"context"
	"log/slog"

	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/framework/storage"
)

// UploadEnqueuer is the storage engine's completion seam, adapted over the
// durable queue: the final chunk's handler hands the finished staging file
// here, and the queue carries it to the sync. The engine states what
// happened — an upload finished — and the queue decides how the work
// travels, the way every notice between the two travels.
type UploadEnqueuer struct {
	client *queue.Client
	log    *slog.Logger
}

// NewUploadEnqueuer builds the adapter over the shared client.
func NewUploadEnqueuer(client *queue.Client, log *slog.Logger) *UploadEnqueuer {
	return &UploadEnqueuer{client: client, log: log}
}

// EnqueueUpload queues the sync for one finished staging file. The loss it
// can suffer is bounded: the staging row the sweep reads is written before
// the enqueue is attempted, so a failed enqueue is a delayed upload, not a
// lost one.
func (e *UploadEnqueuer) EnqueueUpload(ctx context.Context, ref string) error {
	if _, err := e.client.Add(StorageUploadTask{Key: ref}).Ctx(ctx).Save(); err != nil {
		e.log.ErrorContext(ctx, "jobs: enqueue upload", "ref", ref, "err", err)
		return err
	}
	return nil
}

// SweepUploads re-enqueues the staging uploads a dead run left behind:
// whole files whose manifest row is pending and carries no session marker.
// It runs once at a serve's start — the boot-time recovery the staging
// watcher's startup scan used to be — and answers how many uploads it
// requeued, so the run's log can say what it inherited.
func SweepUploads(ctx context.Context, manager *storage.Manager, client *queue.Client, log *slog.Logger) (int, error) {
	refs, err := manager.PendingStagedRefs(ctx)
	if err != nil {
		return 0, err
	}
	for _, ref := range refs {
		if _, err := client.Add(StorageUploadTask{Key: ref}).Ctx(ctx).Save(); err != nil {
			return 0, err
		}
	}
	if len(refs) > 0 {
		log.InfoContext(ctx, "jobs: staged uploads requeued", "count", len(refs))
	}
	return len(refs), nil
}
