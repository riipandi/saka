package apikey

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/modules/identity/user"
)

// Repository reads and writes the key rows the credential procedures manage.
// Every method takes the query surface, so the service passes either the pool
// or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// keyColumns are the columns the key procedures read, in scan order, spelled
// for the queries that name one table.
var keyColumns = []string{
	"id", "user_id", "name", "prefix", "key_hash", "description",
	"expires_at", "last_used_at", "created_at", "updated_at",
	"revoked_at", "expiration_email_sent_at",
}

// qualifiedKeyColumns is the same list spelled for a join, where an
// unqualified column name is ambiguous the moment both sides carry it — and
// both the key and the account carry an id.
func qualifiedKeyColumns(alias string) []string {
	qualified := make([]string, 0, len(keyColumns))
	for _, column := range keyColumns {
		qualified = append(qualified, alias+"."+column)
	}
	return qualified
}

// scanKey reads one row into the schema.
func scanKey(scan func(dest ...any) error) (KeySchema, error) {
	var row KeySchema
	err := scan(
		&row.ID, &row.UserID, &row.Name, &row.Prefix, &row.KeyHash, &row.Descr,
		&row.ExpiresAt, &row.LastUsed, &row.CreatedAt, &row.UpdatedAt,
		&row.RevokedAt, &row.EmailSent,
	)
	if err != nil {
		return KeySchema{}, err
	}
	return row, nil
}

// GetKey reads one key by its identifier. An identifier that names no key is
// the caller's not-found failure.
func (r *Repository) GetKey(ctx context.Context, db datastore.Querier, id uuid.UUID) (KeySchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(keyColumns...)
	sb.From(entity.TableAPIKeys)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanKey(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return KeySchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return KeySchema{}, fmt.Errorf("apikey: get: %w", err)
	}
	return row, nil
}

// ListKeys answers one page of the keys, newest first. An owner identifier
// scopes the page to that account's keys — the owner's own list — and an
// empty one names every key, the administrative view. A revoked key stays
// listed: the revocation is a stamp the view carries, not a deletion.
// keySortColumns is the whitelist a list's sort key resolves through. The
// names are the wire values the list requests validate against.
var keySortColumns = map[string]string{
	"name":         "lower(name)",
	"created_at":   "created_at",
	"expires_at":   "expires_at",
	"last_used_at": "last_used_at",
}

func (r *Repository) ListKeys(ctx context.Context, db datastore.Querier, owner uuid.UUID, sortBy string, ascending bool, offset, limit int) ([]KeySchema, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(keyColumns...)
	sb.From(entity.TableAPIKeys)
	if owner != uuid.Nil() {
		sb.Where(sb.Equal("user_id", owner))
	}
	sb.OrderBy(datastore.ListOrder(keySortColumns, sortBy, "created_at", ascending), "id")
	sb.Limit(limit).Offset(offset)

	keys, err := r.listRows(ctx, db, sb, "apikey: list")
	if err != nil {
		return nil, 0, err
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableAPIKeys)
	if owner != uuid.Nil() {
		cb.Where(cb.Equal("user_id", owner))
	}
	query, args := cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("apikey: count: %w", err)
	}
	return keys, total, nil
}

// listRows runs a built page query and scans the rows it answers.
func (r *Repository) listRows(ctx context.Context, db datastore.Querier, sb *sqlbuilder.SelectBuilder, cause string) ([]KeySchema, error) {
	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cause, err)
	}
	defer rows.Close()

	keys := []KeySchema{}
	for rows.Next() {
		row, err := scanKey(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cause, err)
		}
		keys = append(keys, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", cause, err)
	}
	return keys, nil
}

// CreateKey stores the key row and answers its identifier. The unique index
// on (name, owner) is the storage of the per-owner name rule, and the service
// reads the write's failure to answer a duplicate.
func (r *Repository) CreateKey(ctx context.Context, db datastore.Querier, row KeySchema) (uuid.UUID, error) {
	row.ID = uuid.NewV7()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableAPIKeys)
	ib.Cols("id", "user_id", "name", "prefix", "key_hash", "description", "expires_at")
	ib.Values(
		row.ID, row.UserID, row.Name, row.Prefix, row.KeyHash, row.Descr, row.ExpiresAt,
	)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return uuid.Nil(), err
	}
	return row.ID, nil
}

