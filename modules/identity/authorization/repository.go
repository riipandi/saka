package authorization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"
	"go.jetify.com/typeid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
)

// The columns a role list sorts by, mapped to the SQL expression each is
// ordered by. The names sort case-insensitively, the way every other list
// in the application reads.
var roleSortColumns = map[string]string{
	"name":       "lower(r.name)",
	"slug":       "r.slug",
	"created_at": "r.created_at",
}

// permissionSortColumns are the columns a catalog read sorts by. The
// fallback orders by the slug's first segment — the resource — so the
// unsorted answer still reads grouped, slugs alphabetical inside each
// resource.
var permissionSortColumns = map[string]string{
	"resource":    "split_part(slug, ':', 1)",
	"slug":        "slug",
	"description": "lower(description)",
}

// Repository reads and writes the authorization rows the administration
// procedures manage. Every method takes the query surface, so the service
// passes either the pool or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// roleColumns are the columns the role procedures read, in scan order. The
// callers alias the roles table `r`, so the qualified list is theirs to
// reuse; the unaliased one is for the single-table reads.
var roleColumns = []string{"id", "name", "slug", "description", "type", "created_at", "updated_at"}

// scanRole reads one role row into the schema. The identifier arrives as
// the UUID the column stores and leaves as the typed id the callers hold.
func scanRole(scan func(dest ...any) error) (RoleSchema, error) {
	var row RoleSchema
	var rawID string
	err := scan(&rawID, &row.Name, &row.Slug, &row.Description, &row.Type, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return RoleSchema{}, err
	}
	parsed, err := typeid.FromUUID[RoleID](rawID)
	if err != nil {
		return RoleSchema{}, fmt.Errorf("authorization: id: %w", err)
	}
	row.ID = parsed
	return row, nil
}

// ListRoles answers one page of the roles, ordered as the caller asked,
// optionally narrowed by a search term and a role type, with the total
// count the pagination metadata needs. The join is a LEFT join, spelled
// with the option because `sb.Join` is an INNER join: a role whose
// permission set is empty — exactly the role an administrator just created —
// would drop out of an inner form.
func (r *Repository) ListRoles(ctx context.Context, db datastore.Querier, search, roleType, sortBy string, ascending bool, offset, limit int) ([]RoleRow, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(append(qualifiedRoleColumns(), "count(rp.permission_id)")...)
	sb.From(entity.TableRoles + " r")
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableRolePermissions+" rp", "rp.role_id = r.id")
	sb.GroupBy("r.id")
	applyRoleSearch(sb, search)
	applyRoleType(sb, roleType)
	sb.OrderBy(datastore.ListOrder(roleSortColumns, sortBy, "name", ascending), "r.id")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("authorization: list roles: %w", err)
	}
	defer rows.Close()

	roles := []RoleRow{}
	for rows.Next() {
		row, err := scanRoleRow(rows.Scan)
		if err != nil {
			return nil, 0, fmt.Errorf("authorization: list roles: %w", err)
		}
		roles = append(roles, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("authorization: list roles: %w", err)
	}

	// The count query filters by the same search but joins nothing: the
	// permission count belongs to the page's rows, not to the pagination.
	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableRoles + " r")
	applyRoleSearch(cb, search)
	applyRoleType(cb, roleType)
	query, args = cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("authorization: count roles: %w", err)
	}
	return roles, total, nil
}

// qualifiedRoleColumns is the role list with the `r.` prefix the joined
// queries need.
func qualifiedRoleColumns() []string {
	return []string{"r.id", "r.name", "r.slug", "r.description", "r.type", "r.created_at", "r.updated_at"}
}

// scanRoleRow reads one row of the joined list answer: the role's columns
// and the permission count scan together, one row per Scan call.
func scanRoleRow(scan func(dest ...any) error) (RoleRow, error) {
	// One Scan call for the whole joined row: pgx checks the destination
	// count against the field count per call, so the role's columns and
	// the permission count must arrive together.
	var count int
	schema, err := scanRole(func(dest ...any) error {
		return scan(append(dest, &count)...)
	})
	if err != nil {
		return RoleRow{}, err
	}
	// A LEFT join answers no joined rows as zero, which is the count a role
	// without permissions carries.
	return RoleRow{RoleSchema: schema, PermissionCount: count}, nil
}

