package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/saka/framework/datastore"
	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/modules/apikey"
)

// APIKeyExpiryScanName is the queue the expiry scan runs on. It is the one
// recurring task the keys carry: one scan answers the whole window, and each
// reminder it enqueues travels on its own queue so a slow mailer cannot hold
// the schedule.
const APIKeyExpiryScanName = "api_key_expiry_scan"

// APIKeyExpiryEmailName is the queue one reminder travels on.
const APIKeyExpiryEmailName = "api_key_expiry_email"

// DefaultAPIKeyExpiryInterval is the schedule the scan re-enqueues itself
// with: once a day, the cadence a reminder that names a seven-day window
// needs, and loose enough that a missed run costs a day, not the reminder.
const DefaultAPIKeyExpiryInterval = 24 * time.Hour

// APIKeyExpiryWindow is how far ahead the scan looks: the upstream it ports
// reminds the holder seven days before their key expires.
const APIKeyExpiryWindow = 7 * 24 * time.Hour

// APIKeyExpiryScanTask is one pass over the reminder window. It carries
// nothing: the window is a constant, and the switch it honors is captured
// when the processor is wired, so a change of mind takes effect at the next
// restart rather than leaving half the processes reminding.
type APIKeyExpiryScanTask struct{}

// Config returns the queue the scan runs on. Three attempts with room
// between: a scan that enqueues reminders can afford to lose a whole attempt
// and still remind before the keys expire.
func (t APIKeyExpiryScanTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        APIKeyExpiryScanName,
		MaxAttempts: 3,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
	}
}

// APIKeyExpiryEmailTask renders the expiry template and submits one message.
// The reminder names a key that is about to stop working; it carries no
// credential, so the payload needs none of the queue's encryption.
type APIKeyExpiryEmailTask struct {
	// KeyID is the key row the reminder is about, for tracing and for the
	// mark the scan reads back.
	KeyID string `json:"key_id"`

	// UserID is the account the key belongs to.
	UserID string `json:"user_id"`

	// Email is the address on record when the scan ran.
	Email string `json:"email"`

	// DisplayName is the name the template greets.
	DisplayName string `json:"display_name"`

	// APIKeyName is the label the reminder names.
	APIKeyName string `json:"api_key_name"`

	// ExpiresAt is the instant the key stops working, RFC 3339 — the whole
	// point of the message.
	ExpiresAt string `json:"expires_at"`
}

// Config returns the queue the reminders run on. The same schedule the
// verification email keeps: a reminder is ordinary mail, and an attempt
// schedule that outlives the day would only delay it.
func (t APIKeyExpiryEmailTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        APIKeyExpiryEmailName,
		MaxAttempts: 3,
		Timeout:     30 * time.Second,
		Backoff:     15 * time.Second,
		Retention:   queue.DeadLetter(),
	}
}

// apiKeyExpiryScanProcessor answers one pass over the reminder window: the
// keys that expire within it and have not been reminded, one reminder each,
// and the mark that keeps the next pass from repeating them.
//
// The successor is queued only once the pass has succeeded. Queuing it first
// meant a failed scan still committed its successor, and the queue's retry of
// the same attempt inserted another: three attempts could leave three daily
// chains, and two chains could both remind a key before either marked it. A
// failed scan now leaves no successor — the retry is the queue's — and a
// crash between the scan and the enqueue costs one day rather than leaving a
// second permanent schedule. Unmarked keys are still picked up by the next
// pass. This is the order the cleanup, retention, and garbage-collection jobs
// already keep.
//
// A disabled switch is not an error: the pass finds nothing asked of it and
// re-enqueues its successor, so turning the reminder off and on again leaves
// the schedule alive.
func apiKeyExpiryScanProcessor(ctx context.Context, task APIKeyExpiryScanTask, pool *datastore.Postgres, client *queue.Client, enabled bool, mail *fwmailer.Service) error {
	if enabled && mail != nil {
		if err := apiKeyExpiryScan(ctx, pool, client); err != nil {
			return err
		}
	}

	_, err := client.Add(task).Wait(DefaultAPIKeyExpiryInterval).Ctx(ctx).Save()
	return err
}

