package onetimeaccess

import (
	"time"

	"uuid"
)

// The table the one-time access codes live in. The migrations own the schema;
// these constants are how Go code names it, so a rename touches one line. The
// table is shared with the email-verification feature, which names it for its
// own purpose — the purpose column is what keeps the two apart, and the
// unique index on (user_id, purpose) keeps an account to one code at a time:
// a new code replaces the old, so no cleanup job ever has rows to sweep.
const tokenTable = "public.auth_tokens"

// PurposeOneTimeAccess is the purpose value the rows this feature writes
// carry.
const PurposeOneTimeAccess = "one_time_access"

// OneTimeToken is one row of the token table under the one-time purpose. The
// raw code is never stored — the caller's hash is — and the device token is
// the second half the email path pairs with it: the requester holds the one,
// the mailbox holds the other, and the exchange demands both.
type OneTimeToken struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	ExpiresAt   time.Time
	DeviceToken *string
	LastSentAt  *time.Time
}

// Account is the account view the exchange and the email sends read. The
// exchange names the session's bearer and the issuer's state checks; the
// email send names the address and the greeting.
type Account struct {
	ID          uuid.UUID
	Username    string
	Email       string
	DisplayName string
	Disabled    bool
	BannedAt    *time.Time
	BanExpires  *time.Time
}
