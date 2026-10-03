package signin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"
	"go.jetify.com/typeid"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/session"
)

// activeRestrictionJoin arms the restriction read model: the account's one
// active row — ban or lockout — joined beside the account read, its kind
// the state switch's question answered. The `ar` alias is the join's own.
func activeRestrictionJoin(sb *sqlbuilder.SelectBuilder) {
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableAccountRestrictions+" ar",
		"ar.user_id = u.id AND ar.lifted_at IS NULL AND (ar.expires_at IS NULL OR ar.expires_at > now())")
}

// Repository reads the account a sign-in names and writes the session row its
// refresh token is stored under.
type Repository struct {
	db datastore.Querier
}

// NewRepository builds the repository over the shared pool or a transaction.
func NewRepository(db datastore.Querier) *Repository {
	return &Repository{db: db}
}

// WithQuerier answers the same repository over another query surface, so a
// service can move its writes into a transaction it opens after the
// repository was built. Every method already takes the surface it runs on.
func (r *Repository) WithQuerier(db datastore.Querier) *Repository {
	return &Repository{db: db}
}

// Account is the sign-in's view of a user row and its password. The hash
// leaves this package only into the verifier, never into a response.
type Account struct {
	ID           uuid.UUID
	Username     string
	Email        string
	DisplayName  string
	Disabled     bool
	PasswordHash string
	// RestrictionKind is the account's active restriction — the ban and
	// lockout question the account_restrictions join answers. Empty, no
	// restriction stands; the package's kinds are the restrictions
	// feature's.
	RestrictionKind string
	EmailVerifiedAt *time.Time
}

// FindAccountByIdentity returns the account whose username or email matches
// identity. The username is CITEXT, so its comparison is case-insensitive;
// the email is TEXT matched exactly, the way its unique index does.
//
// An identity can never name two accounts: the username grammar admits no
// `@` and the email grammar requires one, so the two columns cannot both match.
func (r *Repository) FindAccountByIdentity(ctx context.Context, identity string) (*Account, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(
		"u.id", "coalesce(u.username, '') AS username", "u.email", "u.display_name",
		"u.disabled", "p.password_hash",
		"u.email_verified_at", "coalesce(ar.kind, '') AS restriction_kind",
	)
	sb.From(entity.TableUsers + " u")
	sb.Join(entity.TableUserPasswords + " p ON p.user_id = u.id")
	activeRestrictionJoin(sb)
	sb.Where(
		sb.Or(
			sb.Equal("u.username", identity),
			sb.Equal("u.email", identity),
		),
	)

	query, args := sb.Build()
	var row Account
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.Username, &row.Email, &row.DisplayName,
		&row.Disabled, &row.PasswordHash,
		&row.EmailVerifiedAt, &row.RestrictionKind,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, datastore.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("signin: find account: %w", err)
	}
	return &row, nil
}

// FindAccountByID returns the account the identifier names — the read the
// MFA bridge's completion runs, when the pending row has already named the
// account and the session issuer needs the row back.
func (r *Repository) FindAccountByID(ctx context.Context, id uuid.UUID) (*Account, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(
		"u.id", "coalesce(u.username, '') AS username", "u.email", "u.display_name",
		"u.disabled", "p.password_hash", "coalesce(ar.kind, '') AS restriction_kind",
	)
	sb.From(entity.TableUsers + " u")
	sb.Join(entity.TableUserPasswords + " p ON p.user_id = u.id")
	activeRestrictionJoin(sb)
	sb.Where(sb.Equal("u.id", id))

	query, args := sb.Build()
	var row Account
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.Username, &row.Email, &row.DisplayName,
		&row.Disabled, &row.PasswordHash, &row.RestrictionKind,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, datastore.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("signin: find account by id: %w", err)
	}
	return &row, nil
}

// FindAccountByIDAny returns the account the identifier names whether or not
// a password row rides it — the left join the passkey surfaces need, whose
// accounts sign in by credential alone. The inner-join read above is the
// one that answers "is there a way back in"; this one answers "is there an
// account".
func (r *Repository) FindAccountByIDAny(ctx context.Context, id uuid.UUID) (*Account, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(
		"u.id", "coalesce(u.username, '') AS username", "u.email", "u.display_name",
		"u.disabled", "p.password_hash", "coalesce(ar.kind, '') AS restriction_kind",
	)
	sb.From(entity.TableUsers + " u")
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableUserPasswords+" p ON p.user_id = u.id")
	activeRestrictionJoin(sb)
	sb.Where(sb.Equal("u.id", id))

	query, args := sb.Build()
	var row Account
	// The password hash is nullable here: the left join's whole point is the
	// account whose password row is absent, and the empty string is the
	// answer a verifier refuses.
	var hash *string
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.Username, &row.Email, &row.DisplayName,
		&row.Disabled, &hash, &row.RestrictionKind,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, datastore.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("signin: find account by id: %w", err)
	}
	if hash != nil {
		row.PasswordHash = *hash
	}
	return &row, nil
}

// CreateSession stores the refresh token's hashed row. The caller owns the
// transaction, so the session row and the last-login touch commit together.
// The typed session id leaves as its UUID: the column is a UUID, the `sess_`
// form is what a client and a log line read.
func (r *Repository) CreateSession(ctx context.Context, row session.SessionSchema) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableSessions)
	ib.Cols("id", "user_id", "provider", "token_hash", "user_agent", "device_fingerprint", "ip_address", "remember", "created_at", "expires_at")
	ib.Values(row.ID.UUID(), row.UserID, row.Provider, row.TokenHash, row.UserAgent, row.DeviceFingerprint, row.IPAddress, row.Remember, row.CreatedAt, row.ExpiresAt)

	query, args := ib.Build()
	if _, err := r.db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("signin: create session: %w", err)
	}
	return nil
}

// TouchLastLogin records the successful sign-in on the account.
func (r *Repository) TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableUsers)
	ub.Set(ub.Assign("last_login_at", at))
	ub.Where(ub.Equal("id", userID))

	query, args := ub.Build()
	if _, err := r.db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("signin: touch last login: %w", err)
	}
	return nil
}

// MarkDeviceSeen records a fingerprint as seen for an account and answers
// whether this was the first sighting. One statement does both: the insert
// wins the race (the unique pair makes the concurrent loser a no-op), and an
// empty result means the device was already known — no notice for it. The
// conflict branch keeps last_seen_at fresh, so a returning device's row says
// when it was last presented.
//
// db is the query surface the caller is already inside — the judgement rides
// the session's transaction, so a rolled-back sign-in leaves no device row.
func (r *Repository) MarkDeviceSeen(ctx context.Context, db datastore.Querier, userID uuid.UUID, fingerprint string, at time.Time) (bool, error) {
	id, err := typeid.New[KnownDeviceID]()
	if err != nil {
		return false, fmt.Errorf("signin: known device id: %w", err)
	}
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableKnownDevices)
	ib.Cols("id", "user_id", "device_fingerprint", "first_seen_at", "last_seen_at")
	ib.Values(id.UUID(), userID, fingerprint, at, at)
	ib.SQL("ON CONFLICT (user_id, device_fingerprint) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at RETURNING (xmax = 0) AS inserted")

	query, args := ib.Build()
	var inserted bool
	err = db.QueryRow(ctx, query, args...).Scan(&inserted)
	if err != nil {
		return false, fmt.Errorf("signin: mark device seen: %w", err)
	}
	return inserted, nil
}
