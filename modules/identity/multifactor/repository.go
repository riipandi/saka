package multifactor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/riipandi/saka/internal/datastore"
)

// ErrNoRows re-exports the datastore's sentinel so callers answer one
// not-found across packages.
var ErrNoRows = datastore.ErrNoRows

// Repository is the multifactor tables' reader and writer. Every method takes
// the query surface as an argument, so a caller holding an open transaction
// keeps its writes atomic with whatever caused them.
type Repository struct{}

// NewRepository builds the repository.
func NewRepository() *Repository { return &Repository{} }

// ---- TOTP enrollments ----

// totpColumns is the select list one enrollment row answers, in the scan
// order scanTotp reads.
var totpColumns = []string{
	"id", "user_id", "name", "secret", "digits", "period", "algorithm",
	"confirmed_at", "last_used_step", "last_used_at", "created_at", "updated_at",
}

// scanTotp scans one enrollment row. The id arrives as text — the wire form —
// and leaves parsed.
func scanTotp(scan func(dest ...any) error) (TotpSchema, error) {
	var row TotpSchema
	var rawID string
	err := scan(&rawID, &row.UserID, &row.Name, &row.Secret, &row.Digits, &row.Period, &row.Algorithm,
		&row.ConfirmedAt, &row.LastUsedStep, &row.LastUsedAt, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return TotpSchema{}, err
	}
	parsed, parseErr := uuid.Parse(rawID)
	if parseErr != nil {
		return TotpSchema{}, fmt.Errorf("multifactor: id: %w", parseErr)
	}
	row.ID = parsed
	return row, nil
}

// CreateTotp writes an unconfirmed enrollment row.
func (r *Repository) CreateTotp(ctx context.Context, db datastore.Querier, row TotpSchema) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(TotpTable)
	ib.Cols("id", "user_id", "name", "secret", "digits", "period", "algorithm", "confirmed_at", "created_at", "updated_at")
	ib.Values(row.ID, row.UserID, row.Name, row.Secret, row.Digits, row.Period, row.Algorithm, row.ConfirmedAt, row.CreatedAt, row.UpdatedAt)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("multifactor: create totp: %w", err)
	}
	return nil
}

// GetTotp reads one enrollment by its identifier, whatever account holds it.
// The service scopes the read to the caller before answering.
func (r *Repository) GetTotp(ctx context.Context, db datastore.Querier, id uuid.UUID) (TotpSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(totpColumns...)
	sb.From(TotpTable)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanTotp(func(dest ...any) error { return db.QueryRow(ctx, query, args...).Scan(dest...) })
	if errors.Is(err, datastore.ErrNoRows) {
		return TotpSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return TotpSchema{}, fmt.Errorf("multifactor: get totp: %w", err)
	}
	return row, nil
}

// ListTotp reads every enrollment the account holds, confirmed and pending,
// oldest first — the order a settings page renders.
func (r *Repository) ListTotp(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]TotpSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(totpColumns...)
	sb.From(TotpTable)
	sb.Where(sb.Equal("user_id", userID))
	sb.OrderBy("created_at")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("multifactor: list totp: %w", err)
	}
	defer rows.Close()

	var out []TotpSchema
	for rows.Next() {
		row, scanErr := scanTotp(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("multifactor: list totp: %w", scanErr)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("multifactor: list totp: %w", err)
	}
	return out, nil
}

// CountConfirmedTotp answers how many confirmed authenticators the account
// holds — the number the challenge flow, the disable proof, and the delete
// proof all reason about.
func (r *Repository) CountConfirmedTotp(ctx context.Context, db datastore.Querier, userID uuid.UUID) (int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(TotpTable)
	sb.Where(sb.Equal("user_id", userID), sb.IsNotNull("confirmed_at"))

	query, args := sb.Build()
	var count int
	if err := db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("multifactor: count confirmed: %w", err)
	}
	return count, nil
}

