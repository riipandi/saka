package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/modules/identity/user"
)

// BanNotifier is the adapter between the user service's banNotifier
// interface and the durable task queue. The user service states what
// happened; this type decides how the message travels. Every send is
// fire-and-forget — the queue is durable, its retries are the delivery
// guarantee, and a failure to enqueue is logged rather than propagated,
// because the ban itself has already committed.
type BanNotifier struct {
	client *queue.Client
	log    *slog.Logger

	// bannedEnabled and unbannedEnabled are the deployment's cost decisions
	// (mailer.notifications), one per flow. A switched-off flow is a no-op
	// here rather than a dropped task, so the queue never carries mail
	// nobody asked for.
	bannedEnabled   bool
	unbannedEnabled bool
}

// NewBanNotifier builds the ban notifier over the queue client. The two
// flags gate each notice flow separately; a nil logger is answered with the
// discard handler, so a caller without logging stays silent rather than
// panicking on the first missed enqueue.
func NewBanNotifier(client *queue.Client, log *slog.Logger, bannedEnabled, unbannedEnabled bool) *BanNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &BanNotifier{client: client, log: log, bannedEnabled: bannedEnabled, unbannedEnabled: unbannedEnabled}
}

// UserBanned queues the suspension notice. The expiry is rendered here —
// RFC 3339, or empty when the ban never lifts — because the template's data
// travels the queue as JSON. The subject's ban reason is the same sentence
// the audit record keeps.
func (n *BanNotifier) UserBanned(ctx context.Context, email string, subject user.UserView, expiresAt *time.Time) {
	if !n.bannedEnabled {
		return
	}
	task := UserBannedEmailTask{
		Email:       email,
		DisplayName: subject.DisplayName,
		Reason:      derefString(subject.BanReason),
	}
	if expiresAt != nil {
		task.ExpiresAt = expiresAt.Format(time.RFC3339)
	}
	if _, err := n.client.Add(task).Save(); err != nil {
		// The ban committed; the notice is best-effort. The log is the
		// trace an operator follows when an account says it was never
		// told — the audit record does not depend on this enqueue.
		n.log.Warn("user: ban notification was not queued", "error", err)
	}
}

// UserUnbanned queues the reinstatement notice.
func (n *BanNotifier) UserUnbanned(ctx context.Context, email string, subject user.UserView) {
	if !n.unbannedEnabled {
		return
	}
	if _, err := n.client.Add(UserUnbannedEmailTask{
		Email:       email,
		DisplayName: subject.DisplayName,
	}).Save(); err != nil {
		n.log.Warn("user: unban notification was not queued", "error", err)
	}
}

// derefString answers the string a pointer names, or the empty string.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
