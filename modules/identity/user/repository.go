package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
)

// Repository reads and writes the account rows the administration procedures
// manage. Every method takes the query surface, so the service passes either
// the pool or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// userColumns are the columns the account procedures read, in scan order.
// The main table rides the `u` alias — the active-ban join needs it — and
// the ban trio plus the picture pair are the read models the joins provide:
// the restriction row is the ban's storage, the filestore object row the
// picture's, and these columns are their views.
var UserColumns = []string{
	"u.id", "u.username", "u.email", "u.first_name", "u.last_name", "u.display_name",
	"u.metadata", "u.disabled", "u.email_verified_at", "u.created_at", "u.updated_at",
	"ar.started_at AS banned_at", "ar.expires_at AS ban_expires", "ar.reason AS ban_reason",
	"b.name AS picture_bucket", "so.key AS picture_key",
	"u.self_delete_override",
}

// ActiveBanJoin arms the ban read model (the account's active ban
// restriction, one row at most — a re-ban replaces the open row's terms) and
// the picture read model (the filestore object the account's picture
// reference names). Both joins are left ones on purpose: an account the
// restriction row or the object row does not name is the unbanned account
// and the default-picture account, rows the read must keep. The reads that
// scan UserColumns call it; a caller that does not must answer the ban and
// picture fields some other way.
func ActiveBanJoin(sb *sqlbuilder.SelectBuilder) {
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableAccountRestrictions+" ar",
		"ar.user_id = u.id AND ar.kind = 'ban' AND ar.lifted_at IS NULL AND (ar.expires_at IS NULL OR ar.expires_at > now())")
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableStorageObjects+" so", "so.id = u.picture_file_id")
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableStorageBuckets+" b", "b.id = so.bucket_id")
}

// ScanSchema reads one row into the schema. The nullable columns scan through
// pointers, so an absent name part, ban, or username — the column went
// nullable when the username became optional — reads as nil, not as a zero
// value.
func ScanSchema(scan func(dest ...any) error) (UserSchema, error) {
	var row UserSchema
	var username, firstName, lastName, banReason, pictureBucket, pictureKey *string
	err := scan(
		&row.ID, &username, &row.Email, &firstName, &lastName,
		&row.DisplayName, &row.Metadata, &row.Disabled,
		&row.EmailVerifiedAt, &row.CreatedAt, &row.UpdatedAt,
		&row.BannedAt, &row.BanExpires, &banReason, &pictureBucket, &pictureKey,
		&row.SelfDeleteOverride,
	)
	if err != nil {
		return UserSchema{}, err
	}
	row.Username = deref(username)
	row.FirstName = deref(firstName)
	row.LastName = deref(lastName)
	row.BanReason = banReason
	row.PictureBucket = pictureBucket
	row.PictureKey = pictureKey
	return row, nil
}

// deref turns a nullable column's scan target into the value the struct
// carries: an absent column is the empty string the struct's zero value
// holds, and the query writes it back as NULL.
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// GetUser reads one account by its identifier. An identifier that names no
// account is the caller's not-found failure.
func (r *Repository) GetUser(ctx context.Context, db datastore.Querier, id uuid.UUID) (UserSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(UserColumns...)
	sb.From(entity.TableUsers + " u")
	ActiveBanJoin(sb)
	sb.Where(sb.Equal("u.id", id))

	query, args := sb.Build()
	row, err := ScanSchema(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return UserSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return UserSchema{}, fmt.Errorf("user: get: %w", err)
	}
	return row, nil
}

// userSortColumns is the whitelist a list's sort key resolves through. The
// names are the wire values `ListUsersRequest.sort_by` validates against.
var userSortColumns = map[string]string{
	"username":     "lower(username)",
	"email":        "lower(email)",
	"first_name":   "lower(first_name)",
	"last_name":    "lower(last_name)",
	"display_name": "lower(display_name)",
	"created_at":   "created_at",
	// The sign-in stamps the column; the partial btree index answers the sort.
	"last_login_at": "last_login_at",
}

// ListUsers answers one page of the accounts, ordered as the caller asked
// (absent a choice, newest first), with the total count the pagination
// metadata needs. A search term filters by a trigram match against the
// username, the email, and the display name — the columns the indexes exist
// for.
func (r *Repository) ListUsers(ctx context.Context, db datastore.Querier, search, sortBy string, ascending bool, offset, limit int) ([]UserSchema, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(UserColumns...)
	sb.From(entity.TableUsers + " u")
	ActiveBanJoin(sb)
	if search != "" {
		pattern := "%" + search + "%"
		sb.Where(sb.Or(
			sb.ILike("u.username", pattern),
			sb.ILike("u.email", pattern),
			sb.ILike("u.display_name", pattern),
		))
	}
	sb.OrderBy(datastore.ListOrder(userSortColumns, sortBy, "created_at", ascending), "u.id")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("user: list: %w", err)
	}
	defer rows.Close()

	users := []UserSchema{}
	for rows.Next() {
		row, err := ScanSchema(rows.Scan)
		if err != nil {
			return nil, 0, fmt.Errorf("user: list: %w", err)
		}
		users = append(users, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("user: list: %w", err)
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableUsers)
	if search != "" {
		pattern := "%" + search + "%"
		cb.Where(cb.Or(
			cb.ILike("username", pattern),
			cb.ILike("email", pattern),
			cb.ILike("display_name", pattern),
		))
	}
	query, args = cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("user: count: %w", err)
	}
	return users, total, nil
}

