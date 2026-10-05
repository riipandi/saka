package usergroup

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"
	"go.jetify.com/typeid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/modules/identity/user"
)

// The columns a sort answers by, mapped to the SQL expression each is ordered
// by. The name columns are case-insensitive because a group named `aardvark`
// and one named `Zebra` read in dictionary order, not in byte order; the count
// is ordered by what the join computes, not by any column.
var sortColumns = map[string]string{
	"name":         "lower(g.name)",
	"display_name": "lower(g.display_name)",
	"user_count":   "count(m.user_id)",
	"created_at":   "g.created_at",
}

// Repository reads and writes the group rows the administration procedures
// manage. Every method takes the query surface, so the service passes either
// the pool or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// groupColumns are the columns the group procedures read, in scan order.
var groupColumns = []string{"g.id", "g.name", "g.display_name", "g.created_at", "g.updated_at"}

// scanGroup reads one group row into the schema. The identifier arrives as
// the UUID the column stores and leaves as the typed id the callers hold.
func scanGroup(scan func(dest ...any) error) (GroupSchema, error) {
	var row GroupSchema
	var rawID string
	err := scan(&rawID, &row.Name, &row.DisplayName, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return GroupSchema{}, err
	}
	parsed, err := typeid.FromUUID[GroupID](rawID)
	if err != nil {
		return GroupSchema{}, fmt.Errorf("usergroup: id: %w", err)
	}
	row.ID = parsed
	return row, nil
}

// ListGroups answers one page of the groups, ordered as the caller asked, with
// the total count the pagination metadata needs.
//
// The join is a LEFT join, and it is spelled with the option because
// `JoinWithOption` is the only door to it: `sb.Join` is an INNER join, which
// would drop every group whose membership is empty — exactly the groups an
// administrator just created and is about to fill. The GROUP BY turns the
// joined membership rows into one row per group, and the aggregate is the
// member count the list carries.
func (r *Repository) ListGroups(ctx context.Context, db datastore.Querier, search, sortBy string, ascending bool, offset, limit int) ([]GroupRow, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(append(groupColumns, "count(m.user_id)")...)
	sb.From(entity.TableUserGroups + " g")
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableUserGroupsUsers+" m", "m.user_group_id = g.id")
	sb.GroupBy("g.id")
	applySearch(sb, search)
	order := datastore.ListOrder(sortColumns, sortBy, "display_name", ascending)
	sb.OrderBy(order, "g.id")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("usergroup: list: %w", err)
	}
	defer rows.Close()

	groups := []GroupRow{}
	for rows.Next() {
		group, err := scanListRow(rows.Scan)
		if err != nil {
			return nil, 0, fmt.Errorf("usergroup: list: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("usergroup: list: %w", err)
	}

	// The count query filters by the same search but joins nothing: the
	// member count belongs to the page's rows, not to the pagination.
	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableUserGroups + " g")
	applySearch(cb, search)
	query, args = cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("usergroup: count: %w", err)
	}
	return groups, total, nil
}

// applySearch narrows a list to the groups whose name or display name matches
// the term. It is applied to the page query and to the count query separately,
// because a builder's condition and its argument list are one thing.
func applySearch(sb *sqlbuilder.SelectBuilder, search string) {
	if search != "" {
		pattern := "%" + search + "%"
		sb.Where(sb.Or(
			sb.ILike("g.name", pattern),
			sb.ILike("g.display_name", pattern),
		))
	}
}

// scanListRow reads one row of the joined list answer. The group's columns
// and the member count scan together: one row is one Scan call, and a second
// call would read a row that is not there.
func scanListRow(scan func(dest ...any) error) (GroupRow, error) {
	var row GroupRow
	var rawID string
	var count int
	err := scan(
		&rawID, &row.Name, &row.DisplayName, &row.CreatedAt, &row.UpdatedAt,
		&count,
	)
	if err != nil {
		return GroupRow{}, err
	}
	parsed, err := typeid.FromUUID[GroupID](rawID)
	if err != nil {
		return GroupRow{}, fmt.Errorf("usergroup: id: %w", err)
	}
	row.ID = parsed
	// A LEFT join answers no joined rows as zero, which is the count a group
	// without members carries.
	row.UserCount = count
	return row, nil
}