// applyRoleSearch narrows a role list to the rows whose name or slug
// matches the term. It is applied to the page query and the count query
// separately, because a builder's condition and its argument list are one
// thing.
func applyRoleSearch(sb *sqlbuilder.SelectBuilder, search string) {
	if search != "" {
		pattern := "%" + search + "%"
		sb.Where(sb.Or(
			sb.ILike("r.name", pattern),
			sb.ILike("r.slug", pattern),
		))
	}
}

// applyRoleType narrows the read to one `role_type` value; an empty one
// keeps every kind.
func applyRoleType(sb *sqlbuilder.SelectBuilder, roleType string) {
	if roleType != "" {
		sb.Where(sb.Equal("r.type", roleType))
	}
}

// ListPermissions answers the permission catalog as the database holds it —
// the rows the seed writes from the code's list — narrowed by a search term
// and a resource segment, ordered as the caller asked. The ids the answer
// carries are the rows' own, the ones the grant junctions reference.
func (r *Repository) ListPermissions(ctx context.Context, db datastore.Querier, search, resource, sortBy string, ascending bool) ([]PermissionSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "slug", "description")
	sb.From(entity.TablePermissions)
	applyPermissionFilter(sb, search, resource)
	sb.OrderBy(datastore.ListOrder(permissionSortColumns, sortBy, "resource", ascending), "slug")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("authorization: list permissions: %w", err)
	}
	defer rows.Close()

	permissions := []PermissionSchema{}
	for rows.Next() {
		var rawID string
		var entry PermissionSchema
		if err := rows.Scan(&rawID, &entry.Slug, &entry.Description); err != nil {
			return nil, fmt.Errorf("authorization: list permissions: %w", err)
		}
		parsed, err := typeid.FromUUID[PermID](rawID)
		if err != nil {
			return nil, fmt.Errorf("authorization: permission id: %w", err)
		}
		entry.ID = parsed
		permissions = append(permissions, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("authorization: list permissions: %w", err)
	}
	return permissions, nil
}

// applyPermissionFilter narrows the catalog read: a search matches the slug
// or the description case-insensitively, a resource pins the slug's first
// segment.
func applyPermissionFilter(sb *sqlbuilder.SelectBuilder, search, resource string) {
	if search != "" {
		pattern := "%" + search + "%"
		sb.Where(sb.Or(
			sb.ILike("slug", pattern),
			sb.ILike("description", pattern),
		))
	}
	if resource != "" {
		sb.Where(sb.Equal("split_part(slug, ':', 1)", resource))
	}
}

// GetRole reads one role by its identifier. An identifier that names no
// role is the caller's not-found failure.
func (r *Repository) GetRole(ctx context.Context, db datastore.Querier, id RoleID) (RoleSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(roleColumns...)
	sb.From(entity.TableRoles)
	sb.Where(sb.Equal("id", id.UUID()))

	query, args := sb.Build()
	row, err := scanRole(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return RoleSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return RoleSchema{}, fmt.Errorf("authorization: get role: %w", err)
	}
	return row, nil
}

// GetRoleBySlug reads one role by its slug — the read the seed's
// idempotence leans on and the claims' role names resolve through.
func (r *Repository) GetRoleBySlug(ctx context.Context, db datastore.Querier, slug string) (RoleSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(roleColumns...)
	sb.From(entity.TableRoles)
	sb.Where(sb.Equal("slug", slug))

	query, args := sb.Build()
	row, err := scanRole(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return RoleSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return RoleSchema{}, fmt.Errorf("authorization: get role by slug: %w", err)
	}
	return row, nil
}

// CreateRole inserts the role row and answers its identifier. The unique
// indexes on the slug and the name are the storage of the uniqueness rules,
// and the service reads the write's failure to answer a duplicate.
func (r *Repository) CreateRole(ctx context.Context, db datastore.Querier, row RoleSchema) (RoleID, error) {
	id, idErr := typeid.New[RoleID]()
	if idErr != nil {
		return RoleID{}, fmt.Errorf("authorization: id: %w", idErr)
	}
	row.ID = id

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableRoles)
	ib.Cols("id", "name", "slug", "description", "type")
	ib.Values(id.UUID(), row.Name, row.Slug, row.Description, row.Type)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return RoleID{}, err
	}
	return row.ID, nil
}

