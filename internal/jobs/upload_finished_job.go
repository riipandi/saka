package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/framework/storage"
)

// UploadFinishedName is the queue the upload-finished notices run on.
const UploadFinishedName = "upload_finished_notice"

// UploadFinishedTask tells one account its file reached the backend. The
// engine stages whole files — there is no per-chunk distance to report —
// so the notice is the transition the row records: the bytes are ready.
type UploadFinishedTask struct {
	// Key is the storage key the upload finished for.
	Key string `json:"key"`
	// Owner is the account the notice is addressed to, the raw UUID string
	// the staging caller recorded in the file's metadata. Empty means no
	// owner was named, and there is nobody to tell.
	Owner string `json:"owner"`
	// Size is the byte size the manifest carries.
	Size int64 `json:"size"`
}

// Config returns the queue the notices run on: the same generous attempts
// the other notifications keep, because the notice is advisory but a lost
// one is a user staring at a spinner the backend could deny.
func (t UploadFinishedTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        UploadFinishedName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
	}
}

// NoticePublisher is the seam the processor delivers through: the
// notification feature's own create, narrowed to the one shape an
// automated notice takes. The concrete service is resolved where the
// processors are wired, and a nil publisher is a deployment without the
// notification area — the notice is skipped rather than failed.
type NoticePublisher interface {
	CreateSystemNotice(ctx context.Context, userID uuid.UUID, title, body string) error
}

// uploadFinishedProcessor delivers one notice. A task with no owner is the
// state a staging caller that names nobody produces: nothing to tell, not
// a failure to retry. A malformed owner is skipped the same way — the
// metadata is the staging caller's own record, and a notice that cannot
// name its addressee must not dead-letter the queue over it.
func uploadFinishedProcessor(ctx context.Context, task UploadFinishedTask, publisher NoticePublisher) error {
	if task.Owner == "" {
		return nil
	}
	owner, err := uuid.Parse(task.Owner)
	if err != nil {
		return nil
	}
	if publisher == nil {
		return nil
	}
	return publisher.CreateSystemNotice(ctx, owner,
		"Upload complete",
		fmt.Sprintf("Your file %q is ready — %s stored.", task.Key, formatBytes(task.Size)))
}

// formatBytes renders a byte count the way a notice reads it.
func formatBytes(size int64) string {
	const kib, mib, gib = 1024, 1024 * 1024, 1024 * 1024 * 1024
	switch {
	case size >= gib:
		return fmt.Sprintf("%.1f GB", float64(size)/gib)
	case size >= mib:
		return fmt.Sprintf("%.1f MB", float64(size)/mib)
	case size >= kib:
		return fmt.Sprintf("%.1f KB", float64(size)/kib)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// UploadFinishedEnqueuer is the storage engine's after-sync side effect:
// one task per ready manifest, addressed to the owner the staging caller
// recorded. It is best-effort the way every enqueuing side effect here is —
// the file is stored and its manifest says so; a lost notice is a client's
// next poll away, not a lost fact.
type UploadFinishedEnqueuer struct {
	client *queue.Client
	log    *slog.Logger
}

// NewUploadFinishedEnqueuer builds the hook's adapter over the queue
// client.
func NewUploadFinishedEnqueuer(client *queue.Client, log *slog.Logger) *UploadFinishedEnqueuer {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &UploadFinishedEnqueuer{client: client, log: log}
}

// Uploaded is the AfterSyncHook the manager runs once a sync commits ready.
// The hook is replayed on a retry, so a repeated notice is possible the way
// any at-least-once handoff repeats — the inbox is the record, and one
// extra "ready" line costs less than a second progress source. A manifest
// that names no owner produces no task: there is nobody to address.
func (e *UploadFinishedEnqueuer) Uploaded(ctx context.Context, manifest storage.Manifest) error {
	if e.client == nil {
		return nil
	}
	owner, _ := manifest.Metadata["owner"].(string)
	if owner == "" {
		return nil
	}
	task := UploadFinishedTask{Key: manifest.Key, Owner: owner, Size: manifest.Size}
	if _, err := e.client.Add(task).Save(); err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		e.log.Warn("storage: upload finished notice was not queued", "error", err, "key", manifest.Key)
	}
	return nil
}
