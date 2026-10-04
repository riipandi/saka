package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/internal/mailer"
)

// UserLockedNoticeName is the queue the lockout notices run on.
const UserLockedNoticeName = "user_locked_notice"

// UserLockedNoticeTask renders the lockout notice and submits one message.
// It carries no code and grants nothing: the lockout landed when this is
// queued, so the message is a notice, never a credential.
type UserLockedNoticeTask struct {
	// UserID is the account the policy locked.
	UserID string `json:"user_id"`

	// Email is the address the notice goes to.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// ExpiresAt is the window the lockout lifts at, empty for a lockout
	// that never lifts by itself.
	ExpiresAt string `json:"expires_at"`
}

// Config returns the queue the notices run on.
func (t UserLockedNoticeTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        UserLockedNoticeName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// userLockedNoticeProcessor renders the template and submits one message.
func userLockedNoticeProcessor(ctx context.Context, task UserLockedNoticeTask, mail *fwmailer.Service) error {
	if task.Email == "" {
		return errors.New("user_locked_notice: task carries no address")
	}
	err := mail.Send(ctx, fwmailer.Request{
		To:       []string{task.Email},
		Subject:  "Your account was locked",
		Template: mailer.TemplateUserLocked,
		View: fwmailer.View{
			Email: task.Email,
			Data: mailer.UserLockedData{
				Name:      task.DisplayName,
				Email:     task.Email,
				ExpiresAt: task.ExpiresAt,
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: user locked notice sent", "user_id", task.UserID)
	return nil
}
