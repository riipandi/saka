package verification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"uuid"

	"github.com/riipandi/saka/internal/datastore"
)

// Repository reads and writes the verification token and the account state
// it verifies. Every method takes the query surface, so the service passes
// either the pool or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// Account is the slice of the users row the flow needs: who to greet, where
// to send, and whether the work is already done.
type Account struct {
	ID              uuid.UUID
	Username        string
	Email           string
	DisplayName     string
	EmailVerifiedAt *time.Time
}

// UpsertToken writes the verification token and answers nothing: the account
// carries at most one row per purpose, so a re-request replaces the hash,
// moves the window, and stamps the send time over the row it conflicts with.
func (r *Repository) UpsertToken(ctx context.Context, db datastore.Querier, userID uuid.UUID, tokenHash string, expiresAt, sentAt time.Time) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(AuthTokenTable)
	ib.Cols("user_id", "token_hash", "purpose", "expires_at", "last_sent_at")
	ib.Values(userID, tokenHash, PurposeEmailVerification, expiresAt, sentAt)
	// The conflict target carries the partial index's predicate: the unique
	// index excludes reauthentication, so a bare column list matches nothing.
	ib.SQL("ON CONFLICT (user_id, purpose) WHERE purpose <> 'reauthentication' DO UPDATE SET " +
		"token_hash = EXCLUDED.token_hash, " +
		"expires_at = EXCLUDED.expires_at, " +
		"last_sent_at = EXCLUDED.last_sent_at")

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("verification: upsert token: %w", err)
	}
	return nil
}

// FindTokenByHash reads the verification row a raw value hashes to. The raw
// value is never stored: only the caller's hash reaches this query.
func (r *Repository) FindTokenByHash(ctx context.Context, db datastore.Querier, tokenHash string) (VerificationToken, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "expires_at", "last_sent_at")
	sb.From(AuthTokenTable)
	sb.Where(
		sb.Equal("token_hash", tokenHash),
		sb.Equal("purpose", PurposeEmailVerification),
	)

	query, args := sb.Build()
	var row VerificationToken
	err := db.QueryRow(ctx, query, args...).Scan(&row.ID, &row.UserID, &row.ExpiresAt, &row.LastSentAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return VerificationToken{}, datastore.ErrNoRows
	}
	if err != nil {
		return VerificationToken{}, fmt.Errorf("verification: find token: %w", err)
	}
	return row, nil
}

// FindTokenByUser reads the verification row an account carries, whatever
// value it hashes. The resend cooldown reads the send time it stamps.
func (r *Repository) FindTokenByUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) (VerificationToken, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "expires_at", "last_sent_at")
	sb.From(AuthTokenTable)
	sb.Where(
		sb.Equal("user_id", userID),
		sb.Equal("purpose", PurposeEmailVerification),
	)

	query, args := sb.Build()
	var row VerificationToken
	err := db.QueryRow(ctx, query, args...).Scan(&row.ID, &row.UserID, &row.ExpiresAt, &row.LastSentAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return VerificationToken{}, datastore.ErrNoRows
	}
	if err != nil {
		return VerificationToken{}, fmt.Errorf("verification: find token by user: %w", err)
	}
	return row, nil
}

// MarkVerified stamps the account's address as verified. The conditional
// update keeps an earlier verification instant when one is on record: a
// row that already carries the stamp is matched by nothing and stays as it
// was, and the caller treats that as success — the token is consumed either
// way.
func (r *Repository) MarkVerified(ctx context.Context, db datastore.Querier, userID uuid.UUID, at time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update("public.users")
	ub.Set(ub.Assign("email_verified_at", at))
	ub.Where(ub.Equal("id", userID), ub.IsNull("email_verified_at"))

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("verification: mark verified: %w", err)
	}
	return nil
}

// DeleteToken consumes the token row. The WHERE carries the hash the caller
// looked up, so a row a re-request replaced cannot be deleted by the stale
// read that named it: a superseded token answers false, and the caller aborts
// the transaction rather than applying the payload it read from the old row.
func (r *Repository) DeleteToken(ctx context.Context, db datastore.Querier, id uuid.UUID, tokenHash string, purpose string) (bool, error) {
	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(AuthTokenTable)
	dbl.Where(dbl.Equal("id", id), dbl.Equal("token_hash", tokenHash), dbl.Equal("purpose", purpose))

	query, args := dbl.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("verification: delete token: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// FindUserByID reads the account the identifier names — the read the
// confirmation runs inside its transaction, when the pending token has
// already named the account whose address moves.
func (r *Repository) FindUserByID(ctx context.Context, db datastore.Querier, id uuid.UUID) (Account, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "username", "email", "display_name", "email_verified_at")
	sb.From("public.users")
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	var row Account
	err := db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.Username, &row.Email, &row.DisplayName, &row.EmailVerifiedAt,
	)
	if errors.Is(err, datastore.ErrNoRows) {
		return Account{}, datastore.ErrNoRows
	}
	if err != nil {
		return Account{}, fmt.Errorf("verification: find user by id: %w", err)
	}
	return row, nil
}