// GetGroup reads one group by its identifier. An identifier that names no
// group is the caller's not-found failure.
func (r *Repository) GetGroup(ctx context.Context, db datastore.Querier, id GroupID) (GroupSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "name", "display_name", "created_at", "updated_at")
	sb.From(entity.TableUserGroups)
	sb.Where(sb.Equal("id", id.UUID()))

	query, args := sb.Build()
	row, err := scanGroup(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return GroupSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return GroupSchema{}, fmt.Errorf("usergroup: get: %w", err)
	}
	return row, nil
}

// CreateGroup inserts the group row and answers its identifier. The unique
// index on the name is the storage of the name rule, and the service reads
// the write's failure to answer a duplicate.
func (r *Repository) CreateGroup(ctx context.Context, db datastore.Querier, row GroupSchema) (GroupID, error) {
	id, idErr := typeid.New[GroupID]()
	if idErr != nil {
		return GroupID{}, fmt.Errorf("usergroup: id: %w", idErr)
	}
	row.ID = id

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUserGroups)
	ib.Cols("id", "name", "display_name")
	ib.Values(row.ID.UUID(), row.Name, row.DisplayName)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return GroupID{}, err
	}
	return row.ID, nil
}

// UpdateGroup replaces a group's two fields and answers whether the identifier
// named a row. The updated instant is the trigger's job, not this query's.
func (r *Repository) UpdateGroup(ctx context.Context, db datastore.Querier, row GroupSchema) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableUserGroups)
	ub.Set(
		ub.Assign("name", row.Name),
		ub.Assign("display_name", row.DisplayName),
	)
	ub.Where(ub.Equal("id", row.ID.UUID()))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("usergroup: update: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteGroup removes a group and answers whether an identifier named a row.
// The membership rows die with the group by the foreign keys' cascade.
func (r *Repository) DeleteGroup(ctx context.Context, db datastore.Querier, id GroupID) (bool, error) {
	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(entity.TableUserGroups)
	dbl.Where(dbl.Equal("id", id.UUID()))

	query, args := dbl.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("usergroup: delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListMembers answers the accounts that belong to a group, ordered by
// username. The join is an INNER one on purpose: a membership row whose
// account is gone cannot exist, because the foreign key cascades, so a LEFT
// join would have nothing to preserve.
func (r *Repository) ListMembers(ctx context.Context, db datastore.Querier, groupID GroupID) ([]user.UserSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(user.UserColumns...)
	sb.From(entity.TableUsers + " u")
	user.ActiveBanJoin(sb)
	sb.Join(entity.TableUserGroupsUsers+" m", "m.user_id = u.id")
	sb.Where(sb.Equal("m.user_group_id", groupID.UUID()))
	sb.OrderBy("u.username", "u.id")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("usergroup: list members: %w", err)
	}
	defer rows.Close()

	members := []user.UserSchema{}
	for rows.Next() {
		row, err := user.ScanSchema(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("usergroup: list members: %w", err)
		}
		members = append(members, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usergroup: list members: %w", err)
	}
	return members, nil
}

// SetMembers replaces a group's member set with the accounts the caller
// named. The replacement is a delete and a batched insert, one statement
// pair inside the caller's transaction — a group read mid-replacement sees
// either the old set or the new one, never a half of each.
//
// The accounts are checked first: every identifier must name an account,
// because a member that does not exist would make the group wrong rather
// than merely empty. The caller refuses otherwise.
func (r *Repository) SetMembers(ctx context.Context, db datastore.Querier, groupID GroupID, userIDs []uuid.UUID) (int, error) {
	if len(userIDs) > 0 {
		cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
		cb.Select("count(*)")
		cb.From(entity.TableUsers)
		cb.Where(cb.In("id", toList(userIDs)...))

		query, args := cb.Build()
		var found int
		if err := db.QueryRow(ctx, query, args...).Scan(&found); err != nil {
			return 0, fmt.Errorf("usergroup: count members: %w", err)
		}
		if found != len(userIDs) {
			return 0, user.ErrUserNotFound
		}
	}

	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(entity.TableUserGroupsUsers)
	dbl.Where(dbl.Equal("user_group_id", groupID.UUID()))

	query, args := dbl.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return 0, fmt.Errorf("usergroup: clear members: %w", err)
	}

	if len(userIDs) == 0 {
		return 0, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUserGroupsUsers)
	ib.Cols("user_id", "user_group_id")
	for _, userID := range userIDs {
		ib.Values(userID, groupID.UUID())
	}

	query, args = ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return 0, fmt.Errorf("usergroup: insert members: %w", err)
	}
	return len(userIDs), nil
}

// ListGroupsOfUser answers the groups one account belongs to, ordered by the
// group's display name. The join is an INNER one: a membership row survives
// only beside its group, so the inner form drops nothing.
func (r *Repository) ListGroupsOfUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]GroupSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("g.id", "g.name", "g.display_name", "g.created_at", "g.updated_at")
	sb.From(entity.TableUserGroups + " g")
	sb.Join(entity.TableUserGroupsUsers+" m", "m.user_group_id = g.id")
	sb.Where(sb.Equal("m.user_id", userID))
	sb.OrderBy("lower(g.display_name)", "g.id")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("usergroup: list groups of user: %w", err)
	}
	defer rows.Close()

	groups := []GroupSchema{}
	for rows.Next() {
		group, err := scanGroup(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("usergroup: list groups of user: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usergroup: list groups of user: %w", err)
	}
	return groups, nil
}

