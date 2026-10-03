package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"go.jetify.com/typeid"
	"uuid"

	"github.com/riipandi/saka/internal/datastore"
)

// Repository reads and writes the session rows the lifecycle procedures
// manage. Every method takes the query surface, so the service passes either
// the pool or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// sessionColumns are the columns the session procedures read, in scan order.
var sessionColumns = []string{
	"id", "user_id", "provider", "token_hash", "user_agent", "ip_address",
	"remember", "created_at", "expires_at", "refreshed_at", "revoked_at",
	// The ender reads through its text form: the column is a nullable UUID,
	// and a nil is the empty string the scan carries rather than a type the
	// scanner has no plan for.
	"COALESCE(revoked_by::text, '')",
	"COALESCE(impersonated_by::text, '')",
	// The hash the last rotation replaced, read through its text form the
	// same way: a lookup that lands on it is a replayed token.
	"COALESCE(rotated_token_hash::text, '')",
}

// scanSession reads one row into the schema. The identifier arrives as the
// UUID the column stores and leaves as the typed id the callers hold; the
// typed id carries the bytes, so nothing parses the text twice.
func scanSession(scan func(dest ...any) error) (SessionSchema, error) {
	var row SessionSchema
	var rawID, rawEnder, rawActor string
	err := scan(
		&rawID, &row.UserID, &row.Provider, &row.TokenHash, &row.UserAgent, &row.IPAddress,
		&row.Remember, &row.CreatedAt, &row.ExpiresAt, &row.RefreshedAt, &row.RevokedAt,
		&rawEnder, &rawActor, &row.RotatedTokenHash,
	)
	if err != nil {
		return SessionSchema{}, err
	}
	if rawEnder != "" {
		ender, parseErr := uuid.Parse(rawEnder)
		if parseErr != nil {
			return SessionSchema{}, fmt.Errorf("session: revoked_by: %w", parseErr)
		}
		row.RevokedBy = &ender
	}
	if rawActor != "" {
		actor, parseErr := uuid.Parse(rawActor)
		if parseErr != nil {
			return SessionSchema{}, fmt.Errorf("session: impersonated_by: %w", parseErr)
		}
		row.ImpersonatedBy = &actor
	}
	parsed, err := typeid.FromUUID[SessionID](rawID)
	if err != nil {
		return SessionSchema{}, fmt.Errorf("session: id: %w", err)
	}
	row.ID = parsed
	return row, nil
}

// Create writes a new session row — the sign-in issuer's insert, owned here
// so every way a session opens (a password, a one-time code, a delegation)
// writes the same columns. The caller owns the transaction.
func (r *Repository) Create(ctx context.Context, db datastore.Querier, row SessionSchema) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(SessionTable)
	ib.Cols("id", "user_id", "provider", "token_hash", "user_agent", "device_fingerprint", "ip_address", "remember", "created_at", "expires_at", "impersonated_by")
	ib.Values(row.ID.UUID(), row.UserID, row.Provider, row.TokenHash, row.UserAgent, row.DeviceFingerprint, row.IPAddress, row.Remember, row.CreatedAt, row.ExpiresAt, row.ImpersonatedBy)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("session: create: %w", err)
	}
	return nil
}

// GetSession reads one session by its identifier.
func (r *Repository) GetSession(ctx context.Context, db datastore.Querier, id SessionID) (SessionSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(sessionColumns...)
	sb.From(SessionTable)
	sb.Where(sb.Equal("id", id.UUID()))

	query, args := sb.Build()
	row, err := scanSession(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return SessionSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return SessionSchema{}, fmt.Errorf("session: get: %w", err)
	}
	return row, nil
}

// FindActiveByTokenHash reads the session a presented refresh token resolves
// to. The WHERE clause is the whole gate: the hash must match, the session
// must be unrevoked and unexpired — a spent, ended, or expired token answers
// the same not-found, and the caller refuses it as one.
func (r *Repository) FindActiveByTokenHash(ctx context.Context, db datastore.Querier, hash string, now time.Time) (SessionSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(sessionColumns...)
	sb.From(SessionTable)
	sb.Where(sb.Equal("token_hash", hash), sb.IsNull("revoked_at"), sb.GT("expires_at", now))

	query, args := sb.Build()
	row, err := scanSession(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return SessionSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return SessionSchema{}, fmt.Errorf("session: find by token: %w", err)
	}
	return row, nil
}

// LockByRefreshHash locks the session row a presented refresh hash names —
// its current secret, or the one its last rotation replaced — and answers the
// row. The row lock is the serialization the renewal depends on: two
// in-flight renewals of one token queue here, and the second reads what the
// first committed rather than the snapshot it started from. A caller whose
// hash no longer equals the row's `token_hash` is presenting a spent
// credential, and that is the reuse the caller revokes.
func (r *Repository) LockByRefreshHash(ctx context.Context, db datastore.Querier, hash string) (SessionSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(sessionColumns...)
	sb.From(SessionTable)
	sb.Where(sb.Or(sb.Equal("token_hash", hash), sb.Equal("rotated_token_hash", hash)))
	sb.ForUpdate()

	query, args := sb.Build()
	row, err := scanSession(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return SessionSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return SessionSchema{}, fmt.Errorf("session: lock by refresh hash: %w", err)
	}
	return row, nil
}

