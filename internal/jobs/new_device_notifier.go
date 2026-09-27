package jobs

import (
	"context"
	"log/slog"

	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/identity/signin"
)

// DeviceNotifier is the adapter between the sign-in service's
// deviceNoticeEnqueuer interface and the durable task queue. The sign-in
// service states what happened — a fingerprint seen for the first time —;
// this type decides how the message travels. The enqueue is best-effort: the
// sign-in has committed, and the audit record already says so.
type DeviceNotifier struct {
	client *queue.Client
	log    *slog.Logger

	// enabled is the deployment's cost decision (mailer.notifications).
	// A switched-off flow is a no-op here rather than a dropped task, so
	// the queue never carries mail nobody asked for.
	enabled bool
}

// NewDeviceNotifier builds the adapter over the queue client. enabled is the
// notice flag; a nil logger is answered with the discard handler.
func NewDeviceNotifier(client *queue.Client, log *slog.Logger, enabled bool) *DeviceNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &DeviceNotifier{client: client, log: log, enabled: enabled}
}

// EnqueueNewDeviceNotice queues the first-seen notice.
func (n *DeviceNotifier) EnqueueNewDeviceNotice(ctx context.Context, notice signin.NewDeviceNotice) {
	if !n.enabled {
		return
	}
	if _, err := n.client.Add(NewDeviceEmailTask{
		Email:      notice.Email,
		IPAddress:  notice.IPAddress,
		UserAgent:  notice.UserAgent,
		SignedInAt: notice.SignedInAt,
	}).Save(); err != nil {
		n.log.Warn("signin: new device notice was not queued", "error", err, "user_id", notice.UserID)
	}
}
