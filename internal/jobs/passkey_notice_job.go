package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/identity/webauthn"
)

// PasskeyAddedNoticeName is the queue an enrollment receipt runs on.
const PasskeyAddedNoticeName = "passkey_added_notice"

// PasskeyRemovedNoticeName is the queue a removal receipt runs on.
const PasskeyRemovedNoticeName = "passkey_removed_notice"

// PasskeyAddedNoticeTask renders the "a passkey was added" receipt and
// submits one message. The receipt carries no secret: the credential is
// bound to the account, never a way into it.
type PasskeyAddedNoticeTask struct {
	// UserID is the account whose roll grew.
	UserID string `json:"user_id"`

	// Email is the address the receipt goes to.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// CredentialName is the holder's own name for the credential.
	CredentialName string `json:"credential_name"`
}

// Config returns the queue the enrollment receipts run on.
func (t PasskeyAddedNoticeTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        PasskeyAddedNoticeName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// PasskeyRemovedNoticeTask renders the "a passkey was removed" receipt and
// submits one message. The holder's removal and the administrator's both
// ride it — the account learns its roll changed either way.
type PasskeyRemovedNoticeTask struct {
	// UserID is the account whose roll shrank.
	UserID string `json:"user_id"`

	// Email is the address the receipt goes to.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// CredentialName is the holder's own name for the credential.
	CredentialName string `json:"credential_name"`
}

// Config returns the queue the removal receipts run on.
func (t PasskeyRemovedNoticeTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        PasskeyRemovedNoticeName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// passkeyAddedNoticeProcessor renders the template and submits one message.
func passkeyAddedNoticeProcessor(ctx context.Context, task PasskeyAddedNoticeTask, mail *mailer.Service) error {
	if task.Email == "" {
		return errors.New("passkey_added_notice: task carries no address")
	}
	err := mail.Send(ctx, mailer.Request{
		To:       []string{task.Email},
		Subject:  "A passkey was added to your account",
		Template: mailer.TemplatePasskeyAddedNotice,
		View: mailer.View{
			Email: task.Email,
			Data: mailer.PasskeyNoticeData{
				Name:           task.DisplayName,
				Email:          task.Email,
				CredentialName: task.CredentialName,
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: passkey added notice sent", "user_id", task.UserID)
	return nil
}

// passkeyRemovedNoticeProcessor renders the template and submits one
// message.
func passkeyRemovedNoticeProcessor(ctx context.Context, task PasskeyRemovedNoticeTask, mail *mailer.Service) error {
	if task.Email == "" {
		return errors.New("passkey_removed_notice: task carries no address")
	}
	err := mail.Send(ctx, mailer.Request{
		To:       []string{task.Email},
		Subject:  "A passkey was removed from your account",
		Template: mailer.TemplatePasskeyRemovedNotice,
		View: mailer.View{
			Email: task.Email,
			Data: mailer.PasskeyNoticeData{
				Name:           task.DisplayName,
				Email:          task.Email,
				CredentialName: task.CredentialName,
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: passkey removed notice sent", "user_id", task.UserID)
	return nil
}

// PasskeyNoticeNotifier is the adapter between the webauthn service's
// PasskeyNotifier seam and the durable task queue. The service states what
// happened; this type decides how the message travels. The enqueue is
// best-effort — the ceremony has committed, and a lost notice must not
// fail it.
type PasskeyNoticeNotifier struct {
	client *queue.Client
	log    *slog.Logger

	// addedEnabled and removedEnabled are the deployment's cost decisions
	// for the two receipts (mailer.notifications). A switched-off notice
	// is a no-op here rather than a dropped task, so the queue never
	// carries mail nobody asked for.
	addedEnabled   bool
	removedEnabled bool
}

// NewPasskeyNoticeNotifier builds the adapter over the queue client. A nil
// logger is answered with the discard handler.
func NewPasskeyNoticeNotifier(client *queue.Client, log *slog.Logger, addedEnabled, removedEnabled bool) *PasskeyNoticeNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &PasskeyNoticeNotifier{client: client, log: log, addedEnabled: addedEnabled, removedEnabled: removedEnabled}
}

// EnqueuePasskeyAddedNotice queues the enrollment receipt.
func (n *PasskeyNoticeNotifier) EnqueuePasskeyAddedNotice(ctx context.Context, notice webauthn.PasskeyNotice) error {
	if !n.addedEnabled {
		return nil
	}
	if _, err := n.client.Add(PasskeyAddedNoticeTask{
		UserID:         notice.UserID,
		Email:          notice.Email,
		DisplayName:    notice.DisplayName,
		CredentialName: notice.CredentialName,
	}).Save(); err != nil {
		n.log.Warn("webauthn: passkey added notice was not queued", "error", err, "user_id", notice.UserID)
		return nil
	}
	return nil
}

// EnqueuePasskeyRemovedNotice queues the removal receipt.
func (n *PasskeyNoticeNotifier) EnqueuePasskeyRemovedNotice(ctx context.Context, notice webauthn.PasskeyNotice) error {
	if !n.removedEnabled {
		return nil
	}
	if _, err := n.client.Add(PasskeyRemovedNoticeTask{
		UserID:         notice.UserID,
		Email:          notice.Email,
		DisplayName:    notice.DisplayName,
		CredentialName: notice.CredentialName,
	}).Save(); err != nil {
		n.log.Warn("webauthn: passkey removed notice was not queued", "error", err, "user_id", notice.UserID)
		return nil
	}
	return nil
}