// Revoke stamps the end of one session: who ended it, and when. The WHERE
// clause excludes an already-ended row, so a second revocation answers false
// — the state it names is the one the session is already in — and the
// service treats that as the success it is. The refresh token dies with the
// stamp; the access token does not, by the statelessness the protocol
// settles.
func (r *Repository) Revoke(ctx context.Context, db datastore.Querier, id SessionID, by *uuid.UUID, at time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(SessionTable)
	ub.Set(
		ub.Assign("revoked_at", at),
		// A system revocation names no ender: the NULL is the row's way of
		// saying the account did not end its own session.
		ub.Assign("revoked_by", by),
	)
	ub.Where(ub.Equal("id", id.UUID()), ub.IsNull("revoked_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("session: revoke: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Rotate replaces a live session's refresh token and window. The WHERE
// clause is the race: a session that was revoked between the read and this
// write answers false, and the renewal dies before it spent anything — the
// new secret is thrown away and the caller tries again. The hash the caller
// read is part of that guard, so a rotation that lost the row to a concurrent
// renewal matches nothing rather than overwriting the winner's secret.
func (r *Repository) Rotate(ctx context.Context, db datastore.Querier, id SessionID, hash, previousHash string, refreshedAt, expiresAt time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(SessionTable)
	ub.Set(
		ub.Assign("token_hash", hash),
		// The hash being replaced is kept beside the row: a caller who
		// presents it again is replaying a spent token, and the lookup that
		// lands on this column is the reuse signal.
		ub.Assign("rotated_token_hash", previousHash),
		ub.Assign("refreshed_at", refreshedAt),
		ub.Assign("expires_at", expiresAt),
	)
	ub.Where(ub.Equal("id", id.UUID()), ub.Equal("token_hash", previousHash), ub.IsNull("revoked_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("session: rotate: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RevokeLiveForUser stamps the end of every live session the account holds,
// except the one the caller asked to keep — a nil `keep` keeps nothing. The
// read takes row locks before the write, so a session a concurrent actor
// stamps between the two statements waits and then answers as already ended:
// every row the caller sees back is one this write stamped. The refresh
// token of each stamped row dies with it; the access tokens do not, by the
// statelessness the protocol settles.
func (r *Repository) RevokeLiveForUser(ctx context.Context, db datastore.Querier, userID uuid.UUID, keep *SessionID, by uuid.UUID, at time.Time) ([]SessionSchema, error) {
	lb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	lb.Select(sessionColumns...)
	lb.From(SessionTable)
	lb.Where(lb.Equal("user_id", userID), lb.IsNull("revoked_at"))
	if keep != nil {
		lb.Where(lb.NE("id", keep.UUID()))
	}
	lb.ForUpdate()

	lockQuery, lockArgs := lb.Build()
	rows, err := db.Query(ctx, lockQuery, lockArgs...)
	if err != nil {
		return nil, fmt.Errorf("session: bulk revoke: %w", err)
	}
	defer rows.Close()

	targets := []SessionSchema{}
	ids := []any{}
	for rows.Next() {
		row, scanErr := scanSession(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("session: bulk revoke: %w", scanErr)
		}
		targets = append(targets, row)
		ids = append(ids, row.ID.UUID())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: bulk revoke: %w", err)
	}
	if len(ids) == 0 {
		return targets, nil
	}

	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(SessionTable)
	ub.Set(
		ub.Assign("revoked_at", at),
		ub.Assign("revoked_by", by),
	)
	ub.Where(ub.In("id", ids...), ub.IsNull("revoked_at"))

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return nil, fmt.Errorf("session: bulk revoke: %w", err)
	}
	return targets, nil
}

// ListOwn answers one page of the account's sessions, newest first, ended
// ones included. An ended session stays listed: the stamp is the fact a
// holder reads, not something to hide.
func (r *Repository) ListOwn(ctx context.Context, db datastore.Querier, userID uuid.UUID, offset, limit int) ([]SessionSchema, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(sessionColumns...)
	sb.From(SessionTable)
	sb.Where(sb.Equal("user_id", userID))
	sb.OrderBy("created_at DESC", "id DESC")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("session: list: %w", err)
	}
	defer rows.Close()

	sessions := []SessionSchema{}
	for rows.Next() {
		row, scanErr := scanSession(rows.Scan)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("session: list: %w", scanErr)
		}
		sessions = append(sessions, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("session: list: %w", err)
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(SessionTable)
	cb.Where(cb.Equal("user_id", userID))
	query, args = cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("session: count: %w", err)
	}
	return sessions, total, nil
}
