package customclaim

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"
	"go.jetify.com/typeid"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
)

// Repository reads and writes the claim rows the procedures manage. Every
// method takes the query surface, so the service passes either the pool or
// the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository.
func NewRepository() *Repository {
	return &Repository{}
}

// claimColumns are the columns the claim procedures read, in scan order.
var claimColumns = []string{"id", "key", "value", "user_id", "user_group_id", "created_at"}

// scanClaim reads one row into the schema.
func scanClaim(scan func(dest ...any) error) (ClaimSchema, error) {
	var row ClaimSchema
	err := scan(&row.ID, &row.Key, &row.Value, &row.UserID, &row.GroupID, &row.CreatedAt)
	if err != nil {
		return ClaimSchema{}, err
	}
	return row, nil
}

// GroupClaims answers the claims the named groups carry, merged in one
// read. The group ids are the UUIDs the caller's directory resolved.
func (r *Repository) ListByGroups(ctx context.Context, db datastore.Querier, groupIDs []uuid.UUID) ([]ClaimSchema, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(claimColumns...)
	sb.From(entity.TableCustomClaims)
	sb.Where(sb.In("user_group_id", toList(groupIDs)...))
	sb.OrderBy("key")
	return r.list(ctx, db, sb, "customclaim: list group claims")
}

// toList spreads the ids for the builder's IN clause.
func toList(ids []uuid.UUID) []any {
	values := make([]any, len(ids))
	for i, id := range ids {
		values[i] = id
	}
	return values
}

// Suggest answers the claim keys already in use, ordered by how often they
// carry — the autocomplete list a claim form offers.
func (r *Repository) Suggest(ctx context.Context, db datastore.Querier) ([]SuggestedKey, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key", "count(*) AS usage_count")
	sb.From(entity.TableCustomClaims)
	sb.GroupBy("key")
	sb.OrderBy("usage_count DESC", "key")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("customclaim: suggest: %w", err)
	}
	defer rows.Close()

	keys := []SuggestedKey{}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, fmt.Errorf("customclaim: suggest: %w", err)
		}
		keys = append(keys, SuggestedKey{Key: key, UsageCount: int64(count)})
	}
	return keys, rows.Err()
}

// ListByUser answers one account's claims, ordered by key. The account view
// reads the same rows through the token-issuance seam.
func (r *Repository) ListByUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]ClaimSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(claimColumns...)
	sb.From(entity.TableCustomClaims)
	sb.Where(sb.Equal("user_id", userID))
	sb.OrderBy("key")
	return r.list(ctx, db, sb, "customclaim: list user claims")
}

// ListByGroup answers one group's claims, ordered by key.
func (r *Repository) ListByGroup(ctx context.Context, db datastore.Querier, groupID uuid.UUID) ([]ClaimSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(claimColumns...)
	sb.From(entity.TableCustomClaims)
	sb.Where(sb.Equal("user_group_id", groupID))
	sb.OrderBy("key")
	return r.list(ctx, db, sb, "customclaim: list group claims")
}

// list runs a built list query and scans the rows it answers.
func (r *Repository) list(ctx context.Context, db datastore.Querier, sb *sqlbuilder.SelectBuilder, cause string) ([]ClaimSchema, error) {
	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cause, err)
	}
	defer rows.Close()

	claims := []ClaimSchema{}
	for rows.Next() {
		row, err := scanClaim(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cause, err)
		}
		claims = append(claims, row)
	}
	return claims, rows.Err()
}

// CreateUser stores one claim of an account, answering its identifier. The
// unique index on (key, user_id, user_group_id) is the storage of the
// per-subject key rule, and the foreign keys turn an unknown subject into
// the refusal the service maps.
func (r *Repository) CreateUser(ctx context.Context, db datastore.Querier, userID uuid.UUID, key, value string, now time.Time) (ClaimSchema, error) {
	return r.insert(ctx, db, nil, &userID, key, value, now)
}

// CreateGroup stores one claim of a group.
func (r *Repository) CreateGroup(ctx context.Context, db datastore.Querier, groupID uuid.UUID, key, value string, now time.Time) (ClaimSchema, error) {
	return r.insert(ctx, db, &groupID, nil, key, value, now)
}

// insert stores the row and answers it. The identifier is generated in Go —
// the wire form and the column's UUID come from the same TypeID — and the
// created row is read back by the service inside the same transaction, so
// the answer carries what the transaction will commit.
func (r *Repository) insert(ctx context.Context, db datastore.Querier, groupID, userID *uuid.UUID, key, value string, now time.Time) (ClaimSchema, error) {
	wire, err := typeid.New[ClaimID]()
	if err != nil {
		return ClaimSchema{}, fmt.Errorf("customclaim: id: %w", err)
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableCustomClaims)
	ib.Cols("id", "key", "value", "user_id", "user_group_id")
	ib.Values(wire.UUID(), key, value, userID, groupID)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return ClaimSchema{}, fmt.Errorf("customclaim: create: %w", err)
	}
	return ClaimSchema{
		ID:        IDToUUID(wire),
		Key:       key,
		Value:     value,
		UserID:    userID,
		GroupID:   groupID,
		CreatedAt: now,
	}, nil
}

// UpdateClaim rewrites one claim's key and value. The WHERE repeats the
// identifier, so a claim deleted between the read and the write answers
// unchanged.
func (r *Repository) UpdateClaim(ctx context.Context, db datastore.Querier, id uuid.UUID, key, value string) (ClaimSchema, bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableCustomClaims)
	ub.Set(ub.Assign("key", key), ub.Assign("value", value))
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return ClaimSchema{}, false, fmt.Errorf("customclaim: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ClaimSchema{}, false, nil
	}

	row, err := r.GetClaim(ctx, db, id)
	return row, true, err
}

// DeleteClaim removes one claim. The WHERE repeats the identifier, so a
// second deletion answers unchanged — the state it names is the one the
// row is in.
func (r *Repository) DeleteClaim(ctx context.Context, db datastore.Querier, id uuid.UUID) (bool, error) {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(entity.TableCustomClaims)
	dbb.Where(dbb.Equal("id", id))

	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("customclaim: delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// GetClaim reads one claim by its identifier. An identifier that names no
// claim is the caller's not-found failure.
func (r *Repository) GetClaim(ctx context.Context, db datastore.Querier, id uuid.UUID) (ClaimSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(claimColumns...)
	sb.From(entity.TableCustomClaims)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanClaim(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return ClaimSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return ClaimSchema{}, fmt.Errorf("customclaim: get: %w", err)
	}
	return row, nil
}

// errUniqueViolation reports whether the write failed on a unique index, the
// way the (key, user_id, user_group_id) index answers a duplicate.
func errUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// errForeignKeyViolation reports whether the write failed on a foreign key,
// the way the subject columns answer an unknown account or group.
func errForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// parseClaimID turns the request's identifier into the key the rows carry.
// A malformed identifier names no claim, so it is the not-found failure the
// same as an unknown one.
func parseClaimID(wire string) (uuid.UUID, error) {
	id, err := ParseID(wire)
	if err != nil {
		return uuid.Nil(), ErrClaimNotFound
	}
	return IDToUUID(id), nil
}
