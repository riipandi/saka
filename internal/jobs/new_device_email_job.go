package jobs

import (
	"context"
	"errors"
	"time"

	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/queue"
)

// NewDeviceEmailName is the queue the new-device notices run on.
const NewDeviceEmailName = "new_device_email"

// NewDeviceEmailTask renders the new-sign-in template and submits one
// message. The location the template shows is left unnamed here: the process
// resolves no GeoIP, so the template's own fallback prints it as unknown
// rather than the mailer guessing.
type NewDeviceEmailTask struct {
	// Email is the address on record at the moment of the sign-in.
	Email string `json:"email"`

	// IPAddress is the address the session was opened from, empty when the
	// transport could not tell.
	IPAddress string `json:"ip_address"`

	// UserAgent is the agent the session recorded.
	UserAgent string `json:"user_agent"`

	// SignedInAt is when the session opened; the template formats it.
	SignedInAt time.Time `json:"signed_in_at"`
}

// Config returns the queue the new-device notices run on. The attempts stay
// generous for the same reason the other security notices keep them: an SMTP
// outage is the ordinary retry, and a heads-up about an unfamiliar device is
// exactly the message that must not be lost to one.
func (t NewDeviceEmailTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        NewDeviceEmailName,
		MaxAttempts: 5,
		Timeout:     time.Minute,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}
}

// newDeviceEmailProcessor renders the template and submits one message.
func newDeviceEmailProcessor(ctx context.Context, task NewDeviceEmailTask, mail *fwmailer.Service) error {
	if task.Email == "" {
		return errors.New("new_device_email: task carries no address")
	}
	return mail.Send(ctx, fwmailer.Request{
		To:       []string{task.Email},
		Subject:  "New sign-in to your account",
		Template: mailer.TemplateLoginNewDevice,
		View: fwmailer.View{
			Email: task.Email,
			Data: mailer.LoginNewDeviceData{
				IPAddress: task.IPAddress,
				Device:    task.UserAgent,
				DateTime:  task.SignedInAt,
			},
		},
	})
}
