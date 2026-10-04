package jobs

import (
	"context"
	"errors"
	"time"

	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/queue"
)

// UserBannedEmailName is the queue the ban notifications run on.
const UserBannedEmailName = "user_banned_email"

// UserUnbannedEmailName is the queue the lift notifications run on.
const UserUnbannedEmailName = "user_unbanned_email"

// UserBannedEmailTask renders the ban template and submits one message. The
// reason is the account holder's own copy of the term — it travels here
// because the ban's row and the audit record keep it too, but the message is
// the only copy the account reads.
type UserBannedEmailTask struct {
	// Email is the address on record when the ban was applied.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// Reason is why the account was banned, the same sentence the audit
	// record keeps.
	Reason string `json:"reason"`

	// ExpiresAt is when the ban lifts, RFC 3339 — empty for a ban that
	// never lifts by itself, which the template renders as its own case.
	ExpiresAt string `json:"expires_at"`
}

// Config returns the queue the ban messages run on: the same generous
// attempts the other notifications keep, because an SMTP outage is the
// ordinary reason for a retry and a suspension notice must not be lost to
// one.
func (t UserBannedEmailTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        UserBannedEmailName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// userBannedProcessor renders the template and submits one message.
func userBannedProcessor(ctx context.Context, task UserBannedEmailTask, mail *fwmailer.Service) error {
	if task.Email == "" || task.Reason == "" {
		return errors.New("user_banned_email: task carries no address or reason")
	}
	return mail.Send(ctx, fwmailer.Request{
		To:       []string{task.Email},
		Subject:  "Your account has been suspended",
		Template: mailer.TemplateUserBanned,
		View: fwmailer.View{
			Email: task.Email,
			Data: mailer.UserBannedData{
				Name:      task.DisplayName,
				Reason:    task.Reason,
				ExpiresAt: task.ExpiresAt,
			},
		},
	})
}

// UserUnbannedEmailTask renders the reinstatement template and submits one
// message. It carries the account's name and nothing else: the lift has no
// terms to state.
type UserUnbannedEmailTask struct {
	// Email is the address on record when the ban was lifted.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`
}

// Config returns the queue the lift messages run on — the same schedule the
// ban's notice keeps, so both halves of a suspension reach the account the
// same way.
func (t UserUnbannedEmailTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        UserUnbannedEmailName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// userUnbannedProcessor renders the template and submits one message.
func userUnbannedProcessor(ctx context.Context, task UserUnbannedEmailTask, mail *fwmailer.Service) error {
	if task.Email == "" {
		return errors.New("user_unbanned_email: task carries no address")
	}
	return mail.Send(ctx, fwmailer.Request{
		To:       []string{task.Email},
		Subject:  "Your account has been reinstated",
		Template: mailer.TemplateUserUnbanned,
		View: fwmailer.View{
			Email: task.Email,
			Data: mailer.UserUnbannedData{
				Name: task.DisplayName,
			},
		},
	})
}
