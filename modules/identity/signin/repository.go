package signin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"
	"go.jetify.com/typeid"
	"uuid"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/password"
	"github.com/riipandi/tango/modules/identity/session"
	"github.com/riipandi/tango/modules/identity/user"
)

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
	BannedAt     *time.Time
	BanExpires   *time.Time
	PasswordHash string
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
		"u.id", "u.username", "u.email", "u.display_name",
		"u.disabled", "u.banned_at", "u.ban_expires", "p.password_hash",
	)
	sb.From(user.UserTable + " u")
	sb.Join(password.UserPasswordTable + " p ON p.user_id = u.id")
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
		&row.Disabled, &row.BannedAt, &row.BanExpires, &row.PasswordHash,
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
		"u.id", "u.username", "u.email", "u.display_name",
		"u.disabled", "u.banned_at", "u.ban_expires", "p.password_hash",
	)
	sb.From(user.UserTable + " u")
	sb.Join(password.UserPasswordTable + " p ON p.user_id = u.id")
	sb.Where(sb.Equal("u.id", id))

	query, args := sb.Build()
	var row Account
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.Username, &row.Email, &row.DisplayName,
		&row.Disabled, &row.BannedAt, &row.BanExpires, &row.PasswordHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, datastore.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("signin: find account by id: %w", err)
	}
	return &row, nil
}

// CreateSession stores the refresh token's hashed row. The caller owns the
// transaction, so the session row and the last-login touch commit together.
// The typed session id leaves as its UUID: the column is a UUID, the `sess_`
// form is what a client and a log line read.
func (r *Repository) CreateSession(ctx context.Context, row session.SessionSchema) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(session.SessionTable)
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
	ub.Update(user.UserTable)
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
	ib.InsertInto(KnownDeviceTable)
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
