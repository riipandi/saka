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

// EmailVerificationName is the queue the verification emails run on.
const EmailVerificationName = "email_verification"

// EmailVerificationTask renders the verification template and submits one
// message. The token is the raw value the email links to: the feature's
// procedure showed it to nobody and stored only its hash, so the payload is
// the only place it exists — which is why the queue's optional payload
// encryption is the disclosure boundary for it.
type EmailVerificationTask struct {
	// UserID is the account the message verifies, for tracing and for a
	// replay to answer against.
	UserID string `json:"user_id"`

	// Email is the address on record when the task was enqueued. A change
	// made after the enqueue re-issues through a new request, not by
	// rewriting this task.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// Token is the raw verification code the message carries. It is a
	// single-use code the account types back — there is no link to click.
	Token string `json:"token"`
}

// Config returns the queue the messages run on. The attempts are generous
// because an SMTP outage is the ordinary reason for a retry, and the timeout
// bounds one submission — the mailer's own attempt timeout bounds the
// connection inside it.
func (t EmailVerificationTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        EmailVerificationName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// emailVerificationProcessor renders the template and submits one message.
func emailVerificationProcessor(ctx context.Context, task EmailVerificationTask, mail *fwmailer.Service, baseURL string) error {
	if task.Email == "" || task.Token == "" {
		return errors.New("email_verification: task carries no address or code")
	}
	err := mail.Send(ctx, fwmailer.Request{
		To:       []string{task.Email},
		Subject:  "Verify your email address",
		Template: mailer.TemplateEmailVerification,
		View: fwmailer.View{
			Email: task.Email,
			Data: mailer.EmailVerificationData{
				UserFullName:     task.DisplayName,
				VerificationCode: task.Token,
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: email verification sent", "user_id", task.UserID)
	return nil
}
