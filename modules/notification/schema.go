// Package notification is the notification area: what an administrator
// announces and what an account reads.
//
// It is an area of its own rather than a feature of identity because a
// notification is not an account fact — it is a message the deployment
// publishes to an audience the administrator draws, and the surface that
// manages it is administrative while the surface that reads it is every
// account's.
//
// The area owns its own wiring, like every other: the registry names it
// and knows nothing about its service. The email pass rides the durable
// queue through the task internal/jobs carries; the live tail is the
// in-process broker the service holds, because a stream is a connection
// of this process and nothing durable stands behind it.
package notification

import (
	"fmt"
	"time"
	"uuid"

	"go.jetify.com/typeid"

	"github.com/riipandi/saka/pkg/strutils"
)

// ResourceNotification is the resource type an audit record names when the
// change is about a notification. The record's user_id names the
// administrator who made the change.
const ResourceNotification = "notification"

// NotificationIDPrefix is the TypeID prefix of a notification's identifier.
// The id leaves the server in an API response, so the reader of a log line
// or a support ticket can tell what it names without a lookup.
type NotificationIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (NotificationIDPrefix) Prefix() string { return "ntf" }

// NotificationID is the typed identifier of one row of entity.TableNotifications,
// in its wire form. The column stays a UUID; the conversion lives here and
// nowhere else.
type NotificationID = typeid.TypeID[NotificationIDPrefix]

// IDFromUUID wraps the row's UUID into the wire form.
func IDFromUUID(raw uuid.UUID) (NotificationID, error) {
	return strutils.EncodeID[NotificationID](raw)
}

// FormatID renders the wire form of a row's UUID. Rows read from the
// database always carry a valid UUID, so the render cannot fail; an invalid
// one answers the empty string, which no consumer should mistake for an id.
func FormatID(raw uuid.UUID) string {
	return strutils.FormatID[NotificationID](raw)
}

// ParseID reads the wire form back. It is the boundary a request crosses:
// an identifier that arrives without the prefix names no notification, the
// not-found the caller refuses.
func ParseID(wire string) (NotificationID, error) {
	parsed, err := strutils.ParseID[NotificationID](wire)
	if err != nil {
		return NotificationID{}, fmt.Errorf("notification: %w", err)
	}
	return parsed, nil
}

// UUIDFromWire is the request boundary in one step: the wire form a request
// carries in, the key the rows carry out.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	return strutils.UUIDFromWire[NotificationID](wire)
}

// The category and audience values the contract validates on the wire.
// They are spelled here so the repository's queries and the service's
// rules read the same words the wire carries.
const (
	CategorySystem       = "system"
	CategoryAnnouncement = "announcement"

	AudienceGlobal = "global"
	AudienceUsers  = "users"
	AudienceGroups = "user_groups"
)

// Notification is one row of entity.TableNotifications. The nullable instants are
// nullable by construction — the trigger fills updated_at on the first
// update, and a live notification carries neither a cancellation nor an
// email stamp.
type Notification struct {
	ID           uuid.UUID  `db:"id"`
	Category     string     `db:"category"`
	Topic        *string    `db:"topic"`
	Title        string     `db:"title"`
	Body         string     `db:"body"`
	AudienceKind string     `db:"audience_kind"`
	CreatedBy    *uuid.UUID `db:"created_by"`
	CancelledAt  *time.Time `db:"cancelled_at"`
	EmailSentAt  *time.Time `db:"email_sent_at"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    *time.Time `db:"updated_at"`
}

// InboxRow is one notification as its reader sees it: the row plus the
// instant the reader marked it read, absent while it is unread. The
// audience junctions are resolved by the query, not carried — a reader
// sees the notification, not the audience it drew.
type InboxRow struct {
	Notification
	ReadAt *time.Time `db:"read_at"`
}

// Recipient is one account the audience resolves to, with the address its
// email pass goes to and the name the message greets. The disabled
// accounts are not recipients: a notice nobody can sign in to read is
// still delivered nowhere.
type Recipient struct {
	ID          uuid.UUID `db:"id"`
	Email       string    `db:"email"`
	DisplayName string    `db:"display_name"`
}