// UpdateRole replaces a role's name and description and answers whether the
// identifier named a row. The updated instant is the trigger's job.
func (r *Repository) UpdateRole(ctx context.Context, db datastore.Querier, row RoleSchema) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableRoles)
	ub.Set(
		ub.Assign("name", row.Name),
		ub.Assign("description", row.Description),
	)
	ub.Where(ub.Equal("id", row.ID.UUID()))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("authorization: update role: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteRole removes a role and answers whether an identifier named a row.
// The role's permission rows die with it by the cascade; the account grants
// do not — the service refuses a role accounts still hold, so the removal
// strips nothing silently.
func (r *Repository) DeleteRole(ctx context.Context, db datastore.Querier, id RoleID) (bool, error) {
	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(entity.TableRoles)
	dbl.Where(dbl.Equal("id", id.UUID()))

	query, args := dbl.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("authorization: delete role: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// CountActiveHolders answers how many accounts actively hold the role. It
// is the check a deletion runs before it removes: a role in use is not a
// row to cascade away, it is the authorization of whoever holds it.
func (r *Repository) CountActiveHolders(ctx context.Context, db datastore.Querier, id RoleID) (int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TableUserRoles)
	sb.Where(sb.Equal("role_id", id.UUID()), sb.IsNull("revoked_at"))

	query, args := sb.Build()
	var count int
	if err := db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("authorization: count holders: %w", err)
	}
	return count, nil
}

// ListRolePermissions answers the slugs one role carries, ordered.
func (r *Repository) ListRolePermissions(ctx context.Context, db datastore.Querier, id RoleID) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("p.slug")
	sb.From(entity.TableRolePermissions + " rp")
	sb.Join(entity.TablePermissions + " p ON p.id = rp.permission_id")
	sb.Where(sb.Equal("rp.role_id", id.UUID()))
	sb.OrderBy("p.slug")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("authorization: list role permissions: %w", err)
	}
	defer rows.Close()

	slugs := []string{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("authorization: list role permissions: %w", err)
		}
		slugs = append(slugs, slug)
	}
	return slugs, rows.Err()
}

// ResolvePermissionIDs answers the row identifiers the slugs name, in the
// order the slugs arrived. A slug the table does not hold is the caller's
// not-found failure, so a set replacement never writes a grant into
// nothing.
func (r *Repository) ResolvePermissionIDs(ctx context.Context, db datastore.Querier, slugs []string) ([]uuid.UUID, error) {
	if len(slugs) == 0 {
		return nil, nil
	}
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TablePermissions)
	sb.Where(sb.In("slug", toAny(slugs)...))

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("authorization: resolve permissions: %w", err)
	}
	defer rows.Close()

	ids := make([]uuid.UUID, 0, len(slugs))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("authorization: resolve permissions: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("authorization: resolve permissions: %w", err)
	}
	if len(ids) != len(slugs) {
		return nil, ErrPermissionNotFound
	}
	return ids, nil
}

// SetRolePermissions replaces a role's permission set. The junction keeps
// no history — a role's set is its current meaning, and the audit record
// remembers the change — so the replacement is a delete and a batched
// insert, one statement pair inside the caller's transaction.
func (r *Repository) SetRolePermissions(ctx context.Context, db datastore.Querier, id RoleID, permissionIDs []uuid.UUID) error {
	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(entity.TableRolePermissions)
	dbl.Where(dbl.Equal("role_id", id.UUID()))

	query, args := dbl.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("authorization: clear role permissions: %w", err)
	}

	if len(permissionIDs) == 0 {
		return nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableRolePermissions)
	ib.Cols("role_id", "permission_id")
	for _, permissionID := range permissionIDs {
		ib.Values(id.UUID(), permissionID)
	}

	query, args = ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("authorization: grant role permissions: %w", err)
	}
	return nil
}

// ListActiveRoleIDs answers the identifiers of the roles one account
// actively holds. It is the read a set replacement diffs against.
func (r *Repository) ListActiveRoleIDs(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("role_id")
	sb.From(entity.TableUserRoles)
	sb.Where(sb.Equal("user_id", userID), sb.IsNull("revoked_at"))

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("authorization: list active roles: %w", err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("authorization: list active roles: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RevokeRoles stamps the named grant rows revoked. The stamp is a
// compare-and-set on the row's own life — only a row still active is
// written, so a grant revoked a moment earlier keeps its first stamp and
// its first revoker.
func (r *Repository) RevokeRoles(ctx context.Context, db datastore.Querier, userID uuid.UUID, roleIDs []uuid.UUID, revokedBy *uuid.UUID, now time.Time) error {
	if len(roleIDs) == 0 {
		return nil
	}
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableUserRoles)
	ub.Set(
		ub.Assign("revoked_at", now),
		ub.Assign("revoked_by", revokedBy),
	)
	ub.Where(
		ub.Equal("user_id", userID),
		ub.IsNull("revoked_at"),
		ub.In("role_id", toUUIDAny(roleIDs)...),
	)

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("authorization: revoke roles: %w", err)
	}
	return nil
}

// GrantRoles inserts the grant rows for the roles an account newly holds.
// granted_by names the administrator who made the change; an absent one —
// the seed's grants — inserts a NULL the column allows.
func (r *Repository) GrantRoles(ctx context.Context, db datastore.Querier, userID uuid.UUID, roleIDs []uuid.UUID, grantedBy *uuid.UUID) error {
	if len(roleIDs) == 0 {
		return nil
	}
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUserRoles)
	ib.Cols("user_id", "role_id", "granted_by")
	for _, roleID := range roleIDs {
		ib.Values(userID, roleID, grantedBy)
	}

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("authorization: grant roles: %w", err)
	}
	return nil
}

