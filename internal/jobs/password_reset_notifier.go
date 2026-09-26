package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/identity/password"
)

// PasswordResetNotifier is the adapter between the password service's
// resetEnqueuer interface and the durable task queue. The password service
// states what happened; this type decides how the message travels. Unlike
// the ban notices, the enqueue failure is propagated: the token row and the
// message belong to one trigger, and a trigger whose message was never
// queued must not read as issued.
type PasswordResetNotifier struct {
	client *queue.Client
	log    *slog.Logger
}

// NewPasswordResetNotifier builds the adapter over the queue client. A nil
// logger is answered with the discard handler.
func NewPasswordResetNotifier(client *queue.Client, log *slog.Logger) *PasswordResetNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &PasswordResetNotifier{client: client, log: log}
}

// EnqueuePasswordResetEmail queues the message the trigger produced.
func (n *PasswordResetNotifier) EnqueuePasswordResetEmail(ctx context.Context, email password.ResetEmail) error {
	if _, err := n.client.Add(PasswordResetEmailTask{
		UserID: email.UserID,
		Email:  email.Email,
		Token:  email.Token,
	}).Save(); err != nil {
		return fmt.Errorf("password reset notifier: enqueue: %w", err)
	}
	n.log.DebugContext(ctx, "queue: password reset email enqueued", "user_id", email.UserID)
	return nil
}

// EnqueuePasswordChangedNotice queues the receipt a completed reset sends.
// Unlike the trigger's message, a failure here is best-effort: the reset has
// committed, and the audit record already says so — a lost notice must not
// fail the procedure that succeeded.
func (n *PasswordResetNotifier) EnqueuePasswordChangedNotice(ctx context.Context, notice password.ChangedNotice) error {
	if _, err := n.client.Add(PasswordChangedNoticeTask{
		UserID:      notice.UserID,
		Email:       notice.Email,
		DisplayName: notice.DisplayName,
	}).Save(); err != nil {
		n.log.Warn("password: change notice was not queued", "error", err, "user_id", notice.UserID)
		return nil
	}
	return nil
}
