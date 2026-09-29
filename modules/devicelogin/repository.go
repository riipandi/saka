package devicelogin

import (
	"context"
	"errors"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/tango/internal/datastore"
)

// Repository reads and writes the pairing rows. Every single-use state
// transition — the approval, the denial, the consumption — lives in the
// UPDATE's WHERE, so a concurrent exchange or a second decision answers
// the row it found and never rewrites a decided one.
type Repository struct {
	pool *datastore.Postgres
}

func NewRepository(pool *datastore.Postgres) *Repository {
	return &Repository{pool: pool}
}

// Create inserts one pending row. The caller has already hashed the
// user code and the device token; the raw values never reach the
// database.
func (r *Repository) Create(ctx context.Context, userCodeHash, deviceTokenHash, ipAddress, userAgent string, expiresAt time.Time) (Request, error) {
	return r.insert(ctx, r.pool, userCodeHash, deviceTokenHash, ipAddress, userAgent, expiresAt)
}

// CreateWithinLimit inserts one pending row under the pairing cap. The
// advisory lock on the device token's hash serializes one browser's
// creations, and the count decides inside the lock — a bare
// count-then-insert would race past the cap with concurrent creates.
// The transaction owns the lock, so a refused or failed create releases
// it for the next attempt.
func (r *Repository) CreateWithinLimit(ctx context.Context, userCodeHash, deviceTokenHash, ipAddress, userAgent string, expiresAt time.Time, limit int) (Request, error) {
	var req Request
	err := r.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, deviceTokenHash); err != nil {
			return err
		}
		live, err := r.countLiveForToken(ctx, tx, deviceTokenHash)
		if err != nil {
			return err
		}
		if live >= limit {
			return ErrTooManyPendingRequests
		}
		req, err = r.insert(ctx, tx, userCodeHash, deviceTokenHash, ipAddress, userAgent, expiresAt)
		return err
	})
	if err != nil {
		return Request{}, err
	}
	return req, nil
}

// insert writes one row on the query surface it is handed, so a
// caller's transaction carries it.
func (r *Repository) insert(ctx context.Context, db datastore.Querier, userCodeHash, deviceTokenHash, ipAddress, userAgent string, expiresAt time.Time) (Request, error) {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(requestTable)
	sb.Cols("user_code_hash", "device_token_hash", "ip_address", "user_agent", "expires_at")
	sb.Values(userCodeHash, deviceTokenHash, ipAddress, userAgent, expiresAt)
	sb.SQL("RETURNING id, created_at")
	query, args := sb.Build()

	var req Request
	err := db.QueryRow(ctx, query, args...).Scan(&req.ID, &req.CreatedAt)
	if err != nil {
		return Request{}, err
	}
	req.Status = StatusPending
	req.IPAddress = ipAddress
	req.UserAgent = userAgent
	req.ExpiresAt = expiresAt
	return req, nil
}

// scanRequest renders one row onto the view. The hash columns stay
// behind — the callers carry the presented values themselves.
func scanRequest(row pgx.Row) (Request, error) {
	var (
		req          Request
		userCodeHash string
		tokenHash    string
	)
	err := row.Scan(&req.ID, &userCodeHash, &tokenHash, &req.UserID, &req.Status,
		&req.IPAddress, &req.UserAgent, &req.CreatedAt, &req.ExpiresAt, &req.DecidedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, datastore.ErrNoRows
	}
	if err != nil {
		return Request{}, err
	}
	return req, nil
}

const requestColumns = `id, user_code_hash, device_token_hash, user_id, status,
	ip_address, user_agent, created_at, expires_at, decided_at`

// ByUserCode reads one live request by the presented user code. An
// unknown, expired, or consumed row is the same not-found — the code is
// the credential, and a dead one answers nothing.
func (r *Repository) ByUserCode(ctx context.Context, userCodeHash string) (Request, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(requestColumns)
	sb.From(requestTable)
	sb.Where(sb.Equal("user_code_hash", userCodeHash), sb.GreaterThan("expires_at", time.Now().UTC()))
	query, args := sb.Build()
	return scanRequest(r.pool.QueryRow(ctx, query, args...))
}

