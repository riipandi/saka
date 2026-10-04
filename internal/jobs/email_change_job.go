package jobs

import (
	"context"
	"errors"
	"time"

	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/queue"
)

// EmailChangeRequestEmailName is the queue the confirm-link messages run on.
const EmailChangeRequestEmailName = "email_change_request"

// EmailChangeNoticeName is the queue the pending-change and completion
// notices run on.
const EmailChangeNoticeName = "email_change_notice"

// EmailChangeRequestEmailTask carries the addresses and the code; the
// processors below render the request and the notices.
type EmailChangeRequestEmailTask struct {
	// Email is the address the message goes to — the new address, the one
	// proving control of which is the point of the code.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// OldEmail and NewEmail are the two addresses the template names
	// together, so the reader can tell which inbox the change leaves.
	OldEmail string `json:"old_email"`
	NewEmail string `json:"new_email"`

	// Token is the raw change code, in clear text: the message is the only
	// copy, the database keeps the hash. It is a single-use code the account
	// types back — there is no link to click.
	Token string `json:"token"`
}

// Config returns the queue the confirm-code messages run on. The attempts
// stay generous for the same reason the other transactional emails keep
// them: an SMTP outage is the ordinary retry, and a change whose code never
// arrives is a change that never completes.
func (t EmailChangeRequestEmailTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        EmailChangeRequestEmailName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// emailChangeRequestProcessor renders the template and submits one message.
func emailChangeRequestProcessor(ctx context.Context, task EmailChangeRequestEmailTask, mail *fwmailer.Service, baseURL string) error {
	if task.Email == "" || task.Token == "" {
		return errors.New("email_change_request: task carries no address or code")
	}
	return mail.Send(ctx, fwmailer.Request{
		To:       []string{task.Email},
		Subject:  "Confirm your new email address",
		Template: mailer.TemplateEmailChangeRequest,
		View: fwmailer.View{
			Email: task.Email,
			Data: mailer.EmailChangeRequestData{
				Name:        task.DisplayName,
				OldEmail:    task.OldEmail,
				NewEmail:    task.NewEmail,
				ConfirmCode: task.Token,
			},
		},
	})
}

// EmailChangeNoticeKind names which notice the template renders. The two
// share the copy's shape — the addresses, the account, no link — so one task
// type carries both and the processor picks the rendering.
type EmailChangeNoticeKind string

const (
	// EmailChangeNoticePending is the notice the old address receives while
	// a change is pending against it.
	EmailChangeNoticePending EmailChangeNoticeKind = "pending"

	// EmailChangeNoticeSuccess is the confirmation the new address receives
	// once the change has completed.
	EmailChangeNoticeSuccess EmailChangeNoticeKind = "success"
)

// EmailChangeNotice is what a pending request and a completed confirmation
// send to the address they concern. To is the address the notice goes to —
// the old one while the change is pending, the new one once it completed.
// The type lives beside the task it becomes, and the verification service's
// enqueuer interface names it, so the two packages share it without the
// adapter importing the feature back.
type EmailChangeNotice struct {
	UserID      string
	To          string
	DisplayName string
	OldEmail    string
	NewEmail    string
}

// EmailChangeNoticeTask renders one of the two notice templates. Both are
// gated by the deployment's cost decision, so a task on this queue was
// asked for.
type EmailChangeNoticeTask struct {
	// Email is the address the notice goes to: the old address while the
	// change is pending, the new address once it completed.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// OldEmail and NewEmail are the two addresses the notice names.
	OldEmail string `json:"old_email"`
	NewEmail string `json:"new_email"`

	// Kind picks the rendering.
	Kind EmailChangeNoticeKind `json:"kind"`
}

// Config returns the queue the notices run on.
func (t EmailChangeNoticeTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        EmailChangeNoticeName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// emailChangeNoticeProcessor renders the kind's template and submits one
// message.
func emailChangeNoticeProcessor(ctx context.Context, task EmailChangeNoticeTask, mail *fwmailer.Service) error {
	if task.Email == "" {
		return errors.New("email_change_notice: task carries no address")
	}
	data := mailer.EmailChangeNoticeData{
		Name:     task.DisplayName,
		OldEmail: task.OldEmail,
		NewEmail: task.NewEmail,
	}
	view := fwmailer.View{Email: task.Email, Data: data}
	subject := ""
	template := ""
	switch task.Kind {
	case EmailChangeNoticePending:
		subject = "A change of your email address was requested"
		template = mailer.TemplateEmailChangeNotice
	case EmailChangeNoticeSuccess:
		subject = "Your email address was changed"
		template = mailer.TemplateEmailChangeSuccess
	default:
		return errors.New("email_change_notice: task carries an unknown kind")
	}
	return mail.Send(ctx, fwmailer.Request{To: []string{task.Email}, Subject: subject, Template: template, View: view})
}
