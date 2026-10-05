package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/pkg/printext"
)

// OneTimeAccessEmailName is the queue the one-time access emails run on.
const OneTimeAccessEmailName = "one_time_access_email"

// OneTimeAccessEmailTask renders the one-time access template and submits one
// message. The code is the raw value the email carries — the feature's
// procedure showed it to nobody and stored only its hash, so the payload is
// the only place it exists, which is why the queue's optional payload
// encryption is the disclosure boundary for it.
//
// The task's retry schedule is tighter than the verification email's: the
// code it delivers expires, so an attempt schedule that outlives the code
// would keep trying to deliver a credential that can no longer be used.
type OneTimeAccessEmailTask struct {
	// UserID is the account the code signs in, for tracing and for a replay
	// to answer against.
	UserID string `json:"user_id"`

	// Email is the address on record when the task was enqueued.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// Token is the raw code the message carries.
	Token string `json:"token"`

	// TTLSeconds is the window the copy states, so the message and the code
	// agree on how long it works.
	TTLSeconds int64 `json:"ttl_seconds"`
}

// Config returns the queue the messages run on. Three attempts inside a
// minute and a half: an SMTP outage is retried while the code is still young,
// and a mailer that stays down is not retried past the point where the
// delivery could arrive before the code expires.
func (t OneTimeAccessEmailTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        OneTimeAccessEmailName,
		MaxAttempts: 3,
		Timeout:     30 * time.Second,
		Backoff:     15 * time.Second,
		Retention:   queue.DeadLetter(),
	}
}

// oneTimeAccessProcessor renders the template and submits one message.
func oneTimeAccessProcessor(ctx context.Context, task OneTimeAccessEmailTask, mail *fwmailer.Service, baseURL string) error {
	if task.Email == "" || task.Token == "" {
		return errors.New("one_time_access_email: task carries no address or code")
	}

	err := mail.Send(ctx, fwmailer.Request{
		To:       []string{task.Email},
		Subject:  "Your login code",
		Template: mailer.TemplateOneTimeAccess,
		View: fwmailer.View{
			Email: task.Email,
			Data: mailer.OneTimeAccessData{
				Code:             task.Token,
				ExpirationString: expirationString(time.Duration(task.TTLSeconds) * time.Second),
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: one-time access email sent", "user_id", task.UserID)
	return nil
}

// expirationString phrases a window the way the template's copy reads it: a
// whole number of the largest unit the window fills, because "0.25h" is not a
// sentence a holder reads.
func expirationString(ttl time.Duration) string {
	switch {
	case ttl >= 24*time.Hour:
		return fmt.Sprintf("%d %s", int(ttl.Hours()/24), printext.Plural(int(ttl.Hours()/24), "day"))
	case ttl >= time.Hour:
		hours := int(ttl.Hours())
		return fmt.Sprintf("%d %s", hours, printext.Plural(hours, "hour"))
	default:
		minutes := int(ttl.Minutes())
		if minutes < 1 {
			minutes = 1
		}
		return fmt.Sprintf("%d %s", minutes, printext.Plural(minutes, "minute"))
	}
}