// ByID reads one live request by its identifier — the handle the
// creating browser's cookie carries to the exchange.
func (r *Repository) ByID(ctx context.Context, id string) (Request, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(requestColumns)
	sb.From(requestTable)
	sb.Where(sb.Equal("id", id), sb.GreaterThan("expires_at", time.Now().UTC()))
	query, args := sb.Build()
	return scanRequest(r.pool.QueryRow(ctx, query, args...))
}

// Decide stamps the approval or the denial. The WHERE holds `pending`:
// a request decided once is decided forever, and a second decision —
// the other device, the double click — answers zero rows.
func (r *Repository) Decide(ctx context.Context, userCodeHash string, userID *string, decision Decision) (bool, error) {
	now := time.Now().UTC()
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(requestTable)
	// One Set call: a second replaces the clause rather than joining it.
	if decision == DecisionApprove {
		sb.Set(sb.Assign("status", string(statusFor(decision))), sb.Assign("decided_at", now), sb.Assign("user_id", *userID))
	} else {
		sb.Set(sb.Assign("status", string(statusFor(decision))), sb.Assign("decided_at", now))
	}
	sb.Where(sb.Equal("user_code_hash", userCodeHash), sb.Equal("status", string(StatusPending)))
	query, args := sb.Build()
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func statusFor(decision Decision) RequestStatus {
	if decision == DecisionApprove {
		return StatusApproved
	}
	return StatusDenied
}

// Consume marks an approved request spent. The WHERE holds `approved`,
// so of two concurrent exchanges exactly one wins and the loser sees
// zero rows — the code is single-use.
func (r *Repository) Consume(ctx context.Context, id string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(requestTable)
	sb.Set(sb.Assign("status", string(StatusConsumed)))
	sb.Where(sb.Equal("id", id), sb.Equal("status", string(StatusApproved)))
	query, args := sb.Build()
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// CountLiveForToken answers how many unexpired requests the creating
// browser already holds — the cap that keeps one page from filling the
// table with rows nothing will ever decide. The query runs on the
// surface it is handed, so the cap's transaction reads its own lock's
// view.
func (r *Repository) CountLiveForToken(ctx context.Context, db datastore.Querier, deviceTokenHash string) (int, error) {
	return r.countLiveForToken(ctx, db, deviceTokenHash)
}

func (r *Repository) countLiveForToken(ctx context.Context, db datastore.Querier, deviceTokenHash string) (int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(requestTable)
	sb.Where(sb.Equal("device_token_hash", deviceTokenHash), sb.GreaterThan("expires_at", time.Now().UTC()))
	query, args := sb.Build()
	var count int
	err := db.QueryRow(ctx, query, args...).Scan(&count)
	return count, err
}

// PollState is what the exchange's long poll reads: the row's present
// state, with the account the approval named when it arrived.
type PollState struct {
	Status RequestStatus
	UserID *string
}

// PollStateByID reads only the columns the long poll spins on, keyed by
// the device token's hash — a stranger holding the request id alone
// reads nothing the pairing proves.
func (r *Repository) PollStateByID(ctx context.Context, id, deviceTokenHash string) (PollState, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("status", "user_id")
	sb.From(requestTable)
	sb.Where(sb.Equal("id", id), sb.Equal("device_token_hash", deviceTokenHash),
		sb.GreaterThan("expires_at", time.Now().UTC()))
	query, args := sb.Build()
	var state PollState
	err := r.pool.QueryRow(ctx, query, args...).Scan(&state.Status, &state.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PollState{}, datastore.ErrNoRows
	}
	if err != nil {
		return PollState{}, err
	}
	return state, nil
}
