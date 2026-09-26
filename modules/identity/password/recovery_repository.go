package password

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"uuid"

	"github.com/riipandi/tango/internal/datastore"
)

// Repository reads and writes the reset token and the account state it
// opens. Every method takes the query surface, so the service passes either
// the pool or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// Account is the slice of the users row the flow needs: who to greet, where
// to send, and whether the credential is refused right now.
type Account struct {
	ID          uuid.UUID
	Username    string
	Email       string
	DisplayName string
	Disabled    bool
	BannedAt    *time.Time
	BanExpires  *time.Time
	PasswordSet bool
}

// FindUserByEmail reads the account an address names. The email is TEXT
// matched exactly, the way its unique index does — the same match the
// sign-in identity accepts.
func (r *Repository) FindUserByEmail(ctx context.Context, db datastore.Querier, email string) (Account, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(
		"u.id", "u.username", "u.email", "u.display_name",
		"u.disabled", "u.banned_at", "u.ban_expires",
		"p.password_hash IS NOT NULL",
	)
	sb.From("public.users AS u")
	sb.JoinWithOption(sqlbuilder.LeftJoin, "public.user_passwords AS p", "p.user_id = u.id")
	sb.Where(sb.Equal("u.email", email))

	query, args := sb.Build()
	var row Account
	err := db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.Username, &row.Email, &row.DisplayName,
		&row.Disabled, &row.BannedAt, &row.BanExpires, &row.PasswordSet,
	)
	if errors.Is(err, datastore.ErrNoRows) {
		return Account{}, datastore.ErrNoRows
	}
	if err != nil {
		return Account{}, fmt.Errorf("password: find user by email: %w", err)
	}
	return row, nil
}

// FindUserByID reads the account the admin trigger names. The id is the
// UUID the wire-form TypeID decodes to.
func (r *Repository) FindUserByID(ctx context.Context, db datastore.Querier, userID uuid.UUID) (Account, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(
		"u.id", "u.username", "u.email", "u.display_name",
		"u.disabled", "u.banned_at", "u.ban_expires",
		"p.password_hash IS NOT NULL",
	)
	sb.From("public.users AS u")
	sb.JoinWithOption(sqlbuilder.LeftJoin, "public.user_passwords AS p", "p.user_id = u.id")
	sb.Where(sb.Equal("u.id", userID))

	query, args := sb.Build()
	var row Account
	err := db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.Username, &row.Email, &row.DisplayName,
		&row.Disabled, &row.BannedAt, &row.BanExpires, &row.PasswordSet,
	)
	if errors.Is(err, datastore.ErrNoRows) {
		return Account{}, datastore.ErrNoRows
	}
	if err != nil {
		return Account{}, fmt.Errorf("password: find user by id: %w", err)
	}
	return row, nil
}

// UpsertToken writes the reset token and answers nothing: the account
// carries at most one row per purpose, so a re-request replaces the hash,
// moves the window, and stamps the send time over the row it conflicts
// with.
func (r *Repository) UpsertToken(ctx context.Context, db datastore.Querier, userID uuid.UUID, tokenHash string, expiresAt, sentAt time.Time) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(AuthTokenTable)
	ib.Cols("user_id", "token_hash", "purpose", "expires_at", "last_sent_at")
	ib.Values(userID, tokenHash, PurposePasswordReset, expiresAt, sentAt)
	ib.SQL("ON CONFLICT (user_id, purpose) DO UPDATE SET " +
		"token_hash = EXCLUDED.token_hash, " +
		"expires_at = EXCLUDED.expires_at, " +
		"last_sent_at = EXCLUDED.last_sent_at")

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("password: upsert reset token: %w", err)
	}
	return nil
}

// FindTokenByHash reads the reset row a raw value hashes to. The raw value
// is never stored: only the caller's hash reaches this query.
func (r *Repository) FindTokenByHash(ctx context.Context, db datastore.Querier, tokenHash string) (ResetTokenSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "expires_at", "last_sent_at")
	sb.From(AuthTokenTable)
	sb.Where(
		sb.Equal("token_hash", tokenHash),
		sb.Equal("purpose", PurposePasswordReset),
	)

	query, args := sb.Build()
	var row ResetTokenSchema
	err := db.QueryRow(ctx, query, args...).Scan(&row.ID, &row.UserID, &row.ExpiresAt, &row.LastSent)
	if errors.Is(err, datastore.ErrNoRows) {
		return ResetTokenSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return ResetTokenSchema{}, fmt.Errorf("password: find reset token: %w", err)
	}
	return row, nil
}

// FindTokenByUser reads the reset row an account carries, whatever value it
// hashes. The resend cooldown reads the send time it stamps.
func (r *Repository) FindTokenByUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) (ResetTokenSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "expires_at", "last_sent_at")
	sb.From(AuthTokenTable)
	sb.Where(
		sb.Equal("user_id", userID),
		sb.Equal("purpose", PurposePasswordReset),
	)

	query, args := sb.Build()
	var row ResetTokenSchema
	err := db.QueryRow(ctx, query, args...).Scan(&row.ID, &row.UserID, &row.ExpiresAt, &row.LastSent)
	if errors.Is(err, datastore.ErrNoRows) {
		return ResetTokenSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return ResetTokenSchema{}, fmt.Errorf("password: find reset token by user: %w", err)
	}
	return row, nil
}

// DeleteToken removes the reset row. The delete is the consumption: a
// second caller presenting the same token loses the race and reads nothing.
func (r *Repository) DeleteToken(ctx context.Context, db datastore.Querier, id uuid.UUID) error {
	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(AuthTokenTable)
	dbl.Where(dbl.Equal("id", id), dbl.Equal("purpose", PurposePasswordReset))

	query, args := dbl.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("password: delete reset token: %w", err)
	}
	return nil
}

// SetPasswordHash writes the new credential. The row is upserted because a
// reset may be the account's first password (a passkey-created account that
// added one).
func (r *Repository) SetPasswordHash(ctx context.Context, db datastore.Querier, userID uuid.UUID, hash string) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(UserPasswordTable)
	ib.Cols("user_id", "password_hash")
	ib.Values(userID, hash)
	ib.SQL("ON CONFLICT (user_id) DO UPDATE SET password_hash = EXCLUDED.password_hash")

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("password: set password hash: %w", err)
	}
	return nil
}
