package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"
	"uuid"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/password"
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
var UserColumns = []string{
	"id", "username", "email", "first_name", "last_name", "display_name",
	"locale", "timezone", "disabled", "email_verified_at", "created_at",
	"banned_at", "ban_expires", "ban_reason", "avatar_url",
}

// ScanSchema reads one row into the schema. The nullable columns scan through
// pointers, so an absent name part or ban reads as nil, not as a zero value.
func ScanSchema(scan func(dest ...any) error) (UserSchema, error) {
	var row UserSchema
	var firstName, lastName, locale, banReason, picturePath *string
	err := scan(
		&row.ID, &row.Username, &row.Email, &firstName, &lastName,
		&row.DisplayName, &locale, &row.Timezone, &row.Disabled,
		&row.EmailVerifiedAt, &row.CreatedAt,
		&row.BannedAt, &row.BanExpires, &banReason, &picturePath,
	)
	if err != nil {
		return UserSchema{}, err
	}
	row.FirstName = deref(firstName)
	row.LastName = deref(lastName)
	row.Locale = deref(locale)
	row.BanReason = banReason
	row.AvatarURL = picturePath
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
	sb.From(UserTable)
	sb.Where(sb.Equal("id", id))

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
}

// ListUsers answers one page of the accounts, ordered as the caller asked
// (absent a choice, newest first), with the total count the pagination
// metadata needs. A search term filters by a trigram match against the
// username, the email, and the display name — the columns the indexes exist
// for.
func (r *Repository) ListUsers(ctx context.Context, db datastore.Querier, search, sortBy string, ascending bool, offset, limit int) ([]UserSchema, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(UserColumns...)
	sb.From(UserTable)
	if search != "" {
		pattern := "%" + search + "%"
		sb.Where(sb.Or(
			sb.ILike("username", pattern),
			sb.ILike("email", pattern),
			sb.ILike("display_name", pattern),
		))
	}
	sb.OrderBy(datastore.ListOrder(userSortColumns, sortBy, "created_at", ascending), "id")
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
	cb.From(UserTable)
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
	ib.InsertInto(UserTable)
	ib.Cols("id", "username", "email", "first_name", "last_name", "display_name",
		"locale", "disabled", "email_verified_at")
	ib.Values(
		row.ID, row.Username, row.Email,
		nullIfEmpty(row.FirstName), nullIfEmpty(row.LastName), row.DisplayName,
		nullIfEmpty(row.Locale), row.Disabled, row.EmailVerifiedAt,
	)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return uuid.Nil(), err
	}
	return row.ID, nil
}

// UpdateUser replaces an account's writable fields and answers whether the
// identifier named a row. The ban columns arrive as the service computed
// them: an expiry present keeps an earlier start instant, absent clears the
// ban as a unit.
func (r *Repository) UpdateUser(ctx context.Context, db datastore.Querier, row UserSchema) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(UserTable)
	ub.Set(
		ub.Assign("username", row.Username),
		ub.Assign("email", row.Email),
		ub.Assign("first_name", nullIfEmpty(row.FirstName)),
		ub.Assign("last_name", nullIfEmpty(row.LastName)),
		ub.Assign("display_name", row.DisplayName),
		ub.Assign("locale", nullIfEmpty(row.Locale)),
		ub.Assign("timezone", row.Timezone),
		ub.Assign("disabled", row.Disabled),
		ub.Assign("banned_at", row.BannedAt),
		ub.Assign("ban_expires", row.BanExpires),
		ub.Assign("ban_reason", row.BanReason),
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
	dbl.DeleteFrom(UserTable)
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
	ib.InsertInto(password.UserPasswordTable)
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

// SetProfilePicturePath points the account's picture at a storage key, or
// clears it when the key is empty — the reset's way back to the bundled
// default picture.
func (r *Repository) SetAvatarURL(ctx context.Context, db datastore.Querier, id uuid.UUID, path string) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(UserTable)
	ub.SetMore(ub.Assign("avatar_url", nullIfEmpty(path)))
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("user: set profile picture path: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// errUniqueViolation reports whether the write failed on a unique index, the
// way the username and email indexes answer a duplicate account.
func errUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
