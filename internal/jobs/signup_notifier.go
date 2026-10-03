package jobs

import (
	"context"
	"log/slog"

	"github.com/riipandi/tango/internal/queue"
)

// SignupNotifier is the adapter between the sign-up service's
// EnumerationNotifier interface and the durable task queue. The sign-up
// service states what happened — a sign-up answered by the strict mode —;
// this type decides how the message travels. The enqueue is best-effort:
// the sign-up has answered, and nothing was written to roll back.
type SignupNotifier struct {
	client *queue.Client
	log    *slog.Logger

	// enabled is the deployment's cost decision (mailer.notifications).
	// A switched-off flow is a no-op here rather than a dropped task, so
	// the queue never carries mail nobody asked for.
	enabled bool
}

// NewSignupNotifier builds the adapter over the queue client. enabled is
// the notice flag; a nil logger is answered with the discard handler.
func NewSignupNotifier(client *queue.Client, log *slog.Logger, enabled bool) *SignupNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &SignupNotifier{client: client, log: log, enabled: enabled}
}

// EnqueueSignupAttemptExistingEmail queues the notice the address on file
// receives. A nil queue — the state a test or a bare wiring is in — skips
// the mail the same way a switched-off flag does.
func (n *SignupNotifier) EnqueueSignupAttemptExistingEmail(ctx context.Context, email, displayName string) {
	if !n.enabled || n.client == nil {
		return
	}
	if _, err := n.client.Add(SignupAttemptNoticeTask{
		Email:       email,
		DisplayName: displayName,
	}).Save(); err != nil {
		n.log.Warn("signup: existing-email notice was not queued", "error", err)
	}
}
