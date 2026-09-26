package password

import (
	"time"

	"uuid"
)

// AuthTokenTable is the shared token table. The migration owns the schema;
// this constant is how Go code names it. The purpose check allows
// `password_reset` among the four, so the recovery flow needs no table of
// its own.
const AuthTokenTable = "public.auth_tokens"

// PurposePasswordReset is the purpose value the reset rows carry. The
// column's check allows it; a second purpose here would fail the insert.
const PurposePasswordReset = "password_reset"

// ResetTokenSchema is the slice of the auth_tokens row the recovery flow
// reads. The raw token is never stored: the caller's hash is.
type ResetTokenSchema struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	ExpiresAt time.Time  `db:"expires_at"`
	LastSent  *time.Time `db:"last_sent_at"`
}
