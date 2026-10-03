package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riipandi/tango/internal/queue"
)

// RestrictionNotifier is the adapter between the restrictions feature's
// NoticeEnqueuer interface and the durable task queue. The lockout states
// what happened; this type decides how the message travels. The enqueue is
// best-effort: the lockout has committed, and the audit record already says
// so.
type RestrictionNotifier struct {
	client *queue.Client
	log    *slog.Logger

	// enabled is the deployment's cost decision (mailer.notifications).
	// A switched-off flow is a no-op here rather than a dropped task, so
	// the queue never carries mail nobody asked for.
	enabled bool
}

// NewRestrictionNotifier builds the adapter over the queue client. enabled
// is the notice flag; a nil logger is answered with the discard handler.
func NewRestrictionNotifier(client *queue.Client, log *slog.Logger, enabled bool) *RestrictionNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &RestrictionNotifier{client: client, log: log, enabled: enabled}
}

// EnqueueUserLockedNotice queues the notice the account's address receives.
// A nil queue — the state a test or a bare wiring is in — skips the mail
// the same way a switched-off flag does.
func (n *RestrictionNotifier) EnqueueUserLockedNotice(ctx context.Context, email, displayName string, expiresAt *time.Time) {
	if !n.enabled || n.client == nil {
		return
	}
	task := UserLockedNoticeTask{Email: email, DisplayName: displayName}
	if expiresAt != nil {
		task.ExpiresAt = expiresAt.Format(time.RFC3339)
	}
	if _, err := n.client.Add(task).Save(); err != nil {
		n.log.Warn("restrictions: locked notice was not queued", "error", err)
	}
}
