package notification

import (
	"context"
	"log/slog"
	"time"
	"uuid"

	"github.com/riipandi/tango/internal/queue"
)

// NotificationEmailName is the queue the notification email pass runs on.
const NotificationEmailName = "notification_email"

// NotificationEmailPage is how many recipients one read answers. The pass
// walks its audience in keyset pages, so a global announcement delivers to
// every account without holding the whole deployment in memory.
const NotificationEmailPage = 500

// NotificationEmailTask carries one notification's email pass. The
// identifier is the whole payload: the row itself is the record of what
// the message says, so the task never carries a copy of it.
//
// The task lives beside the notification it names — the processor reads
// the audience through this package's repository — because the other
// direction would have the area and the job package importing each other.
type NotificationEmailTask struct {
	NotificationID uuid.UUID `json:"notification_id"`
}

// Config returns the queue the pass runs on. A pass that fails partway is
// retried whole — the recipients it already mailed hear the announcement
// twice, which is the cost of a pass that does not checkpoint a page into
// its payload; the email_sent_at stamp keeps a completed pass from
// repeating.
func (t NotificationEmailTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        NotificationEmailName,
		MaxAttempts: 3,
		Timeout:     5 * time.Minute,
		Backoff:     5 * time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// EmailDispatcher is the queue-side half of the seam the service defines:
// it turns "this notification asked for an email pass" into a durable
// task. The gate sits here, at the enqueue site — a deployment that
// switched the pass off is a no-op here rather than a dropped task, so the
// queue never carries mail nobody asked for.
type EmailDispatcher struct {
	client  *queue.Client
	log     *slog.Logger
	enabled bool
}

// NewEmailDispatcher builds the dispatcher over the queue client. The flag
// is the deployment's cost decision
// (mailer.notifications.announcement_email_enabled).
func NewEmailDispatcher(client *queue.Client, log *slog.Logger, enabled bool) *EmailDispatcher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &EmailDispatcher{client: client, log: log, enabled: enabled}
}

// EnqueueNotificationEmail queues one pass. The notification has already
// committed, so a failure to enqueue is logged rather than propagated —
// the announcement is live either way, and the log is the trace an
// operator follows when a deployment expected mail that never came.
func (d *EmailDispatcher) EnqueueNotificationEmail(ctx context.Context, notificationID uuid.UUID) {
	if !d.enabled || d.client == nil {
		return
	}
	if _, err := d.client.Add(NotificationEmailTask{NotificationID: notificationID}).Save(); err != nil {
		d.log.Warn("notification: email pass was not queued", "error", err)
	}
}
