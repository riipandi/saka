package verification

import (
	"time"

	"uuid"
)

// The tables the email-verification feature reads and writes. The migrations
// own the schema; these constants are how Go code names it, so a table rename
// touches one line.

// AuthTokenTable is the one-time-token table the verification row lives in.
const AuthTokenTable = "public.auth_tokens"

// PurposeEmailVerification is the purpose value the verification rows carry.
// The column's check allows it alone among the four; a second purpose here
// would need its own migration first.
const PurposeEmailVerification = "email_verification"

// PurposeEmailChange is the purpose value the email-change rows carry: the
// pending token a change request writes and the confirmation consumes.
const PurposeEmailChange = "email_change"

// VerificationToken is one row of AuthTokenTable under the verification
// purpose. The raw value is never stored: the caller's hash is.
type VerificationToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	ExpiresAt  time.Time
	LastSentAt *time.Time
}

// EmailChangeToken is one row of AuthTokenTable under the email-change
// purpose. Payload is the pending address the token is bound to: the
// confirmation moves the account to the row's address, never to one the
// confirm request could name, so a token stolen in transit cannot point the
// account anywhere its holder did not ask for.
type EmailChangeToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Payload    string
	ExpiresAt  time.Time
	LastSentAt *time.Time
}
