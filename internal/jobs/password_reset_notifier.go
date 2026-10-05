package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/modules/identity/password"
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

	// changedNoticeEnabled is the deployment's cost decision for the change
	// receipt (mailer.notifications). The reset link itself is transactional
	// and never gated. A switched-off notice is a no-op here rather than a
	// dropped task, so the queue never carries mail nobody asked for.
	changedNoticeEnabled bool

	// removedNoticeEnabled gates the removal receipt the same way.
	removedNoticeEnabled bool
}

// NewPasswordResetNotifier builds the adapter over the queue client.
// changedNoticeEnabled gates the change receipt, removedNoticeEnabled the
// removal receipt; a nil logger is answered with the discard handler.
func NewPasswordResetNotifier(client *queue.Client, log *slog.Logger, changedNoticeEnabled, removedNoticeEnabled bool) *PasswordResetNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &PasswordResetNotifier{client: client, log: log, changedNoticeEnabled: changedNoticeEnabled, removedNoticeEnabled: removedNoticeEnabled}
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
	if !n.changedNoticeEnabled {
		return nil
	}
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

// EnqueuePasswordRemovedNotice queues the receipt a completed removal
// sends. Best-effort like the change receipt: the removal has committed,
// and the audit record already says so.
func (n *PasswordResetNotifier) EnqueuePasswordRemovedNotice(ctx context.Context, notice password.ChangedNotice) error {
	if !n.removedNoticeEnabled {
		return nil
	}
	if _, err := n.client.Add(PasswordRemovedNoticeTask{
		UserID:      notice.UserID,
		Email:       notice.Email,
		DisplayName: notice.DisplayName,
	}).Save(); err != nil {
		n.log.Warn("password: removal notice was not queued", "error", err, "user_id", notice.UserID)
		return nil
	}
	return nil
}
