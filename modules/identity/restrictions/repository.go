package restrictions

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
)

// Repository reads and writes the restriction rows. Every method takes the
// query surface, so the service passes either the pool or the transaction it
// runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// ExpireLifted stamps the end of the account's expired restriction rows —
// the row's own expiry instant is the lift the history keeps. It answers
// whether anything lifted, so the caller zeroes the streak the expiry ends
// and reads no further.
func (r *Repository) ExpireLifted(ctx context.Context, db datastore.Querier, userID uuid.UUID, now time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableAccountRestrictions)
	ub.Set("lifted_at = expires_at")
	ub.Where(ub.Equal("user_id", userID), ub.IsNull("lifted_at"),
		ub.IsNotNull("expires_at"), ub.LTE("expires_at", now))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("restrictions: lift expired: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ActiveRow answers the account's active restriction row — no expiry lift,
// no counter: the reads that ask pure questions read the pure row.
func (r *Repository) ActiveRow(ctx context.Context, db datastore.Querier, userID uuid.UUID, now time.Time) (*Schema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "kind", "reason", "started_at", "expires_at", "lifted_at", "lifted_by")
	sb.From(entity.TableAccountRestrictions)
	sb.Where(sb.Equal("user_id", userID), sb.IsNull("lifted_at"),
		sb.Or(sb.IsNull("expires_at"), sb.GT("expires_at", now)))
	sb.OrderBy("started_at DESC")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("restrictions: read active: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, fmt.Errorf("restrictions: read active: %w", err)
		}
		return nil, nil
	}
	var row Schema
	if err := rows.Scan(&row.ID, &row.UserID, &row.Kind, &row.Reason, &row.StartedAt, &row.ExpiresAt, &row.LiftedAt, &row.LiftedBy); err != nil {
		return nil, fmt.Errorf("restrictions: read active: %w", err)
	}
	return &row, nil
}

// ApplyBan writes the ban the terms name: the open row's reason and expiry
// are replaced — a re-ban is the caller's intent, not a comparison — and an
// account with none open earns a new row whose start is now. The start
// instant answers "since when", so the replace keeps it.
func (r *Repository) ApplyBan(ctx context.Context, db datastore.Querier, userID uuid.UUID, reason string, expiresAt *time.Time, now time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableAccountRestrictions)
	ub.Set(ub.Assign("reason", reason), ub.Assign("expires_at", expiresAt))
	ub.Where(ub.Equal("user_id", userID), ub.Equal("kind", KindBan), ub.IsNull("lifted_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("restrictions: apply ban: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableAccountRestrictions)
	ib.Cols("user_id", "kind", "reason", "started_at", "expires_at")
	ib.Values(userID, KindBan, reason, now, expiresAt)

	query, args = ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("restrictions: apply ban: %w", err)
	}
	return nil
}

// LiftBans stamps the end of the account's open ban rows. Lifting an
// account that is not banned is the caller's no-op success, so a lift that
// changed nothing answers false, not an error.
func (r *Repository) LiftBans(ctx context.Context, db datastore.Querier, userID uuid.UUID, liftedBy *uuid.UUID, now time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableAccountRestrictions)
	ub.Set(ub.Assign("lifted_at", now), ub.Assign("lifted_by", liftedBy))
	ub.Where(ub.Equal("user_id", userID), ub.Equal("kind", KindBan), ub.IsNull("lifted_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("restrictions: lift %s: %w", KindBan, err)
	}
	return tag.RowsAffected() > 0, nil
}

// LiftLockouts stamps the end of the account's open lockout rows — the
// admin's unlock or the policy's own expiry read.
func (r *Repository) LiftLockouts(ctx context.Context, db datastore.Querier, userID uuid.UUID, liftedBy *uuid.UUID, now time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableAccountRestrictions)
	ub.Set(ub.Assign("lifted_at", now), ub.Assign("lifted_by", liftedBy))
	ub.Where(ub.Equal("user_id", userID), ub.Equal("kind", KindLockout), ub.IsNull("lifted_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("restrictions: lift %s: %w", KindLockout, err)
	}
	return tag.RowsAffected() > 0, nil
}

// ApplyLockout writes the automated restriction the failed-attempt policy
// lands. The expiry is the policy duration's answer — NULL when the
// deployment locked without an end.
func (r *Repository) ApplyLockout(ctx context.Context, db datastore.Querier, userID uuid.UUID, expiresAt *time.Time, now time.Time) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableAccountRestrictions)
	ib.Cols("user_id", "kind", "reason", "started_at", "expires_at")
	ib.Values(userID, KindLockout, nil, now, expiresAt)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("restrictions: apply lockout: %w", err)
	}
	return nil
}

// BumpFailures advances the account's failed-password streak in place and
// answers the new count. The in-place increment is the race safety two
// concurrent sign-ins need: both count, neither overwrites. The counter is
// the lockout's own state — the streak is its story.
func (r *Repository) BumpFailures(ctx context.Context, db datastore.Querier, userID uuid.UUID) (int, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableUsers)
	ub.Set(ub.Incr("failed_attempts"))
	ub.Where(ub.Equal("id", userID))
	ub.Returning("failed_attempts")

	query, args := ub.Build()
	var attempts int
	if err := db.QueryRow(ctx, query, args...).Scan(&attempts); err != nil {
		if errors.Is(err, datastore.ErrNoRows) {
			return 0, datastore.ErrNoRows
		}
		return 0, fmt.Errorf("restrictions: bump failures: %w", err)
	}
	return attempts, nil
}

// ZeroFailures clears the account's streak — on a successful verification,
// on the lockout's lift, and on its expiry. The counter is never a
// lifetime tally.
func (r *Repository) ZeroFailures(ctx context.Context, db datastore.Querier, userID uuid.UUID) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableUsers)
	ub.Set(ub.Assign("failed_attempts", 0))
	ub.Where(ub.Equal("id", userID), ub.NotEqual("failed_attempts", 0))

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("restrictions: zero failures: %w", err)
	}
	return nil
}