// RenewKey replaces an expired key's hash and window, and clears the expiry
// reminder — the new window earns a fresh reminder, not the old one's stamp.
// The WHERE repeats the read: this owner, still unrevoked, already expired.
// A concurrent revoke or a second renew matches nothing, so the caller is
// not handed a secret the row no longer stores.
func (r *Repository) RenewKey(ctx context.Context, db datastore.Querier, id, owner uuid.UUID, hash []byte, expiresAt, now time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableAPIKeys)
	ub.Set(
		ub.Assign("key_hash", hash),
		ub.Assign("expires_at", expiresAt),
		ub.Assign("expiration_email_sent_at", nil),
	)
	ub.Where(
		ub.Equal("id", id),
		ub.Equal("user_id", owner),
		ub.IsNull("revoked_at"),
		ub.LessThan("expires_at", now),
	)

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("apikey: renew: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RevokeKey stamps the revocation instant on one live key. The WHERE clause
// excludes an already-revoked row, so a second revocation answers false —
// the state it names is the one the key is already in — and the service
// treats that as the success it is.
func (r *Repository) RevokeKey(ctx context.Context, db datastore.Querier, id, owner uuid.UUID, at time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableAPIKeys)
	ub.Set(ub.Assign("revoked_at", at))
	ub.Where(ub.Equal("id", id), ub.Equal("user_id", owner), ub.IsNull("revoked_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("apikey: revoke: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// FindActiveKey reads the key a presented credential resolves to, with its
// owner's account beside it. The WHERE clause is the whole authwall: the
// hash must match, the key must be unrevoked and unexpired, and its owner
// must not be disabled — a disabled account's keys open nothing, the way a
// disabled account's sessions do not.
//
// The last-used instant is touched by a separate write rather than folded
// into this read: the builder's UPDATE carries no RETURNING, and a touch
// that loses a race only makes the instant a second old — it is a metric,
// not a decision.
func (r *Repository) FindActiveKey(ctx context.Context, db datastore.Querier, hash []byte, now time.Time) (KeySchema, user.UserSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(qualifiedKeyColumns("k")...)
	sb.SelectMore("u.id", "u.username", "u.email", "u.display_name")
	sb.From(entity.TableAPIKeys + " k")
	sb.Join(entity.TableUsers+" u", "u.id = k.user_id")
	// A restricted owner's keys open nothing: the ban and the lockout are
	// the account-level refusal every credential answers, and a machine
	// credential is the one that would otherwise outlive the judgement
	// entirely. The anti-join keeps the key whose account no active
	// restriction stands against.
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableAccountRestrictions+" ar",
		"ar.user_id = k.user_id AND ar.lifted_at IS NULL AND (ar.expires_at IS NULL OR ar.expires_at > now())")
	sb.Where(sb.Equal("k.key_hash", hash), sb.IsNull("k.revoked_at"), sb.GT("k.expires_at", now), sb.Equal("u.disabled", false),
		sb.IsNull("ar.user_id"))

	query, args := sb.Build()
	var key KeySchema
	var owner ownerRow
	err := db.QueryRow(ctx, query, args...).Scan(append(
		keyScanDests(&key),
		&owner.ID, &owner.Username, &owner.Email, &owner.DisplayName,
	)...)
	if errors.Is(err, datastore.ErrNoRows) {
		return KeySchema{}, user.UserSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return KeySchema{}, user.UserSchema{}, fmt.Errorf("apikey: validate: %w", err)
	}
	return key, owner.schema(), nil
}

// TouchLastUsed records that the key just authenticated a request.
func (r *Repository) TouchLastUsed(ctx context.Context, db datastore.Querier, id uuid.UUID, at time.Time) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableAPIKeys)
	ub.Set(ub.Assign("last_used_at", at))
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	// The touch is best-effort by construction: a failure to record when a
	// key was last used costs a metric, and the request it rode on has
	// already been authenticated.
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return
	}
}

// ListExpiring answers the keys that enter the reminder window before the
// cutoff and have not been reminded yet, with the address each reminder goes
// to. A revoked key is beyond reminding, and so is a key whose owner has no
// address to remind.
func (r *Repository) ListExpiring(ctx context.Context, db datastore.Querier, now, cutoff time.Time) ([]ExpiringKey, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(qualifiedKeyColumns("k")...)
	sb.SelectMore("u.email", "u.display_name")
	sb.From(entity.TableAPIKeys + " k")
	sb.Join(entity.TableUsers+" u", "u.id = k.user_id")
	sb.Where(
		sb.GT("k.expires_at", now),
		sb.LE("k.expires_at", cutoff),
		sb.IsNull("k.expiration_email_sent_at"),
		sb.IsNull("k.revoked_at"),
	)
	sb.OrderBy("k.expires_at")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("apikey: list expiring: %w", err)
	}
	defer rows.Close()

	keys := []ExpiringKey{}
	for rows.Next() {
		row, err := scanExpiring(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("apikey: list expiring: %w", err)
		}
		keys = append(keys, row)
	}
	return keys, rows.Err()
}

// MarkEmailSent records that the reminder went out, so the next run of the
// window does not repeat it.
func (r *Repository) MarkEmailSent(ctx context.Context, db datastore.Querier, id uuid.UUID, at time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableAPIKeys)
	ub.Set(ub.Assign("expiration_email_sent_at", at))
	ub.Where(ub.Equal("id", id), ub.IsNull("expiration_email_sent_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("apikey: mark email sent: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ownerRow is the account facts the authwall answers: the columns the caller
// carries into its claims, all of them NOT NULL, scanned whole rather than
// through the nullable-pointer dance a full account read needs.
type ownerRow struct {
	ID          uuid.UUID
	Username    string
	Email       string
	DisplayName string
}

// schema maps the joined facts onto the account schema the caller answers
// with, the one shape of an account in play.
func (o ownerRow) schema() user.UserSchema {
	return user.UserSchema{
		ID:          o.ID,
		Username:    o.Username,
		Email:       o.Email,
		DisplayName: o.DisplayName,
	}
}

// scanExpiring reads one row of the reminder window's query. The address and
// the name are NOT NULL columns, so they scan whole.
func scanExpiring(scan func(dest ...any) error) (ExpiringKey, error) {
	var row ExpiringKey
	if err := scan(append(keyScanDests(&row.Key), &row.OwnerEmail, &row.OwnerName)...); err != nil {
		return ExpiringKey{}, err
	}
	return row, nil
}

// keyScanDests is the scan destinations of one key row, in column order.
func keyScanDests(row *KeySchema) []any {
	return []any{
		&row.ID, &row.UserID, &row.Name, &row.Prefix, &row.KeyHash, &row.Descr,
		&row.ExpiresAt, &row.LastUsed, &row.CreatedAt, &row.UpdatedAt,
		&row.RevokedAt, &row.EmailSent,
	}
}

// ExpiringKey is one row of the reminder window: the key and where its
// reminder goes.
type ExpiringKey struct {
	Key        KeySchema
	OwnerEmail string
	OwnerName  string
}

// errUniqueViolation reports whether the write failed on a unique index, the
// way the (name, owner) index answers a duplicate key name.
func errUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
