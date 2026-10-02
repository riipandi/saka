package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
)

// OAuthSignInCodeName is the queue the sign-in-through-a-provider code
// emails run on.
const OAuthSignInCodeName = "oauth_sign_in_code"

// OAuthSignInCodeTask renders the oauth-sign-in-code template and submits
// one message. The code is the raw value the account types back: the flow
// stored only its hash, so the payload is the only place it exists — the
// queue's optional payload encryption is the disclosure boundary for it.
type OAuthSignInCodeTask struct {
	// Email is the address the flow's resolution named. The code proves
	// the caller holds it, so the message goes nowhere else.
	Email string `json:"email"`

	// ProviderName is the connection's display name, the wording the
	// message names the sign-in with.
	ProviderName string `json:"provider_name"`

	// Code is the raw single-use code. Six or twelve characters.
	Code string `json:"code"`

	// TTLSeconds is the code's window, for the countdown the message
	// renders. The flow's own expiry bounds it too.
	TTLSeconds int `json:"ttl_seconds"`
}

// Config returns the queue the messages run on. The attempts are generous
// because an SMTP outage is the ordinary reason for a retry, and the
// timeout bounds one submission.
func (t OAuthSignInCodeTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        OAuthSignInCodeName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// OAuthSignInCodeNotifier is the oauthsso feature's delivery seam: the
// feature defines the interface, this adapter holds the durable queue.
type OAuthSignInCodeNotifier struct {
	client *queue.Client
	log    *slog.Logger
}

// NewOAuthSignInCodeNotifier builds the queue-backed notifier.
func NewOAuthSignInCodeNotifier(client *queue.Client, log *slog.Logger) OAuthSignInCodeNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return OAuthSignInCodeNotifier{client: client, log: log}
}

// DeliverSignInCode enqueues the code message. A failed enqueue fails the
// continue — the flow sits in the verify_email stage, and the send is
// retried by the caller's next continue.
func (n OAuthSignInCodeNotifier) DeliverSignInCode(ctx context.Context, email, providerName, code string, ttl time.Duration) error {
	if _, err := n.client.Add(OAuthSignInCodeTask{
		Email:        email,
		ProviderName: providerName,
		Code:         code,
		TTLSeconds:   int(ttl.Seconds()),
	}).Save(); err != nil {
		n.log.ErrorContext(ctx, "queue: oauth sign-in code enqueue failed", "error", err)
		return fmt.Errorf("queue: oauth sign-in code enqueue: %w", err)
	}
	return nil
}

// oauthSignInCodeProcessor renders the template and submits one message.
func oauthSignInCodeProcessor(ctx context.Context, task OAuthSignInCodeTask, mail *mailer.Service) error {
	if task.Email == "" || task.Code == "" {
		return errors.New("oauth_sign_in_code: task carries no address or code")
	}
	err := mail.Send(ctx, mailer.Request{
		To:       []string{task.Email},
		Subject:  "Finish signing in",
		Template: mailer.TemplateOAuthSignInCode,
		View: mailer.View{
			Email: task.Email,
			Data: mailer.OAuthSignInCodeData{
				ProviderName:     task.ProviderName,
				Code:             task.Code,
				ExpirationString: expirationString(time.Duration(task.TTLSeconds) * time.Second),
			},
		},
	})
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: oauth sign-in code sent", "email", task.Email)
	return nil
}