// SetUserGroups replaces the set of groups one account belongs to. It is
// the per-user mirror of SetMembers: a delete of the account's memberships
// and a batched insert, one statement pair inside the caller's transaction.
//
// The groups are checked first, the way SetMembers checks the accounts:
// every identifier must name a group, because a membership into nothing
// would make the account wrong rather than merely ungrouped.
func (r *Repository) SetUserGroups(ctx context.Context, db datastore.Querier, userID uuid.UUID, groupIDs []GroupID) (int, error) {
	if len(groupIDs) > 0 {
		keys := make([]any, 0, len(groupIDs))
		for _, id := range groupIDs {
			keys = append(keys, id.UUID())
		}
		cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
		cb.Select("count(*)")
		cb.From(entity.TableUserGroups)
		cb.Where(cb.In("id", keys...))

		query, args := cb.Build()
		var found int
		if err := db.QueryRow(ctx, query, args...).Scan(&found); err != nil {
			return 0, fmt.Errorf("usergroup: count groups of user: %w", err)
		}
		if found != len(groupIDs) {
			return 0, ErrGroupNotFound
		}
	}

	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(entity.TableUserGroupsUsers)
	dbl.Where(dbl.Equal("user_id", userID))

	query, args := dbl.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return 0, fmt.Errorf("usergroup: clear user groups: %w", err)
	}

	if len(groupIDs) == 0 {
		return 0, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUserGroupsUsers)
	ib.Cols("user_id", "user_group_id")
	for _, groupID := range groupIDs {
		ib.Values(userID, groupID.UUID())
	}

	query, args = ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return 0, fmt.Errorf("usergroup: insert user groups: %w", err)
	}
	return len(groupIDs), nil
}

// errUniqueViolation reports whether the write failed on a unique index, the
// way the name index answers a duplicate group.
func errUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// toList is the identifiers' slice, for the IN clause's variadic form.
func toList(ids []uuid.UUID) []any {
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	return values
}
