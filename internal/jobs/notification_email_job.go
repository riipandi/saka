package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/modules/notification"
)

// notificationEmailProcessor delivers one notification's email pass: the
// audience is read in pages and each page is mailed before the next, and
// the stamp lands only after the last page went out — a pass that dies
// mid-audience is retried whole, and a stamp a partial pass left would
// silence the retry.
//
// The task type and its queue live with the notification they name; this
// is the application logic the queue runs, and the mailer is the one
// dependency it needs that the notification package does not carry.
func notificationEmailProcessor(ctx context.Context, task notification.NotificationEmailTask, pool *datastore.Postgres, mail *mailer.Service) error {
	if mail == nil {
		return errors.New("notification_email: mailer missing")
	}
	if !mail.Mailer().Configured() {
		// An unconfigured mailer is a deployment state, not a failure: the
		// pass has nowhere to go, and the gate at the enqueue site is what
		// keeps this from running in a deployment without mail at all.
		return nil
	}

	repo := notification.NewRepository()
	row, _, _, err := repo.Get(ctx, pool, task.NotificationID)
	if errors.Is(err, datastore.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// A withdrawn notification delivers nothing: the withdrawal is the
	// audience's experience from that instant on.
	if row.CancelledAt != nil {
		return nil
	}

	after := uuid.Nil()
	for {
		recipients, err := repo.Recipients(ctx, pool, task.NotificationID, after, notification.NotificationEmailPage)
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			break
		}

		for _, recipient := range recipients {
			if err := mail.Send(ctx, mailer.Request{
				To:       []string{recipient.Email},
				Subject:  row.Title,
				Template: mailer.TemplateAnnouncement,
				View: mailer.View{Data: mailer.AnnouncementData{
					Name:  recipient.DisplayName,
					Topic: derefString(row.Topic),
					Title: row.Title,
					Body:  row.Body,
				}},
			}); err != nil {
				return fmt.Errorf("notification_email: send to %s: %w", recipient.ID, err)
			}
			after = recipient.ID
		}
	}

	return repo.MarkEmailSent(ctx, pool, task.NotificationID, time.Now())
}
