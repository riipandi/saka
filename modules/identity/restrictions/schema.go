package restrictions

import (
	"time"
	"uuid"
)

// RestrictionTable is the account_restrictions table. The migrations own the
// schema; this constant is how Go code names it, so a table rename touches
// one line.
const RestrictionTable = "public.account_restrictions"

// usersTable is the account row's table — the failed-streak counter's
// storage. The literal is this package's spelling of it; the migrations own
// the schema.
const usersTable = "public.users"

// The restriction kinds the column's check admits. Ban is the
// administrator-applied term; lockout is the failed-attempt policy's
// automated one.
const (
	KindBan     = "ban"
	KindLockout = "lockout"
)

// Schema is one row of RestrictionTable. The active predicate every check
// shares lives in the queries: `lifted_at IS NULL AND (expires_at IS NULL
// OR expires_at > now())`. A NULL expires_at is indefinite.
type Schema struct {
	ID uuid.UUID `db:"id"`
	// UserID is the restricted account.
	UserID uuid.UUID `db:"user_id"`
	Kind   string    `db:"kind"`
	// Reason is why the restriction landed. The lockout carries none — the
	// policy's trip is the reason, and the audit record tells the story.
	Reason *string `db:"reason"`
	// StartedAt is when the restriction landed. A re-ban keeps the first
	// open row's start: the instant answers "since when".
	StartedAt time.Time `db:"started_at"`
	// ExpiresAt is when the restriction lifts by itself; NULL, never.
	ExpiresAt *time.Time `db:"expires_at"`
	// LiftedAt is when the restriction ended — an administrator's lift or
	// the expiry read that found it. NULL, still in force.
	LiftedAt *time.Time `db:"lifted_at"`
	// LiftedBy is the administrator who lifted it; an expiry lift carries
	// none.
	LiftedBy *uuid.UUID `db:"lifted_by"`
}
