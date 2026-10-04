package user

import (
	"encoding/json/v2"
	"fmt"
	"time"

	"uuid"

	"go.jetify.com/typeid"

	"github.com/riipandi/saka/pkg/strutils"
)

// UserSchema is one row of entity.TableUsers. It lists only the columns the application
// writes and the account procedures read, so a migration can add a column with
// a default without touching this struct. The db tags are the column names the
// query builder uses.
type UserSchema struct {
	ID          uuid.UUID `db:"id"`
	Username    string    `db:"username"`
	Email       string    `db:"email"`
	FirstName   string    `db:"first_name"`
	LastName    string    `db:"last_name"`
	DisplayName string    `db:"display_name"`
	// Metadata is the account's preference document — the locale and the
	// timezone live here as JSON keys, not as columns, so a new preference
	// is a code change and not a migration. The readers and the writers go
	// through the metadata helpers in the service; nothing touches the raw
	// bytes elsewhere.
	Metadata        []byte     `db:"metadata"`
	Disabled        bool       `db:"disabled"`
	EmailVerifiedAt *time.Time `db:"email_verified_at"`
	CreatedAt       time.Time  `db:"created_at"`
	// UpdatedAt is the account's change stamp — the trigger
	// trg_users_updated_at writes it on every update, and rows never
	// updated since creation carry NULL. The OIDC profile claims answer it
	// as the Standard Claim updated_at.
	UpdatedAt *time.Time `db:"updated_at"`
	// The ban read-model: the active ban restriction's view, joined from
	// public.account_restrictions by the reads that answer it. No insert
	// or update writes these — the ban's storage is the restriction row,
	// and the writes go through the restrictions feature.
	BannedAt   *time.Time `db:"banned_at"`
	BanExpires *time.Time `db:"ban_expires"`
	BanReason  *string    `db:"ban_reason"`
	// The picture read-model: the filestore object the account's picture
	// row names, joined from public.storage_objects and its bucket by the
	// reads that answer it. No insert or update writes these — the
	// picture's storage is the object row, and the writes go through the
	// storage engine. Bucket and key are what a public URL composes from.
	PictureBucket      *string `db:"picture_bucket"`
	PictureKey         *string `db:"picture_key"`
	SelfDeleteOverride *bool   `db:"self_delete_override"`
}

// UserMetadata is the account's preference document — the typed shape of
// the JSONB `users.metadata` column. The document is closed: locale and
// timezone are the whole contract, so an engineer reads the keys here and
// nowhere else. The zero document answers every default; a malformed or
// absent stored document parses to it rather than failing the account read.
//
// Raw is the write side: an empty document marshals no keys, and the
// column answers NULL when no key rests. A future preference joins this
// struct — a code change and a read of this comment, never a migration.
type UserMetadata struct {
	// Locale is the preferred locale tag, empty meaning unset.
	Locale string `json:"locale,omitzero"`
	// Timezone is the IANA zone the frontend formats instants against.
	// Storage writes it always (normalizeTimezone answered the default);
	// reads answer DefaultTimezone when it is absent.
	Timezone string `json:"timezone,omitzero"`
}

// ParseUserMetadata reads the stored document. The zero document answers a
// nil, empty, or malformed one — a corrupt document degrades the account's
// presentation to the defaults instead of failing the read.
func ParseUserMetadata(raw []byte) UserMetadata {
	if len(raw) == 0 {
		return UserMetadata{}
	}
	var doc UserMetadata
	if err := json.Unmarshal(raw, &doc); err != nil {
		return UserMetadata{}
	}
	return doc
}

// Raw marshals the document for storage. A document that carries no key
// rests no bytes — the column stores NULL, the state "every preference is
// absent" — and a two-string document cannot fail to marshal, so an error
// answers no bytes rather than a panic.
func (m UserMetadata) Raw() []byte {
	if m.Locale == "" && m.Timezone == "" {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return raw
}

// TimezoneOrDefault answers the document's timezone, DefaultTimezone when
// the document omits one.
func (m UserMetadata) TimezoneOrDefault() string {
	if m.Timezone == "" {
		return DefaultTimezone
	}
	return m.Timezone
}

// UserIDPrefix is the TypeID prefix of an account's identifier. The id
// leaves the server in a token subject and an API response, so the reader of
// a log line or a support ticket can tell what it names without a lookup.
type UserIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (UserIDPrefix) Prefix() string { return "user" }

// UserID is the typed identifier of one row of the users table, in its wire
// form.
type UserID = typeid.TypeID[UserIDPrefix]

// FromUUID wraps the row's UUID into the wire form. It is the one direction
// every response and every signed token takes.
func IDFromUUID(raw uuid.UUID) (UserID, error) {
	return strutils.EncodeID[UserID](raw)
}

// FromUUIDString wraps a UUID in its text form into the wire form. Rows scan
// as text in several seams, so this is the shape those callers take.
func IDFromUUIDString(raw string) (UserID, error) {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return UserID{}, fmt.Errorf("user: %w", err)
	}
	return IDFromUUID(parsed)
}

// FormatID renders the wire form of a row's UUID. Rows read from the database
// always carry a valid UUID, so the render cannot fail; an invalid one
// answers the empty string, which no consumer should mistake for an id.
func FormatID(raw uuid.UUID) string {
	return strutils.FormatID[UserID](raw)
}

// ParseID reads the wire form back. It is the boundary a request crosses: an
// identifier that arrives without the prefix names nothing this server
// speaks about, and the caller refuses it as the not-found it is.
func ParseID(wire string) (UserID, error) {
	parsed, err := strutils.ParseID[UserID](wire)
	if err != nil {
		return UserID{}, fmt.Errorf("user: %w", err)
	}
	return parsed, nil
}

// IDToUUID unwraps the wire form into the UUID the column stores. The typed id
// carries the bytes itself, so nothing re-parses text to get there.
func IDToUUID(id UserID) uuid.UUID {
	return strutils.ToUUID(id)
}

// UUIDFromWire is the request boundary in one step: the wire form a request
// carries in, the key the rows carry out. A malformed identifier names
// nothing, and the caller refuses it as the not-found it is.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	return strutils.UUIDFromWire[UserID](wire)
}