// UpsertEmailChangeToken writes the pending-change token. The account
// carries at most one pending change, so a re-request replaces the hash,
// rebinds the payload to the newest address asked for, moves the window,
// and stamps the send time over the row it conflicts with.
func (r *Repository) UpsertEmailChangeToken(ctx context.Context, db datastore.Querier, userID uuid.UUID, tokenHash, payload string, expiresAt, sentAt time.Time) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(AuthTokenTable)
	ib.Cols("user_id", "token_hash", "purpose", "payload", "expires_at", "last_sent_at")
	ib.Values(userID, tokenHash, PurposeEmailChange, payload, expiresAt, sentAt)
	// The conflict target carries the partial index's predicate: the unique
	// index excludes reauthentication, so a bare column list matches nothing.
	ib.SQL("ON CONFLICT (user_id, purpose) WHERE purpose <> 'reauthentication' DO UPDATE SET " +
		"token_hash = EXCLUDED.token_hash, " +
		"payload = EXCLUDED.payload, " +
		"expires_at = EXCLUDED.expires_at, " +
		"last_sent_at = EXCLUDED.last_sent_at")

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("verification: upsert email change token: %w", err)
	}
	return nil
}

// FindEmailChangeTokenByHash reads the pending-change row a raw value hashes
// to. The raw value is never stored: only the caller's hash reaches this
// query.
func (r *Repository) FindEmailChangeTokenByHash(ctx context.Context, db datastore.Querier, tokenHash string) (EmailChangeToken, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "payload", "expires_at", "last_sent_at")
	sb.From(AuthTokenTable)
	sb.Where(
		sb.Equal("token_hash", tokenHash),
		sb.Equal("purpose", PurposeEmailChange),
	)

	query, args := sb.Build()
	var row EmailChangeToken
	err := db.QueryRow(ctx, query, args...).Scan(&row.ID, &row.UserID, &row.Payload, &row.ExpiresAt, &row.LastSentAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return EmailChangeToken{}, datastore.ErrNoRows
	}
	if err != nil {
		return EmailChangeToken{}, fmt.Errorf("verification: find email change token: %w", err)
	}
	return row, nil
}

// FindEmailChangeTokenByUser reads the pending-change row an account
// carries. The request cooldown reads the send time it stamps.
func (r *Repository) FindEmailChangeTokenByUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) (EmailChangeToken, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "payload", "expires_at", "last_sent_at")
	sb.From(AuthTokenTable)
	sb.Where(
		sb.Equal("user_id", userID),
		sb.Equal("purpose", PurposeEmailChange),
	)

	query, args := sb.Build()
	var row EmailChangeToken
	err := db.QueryRow(ctx, query, args...).Scan(&row.ID, &row.UserID, &row.Payload, &row.ExpiresAt, &row.LastSentAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return EmailChangeToken{}, datastore.ErrNoRows
	}
	if err != nil {
		return EmailChangeToken{}, fmt.Errorf("verification: find email change token by user: %w", err)
	}
	return row, nil
}

// FindUserByEmail reads the account an address is on record for, whatever
// its username. The uniqueness judgement the request and the confirmation
// both run reads it.
func (r *Repository) FindUserByEmail(ctx context.Context, db datastore.Querier, email string) (Account, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "username", "email", "display_name", "email_verified_at")
	sb.From("public.users")
	sb.Where(sb.Equal("email", email))

	query, args := sb.Build()
	var row Account
	err := db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.Username, &row.Email, &row.DisplayName, &row.EmailVerifiedAt,
	)
	if errors.Is(err, datastore.ErrNoRows) {
		return Account{}, datastore.ErrNoRows
	}
	if err != nil {
		return Account{}, fmt.Errorf("verification: find user by email: %w", err)
	}
	return row, nil
}

// SetEmail moves the account's address and stamps it verified in one write:
// the confirmation's token proved control of the new address, so the proof
// the old address carried moves with the account and the fresh one applies.
func (r *Repository) SetEmail(ctx context.Context, db datastore.Querier, userID uuid.UUID, email string, at time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update("public.users")
	ub.Set(
		ub.Assign("email", email),
		ub.Assign("email_verified_at", at),
	)
	ub.Where(ub.Equal("id", userID))

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("verification: set email: %w", err)
	}
	return nil
}
