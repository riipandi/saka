package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/modules/identity/webauthn"
)

// ReauthenticationCodeEmailName is the queue the reauthentication-code
// emails run on.
const ReauthenticationCodeEmailName = "reauthentication_code_email"

// ReauthenticationCodeEmailTask renders the reauthentication-code template
// and submits one message. The code is the raw value the email carries — the
// feature stored only its hash, so the payload is the only place it exists,
// which is why the queue's optional payload encryption is the disclosure
// boundary for it.
//
// The retry schedule matches the one-time access email's: the code it
// delivers expires, so an attempt schedule that outlives the code would keep
// trying to deliver a credential that can no longer be used.
type ReauthenticationCodeEmailTask struct {
	// UserID is the account the proof is for, for tracing and for a replay
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

// Config returns the queue the messages run on.
func (t ReauthenticationCodeEmailTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        ReauthenticationCodeEmailName,
		MaxAttempts: 3,
		Timeout:     30 * time.Second,
		Backoff:     15 * time.Second,
		Retention:   queue.DeadLetter(),
	}
}

// reauthenticationCodeProcessor renders the template and submits one message.
func reauthenticationCodeProcessor(ctx context.Context, task ReauthenticationCodeEmailTask, mail *mailer.Service) error {
	if task.Email == "" || task.Token == "" {
		return errors.New("reauthentication_code_email: task carries no address or code")
	}

	err := mail.Send(ctx, mailer.Request{
		To:       []string{task.Email},
		Subject:  "Confirm it is you",
		Template: mailer.TemplateReauthenticationCode,
		View: mailer.View{
			Email: task.Email,
			Data: mailer.ReauthenticationCodeData{
				Code:             task.Token,
				ExpirationString: expirationString(time.Duration(task.TTLSeconds) * time.Second),
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: reauthentication code email sent", "user_id", task.UserID)
	return nil
}

// ReauthenticationCodeNotifier is the webauthn feature's enqueue seam: the
// feature defines the interface, this adapter holds the durable queue.
type ReauthenticationCodeNotifier struct {
	client *queue.Client
	log    *slog.Logger
}

// NewReauthenticationCodeNotifier builds the adapter over the shared queue.
// A nil logger is answered with the discard handler.
func NewReauthenticationCodeNotifier(client *queue.Client, log *slog.Logger) *ReauthenticationCodeNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ReauthenticationCodeNotifier{client: client, log: log}
}

// EnqueueReauthenticationCodeEmail queues the message the code request
// produced.
func (n *ReauthenticationCodeNotifier) EnqueueReauthenticationCodeEmail(ctx context.Context, mail webauthn.CodeEmail) error {
	if _, err := n.client.Add(ReauthenticationCodeEmailTask{
		UserID:      mail.UserID,
		Email:       mail.Email,
		DisplayName: mail.DisplayName,
		Token:       mail.Token,
		TTLSeconds:  mail.TTLSeconds,
	}).Save(); err != nil {
		return fmt.Errorf("reauthentication code notifier: enqueue: %w", err)
	}
	n.log.DebugContext(ctx, "queue: reauthentication code email enqueued", "user_id", mail.UserID)
	return nil
}
