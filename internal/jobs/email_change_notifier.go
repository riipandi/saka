package jobs

import (
	"context"
	"log/slog"

	"github.com/riipandi/saka/framework/queue"
)

// EmailChangeNotifier is the adapter between the verification service's
// changeNoticeEnqueuer interface and the durable task queue. The service
// states what happened; this type decides how the message travels. Both
// notices are best-effort: the request and the confirmation have committed,
// and the audit record already says so.
type EmailChangeNotifier struct {
	client *queue.Client
	log    *slog.Logger

	// enabled is the deployment's cost decision (mailer.notifications). A
	// switched-off flow is a no-op here rather than a dropped task, so the
	// queue never carries mail nobody asked for. The confirm-link message
	// the request itself sends is transactional and never gated.
	enabled bool
}

// NewEmailChangeNotifier builds the adapter over the queue client. enabled
// gates both notices; a nil logger is answered with the discard handler.
func NewEmailChangeNotifier(client *queue.Client, log *slog.Logger, enabled bool) *EmailChangeNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &EmailChangeNotifier{client: client, log: log, enabled: enabled}
}

// EnqueuePendingNotice queues the notice the old address receives while the
// change is pending.
func (n *EmailChangeNotifier) EnqueuePendingNotice(ctx context.Context, notice EmailChangeNotice) {
	n.enqueue(ctx, notice, EmailChangeNoticePending)
}

// EnqueueSuccessNotice queues the confirmation the new address receives once
// the change completed.
func (n *EmailChangeNotifier) EnqueueSuccessNotice(ctx context.Context, notice EmailChangeNotice) {
	n.enqueue(ctx, notice, EmailChangeNoticeSuccess)
}

func (n *EmailChangeNotifier) enqueue(ctx context.Context, notice EmailChangeNotice, kind EmailChangeNoticeKind) {
	if !n.enabled {
		return
	}
	if _, err := n.client.Add(EmailChangeNoticeTask{
		Email:       notice.To,
		DisplayName: notice.DisplayName,
		OldEmail:    notice.OldEmail,
		NewEmail:    notice.NewEmail,
		Kind:        kind,
	}).Save(); err != nil {
		n.log.Warn("verification: email change notice was not queued", "error", err, "user_id", notice.UserID)
	}
}
