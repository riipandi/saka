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
	"time"
	"uuid"
)

// The tables the migrations own. These constants are how Go code names
// them, so a table rename touches one line.
const (
	// NotificationTable is the notifications table.
	NotificationTable = "public.notifications"

	// UserAudienceTable is the junction naming the accounts a `users`
	// audience targets.
	UserAudienceTable = "public.notification_users"

	// GroupAudienceTable is the junction naming the groups a
	// `user_groups` audience targets.
	GroupAudienceTable = "public.notification_user_groups"

	// ReadTable is the per-account read receipts.
	ReadTable = "public.notification_reads"
)

// ResourceNotification is the resource type an audit record names when the
// change is about a notification. The record's user_id names the
// administrator who made the change.
const ResourceNotification = "notification"

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

// Notification is one row of NotificationTable. The nullable instants are
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
