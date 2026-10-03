package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riipandi/saka/internal/queue"
)

// ScimSyncNotifier is the adapter between the features' change seams and
// the durable queue. The features state that accounts or groups changed;
// this type decides how the message travels. The enqueue failure is
// best-effort: the change has committed, and the hourly pass picks it up
// when the debounce message was never queued.
type ScimSyncNotifier struct {
	client *queue.Client
	log    *slog.Logger
	// debounce is the window the change-triggered pass waits out.
	debounce time.Duration
}

// NewScimSyncNotifier builds the adapter over the queue client. A nil
// logger is answered with the discard handler.
func NewScimSyncNotifier(client *queue.Client, log *slog.Logger, debounce time.Duration) *ScimSyncNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if debounce <= 0 {
		debounce = DefaultScimSyncDebounce
	}
	return &ScimSyncNotifier{client: client, log: log, debounce: debounce}
}

// ScheduleSync queues one debounced pass. Every caller sends the same
// message whatever changed — the pass re-lists the remote and reconciles,
// so the message needs no detail about what moved.
func (n *ScimSyncNotifier) ScheduleSync(ctx context.Context) error {
	if n.client == nil {
		return nil
	}
	if _, err := n.client.Add(ScimSyncDebouncedTask{
		DebounceMillis: n.debounce.Milliseconds(),
	}).Save(); err != nil {
		n.log.Warn("scimsync: change-triggered pass was not queued", "error", err)
		return nil
	}
	n.log.DebugContext(ctx, "queue: scim sync pass scheduled", "debounce", n.debounce.String())
	return nil
}

// compile-time guard: the notifier must never drift from the seam the
// features declare.
var _ interface {
	ScheduleSync(ctx context.Context) error
} = (*ScimSyncNotifier)(nil)
