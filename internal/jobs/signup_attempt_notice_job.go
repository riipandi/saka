package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/queue"
)

// SignupAttemptNoticeName is the queue the strict mode's sign-up notices run on.
const SignupAttemptNoticeName = "signup_attempt_existing_email"

// SignupAttemptNoticeTask renders the "someone tried to sign up with this
// address" notice and submits one message. It carries no code and grants
// nothing: the sign-up it answers created nothing, so the message is a
// notice, never a credential.
type SignupAttemptNoticeTask struct {
	// Email is the address on file — the message's recipient and subject.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`
}

// Config returns the queue the notices run on.
func (t SignupAttemptNoticeTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        SignupAttemptNoticeName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// signupAttemptNoticeProcessor renders the template and submits one message.
func signupAttemptNoticeProcessor(ctx context.Context, task SignupAttemptNoticeTask, mail *mailer.Service) error {
	if task.Email == "" {
		return errors.New("signup_attempt_existing_email: task carries no address")
	}
	err := mail.Send(ctx, mailer.Request{
		To:       []string{task.Email},
		Subject:  "Sign-up attempt for your email",
		Template: mailer.TemplateSignupAttemptNotice,
		View: mailer.View{
			Email: task.Email,
			Data: mailer.SignupAttemptNoticeData{
				Name:  task.DisplayName,
				Email: task.Email,
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: signup attempt notice sent", "email", task.Email)
	return nil
}