// CreateUser inserts the account row and answers its identifier. The nullable
// columns carry the NULL an absent field stores, not the empty string the
// struct's zero value holds.
func (r *Repository) CreateUser(ctx context.Context, db datastore.Querier, row UserSchema) (uuid.UUID, error) {
	row.ID = uuid.NewV7()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUsers)
	ib.Cols("id", "username", "email", "first_name", "last_name", "display_name",
		"metadata", "disabled", "email_verified_at")
	ib.Values(
		row.ID, nullIfEmpty(row.Username), row.Email,
		nullIfEmpty(row.FirstName), nullIfEmpty(row.LastName), row.DisplayName,
		nullJSON(row.Metadata), row.Disabled, row.EmailVerifiedAt,
	)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return uuid.Nil(), err
	}
	return row.ID, nil
}

// UpdateUser replaces an account's writable fields and answers whether the
// identifier named a row. The ban read model is nobody's write: the ban's
// storage is the restrictions feature's row.
func (r *Repository) UpdateUser(ctx context.Context, db datastore.Querier, row UserSchema) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableUsers)
	ub.Set(
		ub.Assign("username", row.Username),
		ub.Assign("email", row.Email),
		ub.Assign("first_name", nullIfEmpty(row.FirstName)),
		ub.Assign("last_name", nullIfEmpty(row.LastName)),
		ub.Assign("display_name", row.DisplayName),
		ub.Assign("metadata", nullJSON(row.Metadata)),
		ub.Assign("disabled", row.Disabled),
	)
	ub.Where(ub.Equal("id", row.ID))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("user: update: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteUser removes an account. The database trigger archives the removed
// row, so the removal is soft by construction. It answers whether an
// identifier named a row, so the caller refuses one that names nothing.
func (r *Repository) DeleteUser(ctx context.Context, db datastore.Querier, id uuid.UUID) (bool, error) {
	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(entity.TableUsers)
	dbl.Where(dbl.Equal("id", id))

	query, args := dbl.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("user: delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// CreatePassword stores the account's primary credential.
func (r *Repository) CreatePassword(ctx context.Context, db datastore.Querier, userID uuid.UUID, passwordHash string) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUserPasswords)
	ib.Cols("user_id", "password_hash")
	ib.Values(userID, passwordHash)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("user: create password: %w", err)
	}
	return nil
}

// nullIfEmpty turns an absent optional field into the SQL NULL its column
// stores.
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nullJSON turns an absent document into the SQL NULL its column stores; a
// document that carries no keys is still a document — the state "the account
// answered every preference", distinct from "no document was ever written".
func nullJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

// SetPictureFileID points the account's picture at a filestore object row,
// or clears it when the id is empty — the reset's way back to the bundled
// default picture. The id is the object row's UUID, not its wire TypeID: the
// column holds what the foreign key resolves, and the wire form never
// crosses into a query.
func (r *Repository) SetPictureFileID(ctx context.Context, db datastore.Querier, id uuid.UUID, fileID string) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableUsers)
	ub.SetMore(ub.Assign("picture_file_id", nullIfEmpty(fileID)))
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("user: set picture reference: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MergeCustomAttributes folds one identity source's answer onto the
// account's document: the stored document — or an empty one, for an
// account no source has written — is read, the incoming keys overwrite
// theirs, and the merged document is written back. The read and the
// write share the caller's transaction, so the merge sees its own view;
// the row the read locks against a racing merge is the same row the
// update rewrites.
func (r *Repository) MergeCustomAttributes(ctx context.Context, db datastore.Querier, id uuid.UUID, attrs map[string]any) error {
	var stored []byte
	err := db.QueryRow(ctx,
		`SELECT COALESCE(custom_attributes, '{}'::jsonb) FROM `+entity.TableUsers+` WHERE id = $1 FOR UPDATE`,
		id).Scan(&stored)
	if err != nil {
		return fmt.Errorf("user: read custom attributes: %w", err)
	}

	document := map[string]any{}
	if len(stored) > 0 {
		if unmarshalErr := json.Unmarshal(stored, &document); unmarshalErr != nil {
			return fmt.Errorf("user: stored custom attributes: %w", unmarshalErr)
		}
	}
	maps.Copy(document, attrs)
	merged, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("user: custom attributes: %w", err)
	}

	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableUsers)
	ub.Set(ub.Assign("custom_attributes", merged))
	ub.Where(ub.Equal("id", id))
	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("user: merge custom attributes: %w", err)
	}
	return nil
}

// errUniqueViolation reports whether the write failed on a unique index, the
// way the username and email indexes answer a duplicate account.
func errUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