// ConfirmTotp stamps the enrollment active. The WHERE clause refuses a row
// that is not the account's, so the service's ownership check and this write
// agree even if a caller skips it.
func (r *Repository) ConfirmTotp(ctx context.Context, db datastore.Querier, id, userID uuid.UUID, at time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(TotpTable)
	ub.Set(ub.Assign("confirmed_at", at), ub.Assign("updated_at", at))
	ub.Where(ub.Equal("id", id), ub.Equal("user_id", userID), ub.IsNull("confirmed_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("multifactor: confirm totp: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return datastore.ErrNoRows
	}
	return nil
}

// DeleteTotp removes one enrollment. The WHERE clause is the ownership check.
func (r *Repository) DeleteTotp(ctx context.Context, db datastore.Querier, id, userID uuid.UUID) error {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(TotpTable)
	dbb.Where(dbb.Equal("id", id), dbb.Equal("user_id", userID))

	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("multifactor: delete totp: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return datastore.ErrNoRows
	}
	return nil
}

// DeleteUnconfirmedTotpBefore purges enrollments whose ceremony never
// completed — the sweeper's delete, and the cap on how long an unconfirmed
// secret sits sealed in the table.
func (r *Repository) DeleteUnconfirmedTotpBefore(ctx context.Context, db datastore.Querier, cutoff time.Time) (int, error) {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(TotpTable)
	dbb.Where(dbb.IsNull("confirmed_at"), dbb.LT("created_at", cutoff))

	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("multifactor: purge unconfirmed: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// TouchTotpUsage records the step a challenge answered with, so a replayed
// code from the same time window refuses. The WHERE clause carries the step:
// the update affects a row only when the step is new, so two concurrent
// challenges with one code answer exactly one winner.
func (r *Repository) TouchTotpUsage(ctx context.Context, db datastore.Querier, id uuid.UUID, step int64, at time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(TotpTable)
	ub.Set(ub.Assign("last_used_step", step), ub.Assign("last_used_at", at), ub.Assign("updated_at", at))
	ub.Where(ub.Equal("id", id), ub.Or(ub.IsNull("last_used_step"), ub.LT("last_used_step", step)))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("multifactor: touch usage: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteAllTotpForUser removes every enrollment the account holds — disable's
// write, and the sweep a recovery flow may ask for.
func (r *Repository) DeleteAllTotpForUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) (int, error) {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(TotpTable)
	dbb.Where(dbb.Equal("user_id", userID))

	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("multifactor: delete all totp: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ---- Recovery codes ----

// ReplaceRecoveryCodes rewrites the account's set in one statement batch:
// the old rows die and the new ones land in the same transaction the caller
// holds, so a regenerate that rolls back keeps the old set.
func (r *Repository) ReplaceRecoveryCodes(ctx context.Context, db datastore.Querier, userID uuid.UUID, hashes []string, at time.Time) error {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(RecoveryTable)
	dbb.Where(dbb.Equal("user_id", userID))

	query, args := dbb.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("multifactor: clear recovery: %w", err)
	}

	for _, hash := range hashes {
		ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
		ib.InsertInto(RecoveryTable)
		ib.Cols("user_id", "code_hash", "created_at")
		ib.Values(userID, hash, at)

		insertQuery, insertArgs := ib.Build()
		if _, err := db.Exec(ctx, insertQuery, insertArgs...); err != nil {
			return fmt.Errorf("multifactor: write recovery: %w", err)
		}
	}
	return nil
}

// ConsumeRecoveryCode marks one unused code used and answers whether the
// mark landed. The WHERE clause is the single-use guarantee: two challenges
// racing one code answer exactly one winner, and a used code matches nothing.
func (r *Repository) ConsumeRecoveryCode(ctx context.Context, db datastore.Querier, userID uuid.UUID, hash string, at time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(RecoveryTable)
	ub.Set(ub.Assign("used_at", at))
	ub.Where(ub.Equal("user_id", userID), ub.Equal("code_hash", hash), ub.IsNull("used_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("multifactor: consume recovery: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// CountRecoveryCodes answers the whole set's size and how much of it is
// spent — the two numbers a settings page renders.
func (r *Repository) CountRecoveryCodes(ctx context.Context, db datastore.Querier, userID uuid.UUID) (total, used int, err error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)", "count(used_at)")
	sb.From(RecoveryTable)
	sb.Where(sb.Equal("user_id", userID))

	query, args := sb.Build()
	if err := db.QueryRow(ctx, query, args...).Scan(&total, &used); err != nil {
		return 0, 0, fmt.Errorf("multifactor: count recovery: %w", err)
	}
	return total, used, nil
}

// DeleteAllRecoveryForUser clears the account's set — disable's write.
func (r *Repository) DeleteAllRecoveryForUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) error {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(RecoveryTable)
	dbb.Where(dbb.Equal("user_id", userID))

	query, args := dbb.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("multifactor: delete recovery: %w", err)
	}
	return nil
}

// ---- The pending-auth bridge ----

// CreatePending writes one bridge row; the caller owns the transaction, so
// the pending row and whatever minted it commit together.
func (r *Repository) CreatePending(ctx context.Context, db datastore.Querier, row PendingSchema) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(PendingTable)
	ib.Cols("id", "user_id", "token_hash", "remember", "purpose", "expires_at", "created_at")
	ib.Values(row.ID, row.UserID, row.TokenHash, row.Remember, row.Purpose, row.ExpiresAt, row.CreatedAt)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("multifactor: create pending: %w", err)
	}
	return nil
}

// pendingColumns is the select list one bridge row answers.
var pendingColumns = []string{"id", "user_id", "token_hash", "remember", "purpose", "expires_at", "created_at", "wrong_attempts"}

// FindLivePending reads the bridge a presented pending token resolves to.
// The WHERE clause is the gate: hash match and unexpired — a spent, expired,
// or forged token answers the same not-found.
func (r *Repository) FindLivePending(ctx context.Context, db datastore.Querier, hash string, now time.Time) (PendingSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(pendingColumns...)
	sb.From(PendingTable)
	sb.Where(sb.Equal("token_hash", hash), sb.GT("expires_at", now))

	query, args := sb.Build()
	var row PendingSchema
	var rawID string
	scan := func(dest ...any) error { return db.QueryRow(ctx, query, args...).Scan(dest...) }
	if err := scan(&rawID, &row.UserID, &row.TokenHash, &row.Remember, &row.Purpose, &row.ExpiresAt, &row.CreatedAt, &row.WrongAttempts); err != nil {
		if errors.Is(err, datastore.ErrNoRows) {
			return PendingSchema{}, datastore.ErrNoRows
		}
		return PendingSchema{}, fmt.Errorf("multifactor: find pending: %w", err)
	}
	parsed, err := uuid.Parse(rawID)
	if err != nil {
		return PendingSchema{}, fmt.Errorf("multifactor: pending id: %w", err)
	}
	row.ID = parsed
	return row, nil
}

// DeletePending removes one bridge row — CompleteSignIn's consume, whatever
// way the exchange ended.
// DeletePending consumes the bridge a presented pending token names. The
// delete is the consumption: its WHERE carries the token hash, so two
// completions racing on one bridge cannot both spend it — the second answers
// false, and the session it was about to open never opens.
func (r *Repository) DeletePending(ctx context.Context, db datastore.Querier, id uuid.UUID, hash string) (bool, error) {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(PendingTable)
	dbb.Where(dbb.Equal("id", id), dbb.Equal("token_hash", hash))

	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("multifactor: delete pending: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RegisterWrongAttempt spends one wrong-code slot on the bridge. The UPDATE's
// WHERE carries the budget, so two racing wrong answers cannot both fit in
// the last slot; it answers whether that strike was the budget's last — the
// caller ends the bridge when it was.
func (r *Repository) RegisterWrongAttempt(ctx context.Context, db datastore.Querier, id uuid.UUID, max int) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(PendingTable)
	ub.Set(ub.Add("wrong_attempts", 1))
	ub.Where(ub.Equal("id", id), ub.LT("wrong_attempts", max))
	ub.Returning("wrong_attempts")

	query, args := ub.Build()
	var attempts int
	err := db.QueryRow(ctx, query, args...).Scan(&attempts)
	if errors.Is(err, datastore.ErrNoRows) {
		// Another racer took the slot: the bridge is on its way out either
		// way, so the strike reads as the last one.
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("multifactor: register wrong attempt: %w", err)
	}
	return attempts >= max, nil
}

// DeleteExpiredPending purges bridges past their life — the sweeper's delete.
func (r *Repository) DeleteExpiredPending(ctx context.Context, db datastore.Querier, now time.Time) (int, error) {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(PendingTable)
	dbb.Where(dbb.LT("expires_at", now))

	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("multifactor: purge pending: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
