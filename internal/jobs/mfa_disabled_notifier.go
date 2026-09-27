package jobs

import (
	"context"
	"log/slog"

	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/identity/multifactor"
)

// MfaDisabledNotifier is the adapter between the multifactor service's
// noticeEnqueuer interface and the durable task queue. The multifactor
// service states what happened; this type decides how the message travels.
// Like the ban notices, the enqueue failure is best-effort: the removal has
// committed, and the audit record already says so.
type MfaDisabledNotifier struct {
	client *queue.Client
	log    *slog.Logger

	// enabled is the deployment's cost decision (mailer.notifications). A
	// switched-off flow is a no-op here rather than a dropped task, so the
	// queue never carries mail nobody asked for.
	enabled bool
}

// NewMfaDisabledNotifier builds the adapter over the queue client. enabled is
// the notice flag; a nil logger is answered with the discard handler.
func NewMfaDisabledNotifier(client *queue.Client, log *slog.Logger, enabled bool) *MfaDisabledNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &MfaDisabledNotifier{client: client, log: log, enabled: enabled}
}

// EnqueueMfaDisabledNotice queues the removal notice.
func (n *MfaDisabledNotifier) EnqueueMfaDisabledNotice(ctx context.Context, notice multifactor.MfaDisabledNotice) {
	if !n.enabled {
		return
	}
	if _, err := n.client.Add(MfaDisabledNoticeTask{
		Email:       notice.Email,
		DisplayName: notice.DisplayName,
		Reason:      notice.Reason,
	}).Save(); err != nil {
		n.log.Warn("multifactor: removal notice was not queued", "error", err, "user_id", notice.UserID)
	}
}
