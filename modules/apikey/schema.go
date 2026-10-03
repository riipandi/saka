package apikey

import (
	"time"

	"uuid"
)

// ResourceAPIKey is the resource type an audit record names when the change is
// about a key. The record's user_id names the owner — unlike a group, a key
// belongs to an account — and the key is named in resource_type and
// resource_id.
const ResourceAPIKey = "api_key"

// KeySchema is one row of entity.TableAPIKeys. The hash is the key's whole stored
// presence: the raw credential exists in exactly one response and is never
// written down. The nullable instants are nullable by construction — the
// trigger fills updated_at on the first update, and a key that has never
// authenticated a request or been revoked carries nothing to say about it.
type KeySchema struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	Name      string     `db:"name"`
	Prefix    string     `db:"prefix"`
	KeyHash   []byte     `db:"key_hash"`
	Descr     *string    `db:"description"`
	ExpiresAt time.Time  `db:"expires_at"`
	LastUsed  *time.Time `db:"last_used_at"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
	RevokedAt *time.Time `db:"revoked_at"`
	EmailSent *time.Time `db:"expiration_email_sent_at"`
}
