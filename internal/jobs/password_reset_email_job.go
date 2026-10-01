package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
)

// PasswordResetEmailName is the queue the reset emails run on.
const PasswordResetEmailName = "password_reset_email"

// PasswordResetEmailTask renders the reset template and submits one message.
// The token is the raw value the email links to: the issuing procedure stored
// only its hash, so the payload is the only place it exists — which is why
// the queue's optional payload encryption is the disclosure boundary for it.
type PasswordResetEmailTask struct {
	// UserID is the account the reset belongs to, for tracing and for a
	// replay to answer against.
	UserID string `json:"user_id"`

	// Email is the address on record when the task was enqueued.
	Email string `json:"email"`

	// Token is the raw reset token the link carries.
	Token string `json:"token"`
}

// Config returns the queue the messages run on. The attempts are generous
// because an SMTP outage is the ordinary reason for a retry.
func (t PasswordResetEmailTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        PasswordResetEmailName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
	}
}

// passwordResetProcessor renders the template and submits one message.
func passwordResetProcessor(ctx context.Context, task PasswordResetEmailTask, mail *mailer.Service, baseURL string) error {
	if task.Email == "" || task.Token == "" {
		return errors.New("password_reset_email: task carries no address or code")
	}
	err := mail.Send(ctx, mailer.Request{
		To:       []string{task.Email},
		Subject:  "Reset your password",
		Template: mailer.TemplatePasswordReset,
		View: mailer.View{
			Email: task.Email,
			Data: mailer.PasswordResetData{
				Email:     task.Email,
				ResetCode: task.Token,
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: password reset email sent", "user_id", task.UserID)
	return nil
}

// PasswordChangedNoticeName is the queue the change receipts run on.
const PasswordChangedNoticeName = "password_changed_notice"

// PasswordChangedNoticeTask renders the "your password was changed"
// receipt and submits one message. It carries no token and no secret: the
// reset has already completed when this is queued, so the message is a
// notice, never a credential.
type PasswordChangedNoticeTask struct {
	// UserID is the account whose credential was replaced.
	UserID string `json:"user_id"`

	// Email is the address the receipt goes to.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`
}

// Config returns the queue the receipts run on.
func (t PasswordChangedNoticeTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        PasswordChangedNoticeName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
	}
}

// passwordChangedNoticeProcessor renders the template and submits one
// message.
func passwordChangedNoticeProcessor(ctx context.Context, task PasswordChangedNoticeTask, mail *mailer.Service) error {
	if task.Email == "" {
		return errors.New("password_changed_notice: task carries no address")
	}
	err := mail.Send(ctx, mailer.Request{
		To:       []string{task.Email},
		Subject:  "Your password was changed",
		Template: mailer.TemplatePasswordChangedNotice,
		View: mailer.View{
			Email: task.Email,
			Data: mailer.PasswordChangedNoticeData{
				Name:  task.DisplayName,
				Email: task.Email,
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: password changed notice sent", "user_id", task.UserID)
	return nil
}
