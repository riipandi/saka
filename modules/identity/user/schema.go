package user

import (
	"fmt"
	"time"

	"go.jetify.com/typeid"
	"uuid"
)

// UserTable is the users table. The migrations own the schema; this constant is
// how Go code names it, so a table rename touches one line.
const UserTable = "public.users"

// UserSchema is one row of UserTable. It lists only the columns the application
// writes and the account procedures read, so a migration can add a column with
// a default without touching this struct. The db tags are the column names the
// query builder uses.
type UserSchema struct {
	ID              uuid.UUID  `db:"id"`
	Username        string     `db:"username"`
	Email           string     `db:"email"`
	FirstName       string     `db:"first_name"`
	LastName        string     `db:"last_name"`
	DisplayName     string     `db:"display_name"`
	Locale          string     `db:"locale"`
	Timezone        string     `db:"timezone"`
	Disabled        bool       `db:"disabled"`
	EmailVerifiedAt *time.Time `db:"email_verified_at"`
	CreatedAt       time.Time  `db:"created_at"`
	BannedAt        *time.Time `db:"banned_at"`
	BanExpires      *time.Time `db:"ban_expires"`
	BanReason       *string    `db:"ban_reason"`
	AvatarURL       *string    `db:"avatar_url"`
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
	return typeid.FromUUID[UserID](raw.String())
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
	id, err := IDFromUUID(raw)
	if err != nil {
		return ""
	}
	return id.String()
}

// ParseID reads the wire form back. It is the boundary a request crosses: an
// identifier that arrives without the prefix names nothing this server
// speaks about, and the caller refuses it as the not-found it is.
func ParseID(wire string) (UserID, error) {
	parsed, err := typeid.Parse[UserID](wire)
	if err != nil {
		return UserID{}, fmt.Errorf("user: %w", err)
	}
	return parsed, nil
}

// IDToUUID unwraps the wire form into the UUID the column stores. The typed id
// carries the bytes itself, so nothing re-parses text to get there.
func IDToUUID(id UserID) uuid.UUID {
	return uuid.UUID(id.UUIDBytes())
}

// UUIDFromWire is the request boundary in one step: the wire form a request
// carries in, the key the rows carry out. A malformed identifier names
// nothing, and the caller refuses it as the not-found it is.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	id, err := ParseID(wire)
	if err != nil {
		return uuid.Nil(), err
	}
	return IDToUUID(id), nil
}