// ListRolesOfUser answers the roles one account actively holds, ordered by
// name. The join is an INNER one on purpose: a grant row whose role is gone
// cannot exist, because the foreign key cascades.
func (r *Repository) ListRolesOfUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]RoleSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(qualifiedRoleColumns()...)
	sb.From(entity.TableRoles + " r")
	sb.Join(entity.TableUserRoles + " ur ON ur.role_id = r.id")
	sb.Where(sb.Equal("ur.user_id", userID), sb.IsNull("ur.revoked_at"))
	sb.OrderBy("lower(r.name)", "r.id")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("authorization: list roles of user: %w", err)
	}
	defer rows.Close()

	roles := []RoleSchema{}
	for rows.Next() {
		row, err := scanRole(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("authorization: list roles of user: %w", err)
		}
		roles = append(roles, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("authorization: list roles of user: %w", err)
	}
	return roles, nil
}

// ListActivePermissionIDs answers the identifiers of the permissions one
// account holds directly and actively. It is the read a set replacement
// diffs against.
func (r *Repository) ListActivePermissionIDs(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("permission_id")
	sb.From(entity.TableUserPermissions)
	sb.Where(sb.Equal("user_id", userID), sb.IsNull("revoked_at"))

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("authorization: list active permissions: %w", err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("authorization: list active permissions: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RevokePermissions stamps the named direct-grant rows revoked, the way
// RevokeRoles stamps the role grants.
func (r *Repository) RevokePermissions(ctx context.Context, db datastore.Querier, userID uuid.UUID, permissionIDs []uuid.UUID, revokedBy *uuid.UUID, now time.Time) error {
	if len(permissionIDs) == 0 {
		return nil
	}
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableUserPermissions)
	ub.Set(
		ub.Assign("revoked_at", now),
		ub.Assign("revoked_by", revokedBy),
	)
	ub.Where(
		ub.Equal("user_id", userID),
		ub.IsNull("revoked_at"),
		ub.In("permission_id", toUUIDAny(permissionIDs)...),
	)

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("authorization: revoke permissions: %w", err)
	}
	return nil
}

// GrantPermissions inserts the direct-grant rows for the permissions an
// account newly carries.
func (r *Repository) GrantPermissions(ctx context.Context, db datastore.Querier, userID uuid.UUID, permissionIDs []uuid.UUID, grantedBy *uuid.UUID) error {
	if len(permissionIDs) == 0 {
		return nil
	}
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUserPermissions)
	ib.Cols("user_id", "permission_id", "granted_by")
	for _, permissionID := range permissionIDs {
		ib.Values(userID, permissionID, grantedBy)
	}

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("authorization: grant permissions: %w", err)
	}
	return nil
}

// ListPermissionsOfUser answers the slugs granted to one account directly,
// outside any role, ordered.
func (r *Repository) ListPermissionsOfUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("p.slug")
	sb.From(entity.TableUserPermissions + " up")
	sb.Join(entity.TablePermissions + " p ON p.id = up.permission_id")
	sb.Where(sb.Equal("up.user_id", userID), sb.IsNull("up.revoked_at"))
	sb.OrderBy("p.slug")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("authorization: list permissions of user: %w", err)
	}
	defer rows.Close()

	slugs := []string{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("authorization: list permissions of user: %w", err)
		}
		slugs = append(slugs, slug)
	}
	return slugs, rows.Err()
}

// errUniqueViolation reports whether the write failed on a unique index,
// the way the slug index answers a duplicate role.
func errUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// toAny is the slugs' slice, for the IN clause's variadic form.
func toAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

// toUUIDAny is the identifiers' slice, for the IN clause's variadic form.
func toUUIDAny(values []uuid.UUID) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
