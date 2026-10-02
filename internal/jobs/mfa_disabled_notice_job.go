package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
)

// MfaDisabledNoticeName is the queue the MFA-removal notices run on.
const MfaDisabledNoticeName = "mfa_disabled_notice"

// MfaDisabledNoticeTask renders the removal template and submits one
// message. The reason is the account holder's copy of the operator's note —
// it travels here because the audit record keeps it too, but the message is
// the only copy the account reads.
type MfaDisabledNoticeTask struct {
	// Email is the address on record when the second factor was removed.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// Reason is why the second factor was removed, the same sentence the
	// audit record keeps. Empty when the operator wrote none.
	Reason string `json:"reason"`
}

// Config returns the queue the removal notices run on: the same generous
// attempts the other notifications keep, because an SMTP outage is the
// ordinary reason for a retry and a security notice must not be lost to one.
func (t MfaDisabledNoticeTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        MfaDisabledNoticeName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// mfaDisabledNoticeProcessor renders the template and submits one message.
func mfaDisabledNoticeProcessor(ctx context.Context, task MfaDisabledNoticeTask, mail *mailer.Service) error {
	if task.Email == "" {
		return errors.New("mfa_disabled_notice: task carries no address")
	}
	return mail.Send(ctx, mailer.Request{
		To:       []string{task.Email},
		Subject:  "Two-factor authentication was removed from your account",
		Template: mailer.TemplateMfaDisabledNotice,
		View: mailer.View{
			Email: task.Email,
			Data: mailer.MfaDisabledNoticeData{
				Name:   task.DisplayName,
				Reason: task.Reason,
			},
		},
	})
}