// apiKeyExpiryScan is the pass itself: list the window, enqueue one reminder
// per key, and mark each one. A failure to list is the pass's failure, which
// leaves the caller no successor to queue. A failure on a single key is
// logged and skipped: the unmarked row is the fact the next pass re-reads, so
// an address that appears later, or an enqueue that failed once, still earns
// its reminder.
func apiKeyExpiryScan(ctx context.Context, pool *datastore.Postgres, client *queue.Client) error {
	now := time.Now()
	repo := apikey.NewRepository()
	keys, err := repo.ListExpiring(ctx, pool, now, now.Add(APIKeyExpiryWindow))
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}

	// The scan is an application act, not a request, so its record carries
	// no client facts and the trigger is the system's.
	recorder := audit.NewRecorder(slog.New(slog.DiscardHandler))

	for _, key := range keys {
		if key.OwnerEmail == "" {
			// An account with no address on record cannot be reminded; the
			// unmarked row is the fact the next pass re-reads, so an address
			// that appears later still earns its reminder.
			continue
		}

		_, err := client.Add(APIKeyExpiryEmailTask{
			KeyID:       key.Key.ID.String(),
			UserID:      key.Key.UserID.String(),
			Email:       key.OwnerEmail,
			DisplayName: key.OwnerName,
			APIKeyName:  key.Key.Name,
			ExpiresAt:   key.Key.ExpiresAt.Format(time.RFC3339),
		}).Ctx(ctx).Save()
		if err != nil {
			// One failed enqueue costs that key's reminder this pass; the
			// unmarked row is what the next pass retries.
			slog.ErrorContext(ctx, "queue: api key expiry reminder not enqueued",
				"key_id", key.Key.ID.String(), "error", err)
			continue
		}

		if _, err := repo.MarkEmailSent(ctx, pool, key.Key.ID, now); err != nil {
			slog.ErrorContext(ctx, "queue: api key expiry reminder not marked",
				"key_id", key.Key.ID.String(), "error", err)
			continue
		}

		recorder.Record(ctx, pool, audit.Entry{
			Event:        audit.EventAPIKeyExpiryEmailSent,
			Trigger:      audit.TriggerSystem,
			Status:       audit.StatusSuccess,
			UserID:       key.Key.UserID.String(),
			ResourceType: apikey.ResourceAPIKey,
			ResourceID:   key.Key.ID.String(),
			Payload: map[string]string{
				"name":       key.Key.Name,
				"expires_at": key.Key.ExpiresAt.Format(time.RFC3339),
			},
		})
	}
	return nil
}

// apiKeyExpiryEmailProcessor renders the template and submits one message.
func apiKeyExpiryEmailProcessor(ctx context.Context, task APIKeyExpiryEmailTask, mail *fwmailer.Service) error {
	if task.Email == "" || task.ExpiresAt == "" {
		return errors.New("api_key_expiry_email: task carries no address or expiry")
	}

	expiresAt, err := time.Parse(time.RFC3339, task.ExpiresAt)
	if err != nil {
		return errors.New("api_key_expiry_email: task carries an unreadable expiry")
	}

	if err := mail.Send(ctx, fwmailer.Request{
		To:       []string{task.Email},
		Subject:  "Your API key expires soon",
		Template: mailer.TemplateAPIKeyExpiringSoon,
		View: fwmailer.View{
			Email: task.Email,
			Data: mailer.APIKeyExpiringSoonData{
				Name:       task.DisplayName,
				APIKeyName: task.APIKeyName,
				ExpiresAt:  expiresAt.Format(time.RFC3339),
			},
		},
	}); err != nil {
		return err
	}
	slog.DebugContext(ctx, "queue: api key expiry reminder sent", "key_id", task.KeyID, "user_id", task.UserID)
	return nil
}
