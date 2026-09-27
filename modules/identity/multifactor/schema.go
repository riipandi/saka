package multifactor

import (
	"time"

	"uuid"

	"go.jetify.com/typeid"
)

// TotpTable is the table holding one row per enrolled TOTP authenticator. The
// migrations own the schema; this constant is how Go code names it, so a table
// rename touches one line.
const TotpTable = "public.user_mfa_totp"

// RecoveryTable is the table holding the hashed single-use recovery codes.
const RecoveryTable = "public.user_mfa_recovery_codes"

// PendingTable is the table holding the short-lived bridges a successful
// password check writes between the first factor and the full session.
const PendingTable = "public.user_mfa_pending"

// TotpPrefix is the TypeID prefix of an enrolled authenticator's identifier.
// The id leaves the server in API responses, so a support ticket can tell
// which device the reader is looking at without a lookup.
type TotpPrefix struct{}

// Prefix reports the TypeID prefix.
func (TotpPrefix) Prefix() string { return "totp" }

// TotpID is the typed identifier of one row of TotpTable.
type TotpID = typeid.TypeID[TotpPrefix]

// IDFromUUID wraps the row's UUID into the wire form. It is the one direction
// every response takes.
func IDFromUUID(raw uuid.UUID) (TotpID, error) {
	return typeid.FromUUID[TotpID](raw.String())
}

// AlgorithmSHA1 and friends name the algorithms the table's check constraint
// admits. SHA1 here is the HMAC the RFC 6238 reference implementation uses and
// every authenticator app supports — it is not a TLS-certificate SHA-1, and
// hardening it away breaks compatibility with every device on the market.
const (
	AlgorithmSHA1   = "SHA1"
	AlgorithmSHA256 = "SHA256"
	AlgorithmSHA512 = "SHA512"
)

// TotpSchema is one enrolled authenticator. It lists only the columns the
// application writes; the db tags are the column names the query builder uses.
type TotpSchema struct {
	ID           uuid.UUID  `db:"id"`
	UserID       uuid.UUID  `db:"user_id"`
	Name         string     `db:"name"`
	Secret       string     `db:"secret"`
	Digits       int16      `db:"digits"`
	Period       int16      `db:"period"`
	Algorithm    string     `db:"algorithm"`
	ConfirmedAt  *time.Time `db:"confirmed_at"`
	LastUsedStep *int64     `db:"last_used_step"`
	LastUsedAt   *time.Time `db:"last_used_at"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

// RecoverySchema is one hashed recovery code. The set is per account, not per
// device: every code replaces a lost authenticator, and regenerate rewrites
// the whole set.
type RecoverySchema struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	CodeHash  string     `db:"code_hash"`
	UsedAt    *time.Time `db:"used_at"`
	CreatedAt time.Time  `db:"created_at"`
}

// PendingSchema is one pending-auth bridge row.
type PendingSchema struct {
	ID        uuid.UUID `db:"id"`
	UserID    uuid.UUID `db:"user_id"`
	TokenHash string    `db:"token_hash"`
	Remember  bool      `db:"remember"`
	ExpiresAt time.Time `db:"expires_at"`
	CreatedAt time.Time `db:"created_at"`
	// WrongAttempts is the wrong-code budget the bridge has spent. The count
	// rides the row so every replica judges the same bridge the same way,
	// and so the budget survives a process restart inside the bridge's life.
	WrongAttempts int `db:"wrong_attempts"`
}
