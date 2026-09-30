package onetimeaccess

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/user"

	"uuid"
)

// Repository reads and writes the one-time access codes. Every method takes
// the Querier to run on, so a caller inside a transaction passes its tx — the
// consume and the session it opens are one fact — and one that holds none
// passes the pool.
type Repository struct{}

// NewRepository builds the repository.
func NewRepository() *Repository { return &Repository{} }

// accountColumns are the columns the account reads name, in scan order.
var accountColumns = []string{
	"id", "username", "email", "display_name",
	"disabled", "banned_at", "ban_expires",
}

// scanAccount reads one account row.
func scanAccount(scan func(dest ...any) error) (Account, error) {
	var account Account
	err := scan(&account.ID, &account.Username, &account.Email, &account.DisplayName,
		&account.Disabled, &account.BannedAt, &account.BanExpires)
	if err != nil {
		return Account{}, err
	}
	return account, nil
}

// FindAccountByID reads the account an issued code names.
func (r *Repository) FindAccountByID(ctx context.Context, db datastore.Querier, id uuid.UUID) (Account, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(accountColumns...)
	sb.From(user.UserTable)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	account, err := scanAccount(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return Account{}, datastore.ErrNoRows
	}
	if err != nil {
		return Account{}, fmt.Errorf("onetimeaccess: find account by id: %w", err)
	}
	return account, nil
}

// FindAccountByEmail reads the account an address names, matched exactly the
// way its unique index does. The public email request reads it to decide
// whether a code has anywhere to go; the address it answers with is never
// disclosed to the caller either way.
func (r *Repository) FindAccountByEmail(ctx context.Context, db datastore.Querier, email string) (Account, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(accountColumns...)
	sb.From(user.UserTable)
	sb.Where(sb.Equal("email", email))

	query, args := sb.Build()
	account, err := scanAccount(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return Account{}, datastore.ErrNoRows
	}
	if err != nil {
		return Account{}, fmt.Errorf("onetimeaccess: find account by email: %w", err)
	}
	return account, nil
}

// UpsertToken records the code an account may sign in with. The account's
// older code is replaced, not stacked: the unique index on (user_id, purpose)
// is what keeps an account to one code, and the upsert is what makes a
// re-request a re-issue rather than a conflict. The device token travels with
// the code when the email path issued one, and stays unset otherwise.
func (r *Repository) UpsertToken(ctx context.Context, db datastore.Querier, userID uuid.UUID, tokenHash string, deviceToken *string, expiresAt, sentAt time.Time) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(tokenTable)
	ib.Cols("user_id", "token_hash", "device_token", "purpose", "expires_at", "last_sent_at")
	ib.Values(userID, tokenHash, deviceToken, PurposeOneTimeAccess, expiresAt, sentAt)
	// The conflict target carries the partial index's predicate: the unique
	// index excludes reauthentication, so a bare column list matches nothing.
	ib.SQL("ON CONFLICT (user_id, purpose) WHERE purpose <> 'reauthentication' DO UPDATE SET " +
		"token_hash = EXCLUDED.token_hash, " +
		"device_token = EXCLUDED.device_token, " +
		"expires_at = EXCLUDED.expires_at, " +
		"last_sent_at = EXCLUDED.last_sent_at")

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("onetimeaccess: upsert token: %w", err)
	}
	return nil
}

// FindTokenByHash reads the code row a raw value hashes to. The raw value is
// never stored: only the caller's hash reaches this query.
func (r *Repository) FindTokenByHash(ctx context.Context, db datastore.Querier, tokenHash string) (OneTimeToken, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "expires_at", "device_token", "last_sent_at")
	sb.From(tokenTable)
	sb.Where(
		sb.Equal("token_hash", tokenHash),
		sb.Equal("purpose", PurposeOneTimeAccess),
	)

	query, args := sb.Build()
	var row OneTimeToken
	err := db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.UserID, &row.ExpiresAt, &row.DeviceToken, &row.LastSentAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return OneTimeToken{}, datastore.ErrNoRows
	}
	if err != nil {
		return OneTimeToken{}, fmt.Errorf("onetimeaccess: find token: %w", err)
	}
	return row, nil
}

// FindTokenByUser reads the code row an account carries, whatever value it
// hashes — the upsert keeps an account to one row per purpose, so the
// holder alone names it. The resend cooldown reads the send time it stamps.
func (r *Repository) FindTokenByUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) (OneTimeToken, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "expires_at", "device_token", "last_sent_at")
	sb.From(tokenTable)
	sb.Where(
		sb.Equal("user_id", userID),
		sb.Equal("purpose", PurposeOneTimeAccess),
	)

	query, args := sb.Build()
	var row OneTimeToken
	err := db.QueryRow(ctx, query, args...).Scan(
		&row.ID, &row.UserID, &row.ExpiresAt, &row.DeviceToken, &row.LastSentAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return OneTimeToken{}, datastore.ErrNoRows
	}
	if err != nil {
		return OneTimeToken{}, fmt.Errorf("onetimeaccess: find token by user: %w", err)
	}
	return row, nil
}

// DeleteToken consumes a code row: the delete is the spend, and the count it
// reports is what a second caller loses the race with. The caller checks the
// expiry and the device token before it deletes, so the delete's guard is the
// last word on whether the code was still there to spend.
// RegisterWrongAttempt spends one wrong-device slot on the token. The
// UPDATE's WHERE carries the budget, so two racing mismatches cannot both
// fit in the last slot; it answers whether the token still has attempts
// left.
func (r *Repository) RegisterWrongAttempt(ctx context.Context, db datastore.Querier, id uuid.UUID, max int) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(tokenTable)
	ub.Set(ub.Add("wrong_attempts", 1))
	ub.Where(ub.Equal("id", id), ub.LT("wrong_attempts", max))
	ub.Returning("wrong_attempts")

	query, args := ub.Build()
	var attempts int
	err := db.QueryRow(ctx, query, args...).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		// The budget was already spent: the token is dead, and the caller
		// ends it.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("onetimeaccess: register wrong attempt: %w", err)
	}
	return attempts < max, nil
}

// DeleteToken consumes a code row: the delete is the spend, and the count it
// reports is what a second caller loses the race with. The caller checks the
// expiry and the device token before it deletes, so the delete's guard is the
// last word on whether the code was still there to spend. The WHERE carries
// the hash the caller resolved, so a re-issue that replaced the row cannot be
// consumed by the stale read that named it.
func (r *Repository) DeleteToken(ctx context.Context, db datastore.Querier, id uuid.UUID, tokenHash, purpose string) (int64, error) { // pgconn's CommandTag counts the rows the statement touched; a second
	// caller that lost the race sees zero, which is the code already spent.
	db2 := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db2.DeleteFrom(tokenTable)
	db2.Where(db2.Equal("id", id), db2.Equal("token_hash", tokenHash), db2.Equal("purpose", purpose))

	query, args := db2.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("onetimeaccess: delete token: %w", err)
	}
	return int64(tag.RowsAffected()), nil
}
