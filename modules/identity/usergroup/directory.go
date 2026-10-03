package usergroup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"
	"go.jetify.com/typeid"
	"uuid"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/user"
)

// Repository implements the account view's group seam: what one account
// belongs to, what a page of them belongs to, and which memberships a
// creation opens. The methods live here because the junction is this
// feature's table; the user package names the contract and never learns
// the tables.
//
// Every method takes the query surface, so a caller inside a transaction
// keeps the reads and writes in it — a creation's memberships attach in
// the account's own transaction, and the views read back what that
// transaction will commit.
//
// The wire forms cross the seam untouched: the ids arrive as the request
// carried them and leave as the views render them, so neither package
// re-parses what the other already shaped.

// GroupsOfUser answers the groups one account belongs to, ordered by the
// group's display name.
func (r *Repository) GroupsOfUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]user.GroupSummary, error) {
	groups, err := r.ListGroupsOfUser(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	summaries := make([]user.GroupSummary, 0, len(groups))
	for _, group := range groups {
		summaries = append(summaries, summarize(group))
	}
	return summaries, nil
}

// GroupsOfUsers answers the memberships of many accounts in one read — the
// batch the account list needs, so a page of users costs one group query
// rather than one per row.
func (r *Repository) GroupsOfUsers(ctx context.Context, db datastore.Querier, userIDs []uuid.UUID) (map[uuid.UUID][]user.GroupSummary, error) {
	byUser := make(map[uuid.UUID][]user.GroupSummary, len(userIDs))
	if len(userIDs) == 0 {
		return byUser, nil
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("m.user_id", "g.id", "g.name", "g.display_name", "g.created_at", "g.updated_at")
	sb.From(GroupTable + " g")
	sb.JoinWithOption(sqlbuilder.InnerJoin, GroupMemberTable+" m", "m.user_group_id = g.id")
	sb.Where(sb.In("m.user_id", toList(userIDs)...))
	sb.OrderBy("m.user_id", "lower(g.display_name)", "g.id")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("usergroup: list groups of users: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var userID uuid.UUID
		var rawID string
		var name, displayName string
		var createdAt time.Time
		var updatedAt *time.Time
		if err := rows.Scan(&userID, &rawID, &name, &displayName, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("usergroup: list groups of users: %w", err)
		}
		id, err := typeid.FromUUID[GroupID](rawID)
		if err != nil {
			return nil, fmt.Errorf("usergroup: list groups of users: %w", err)
		}
		byUser[userID] = append(byUser[userID], user.GroupSummary{
			ID:          id.String(),
			Name:        name,
			DisplayName: displayName,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usergroup: list groups of users: %w", err)
	}
	return byUser, nil
}

// AttachGroups opens the memberships a creation names. Every identifier is
// parsed first, so a malformed one refuses before anything is written, and
// an identifier that names no group fails the write through the foreign key
// the junction carries — mapped to the account feature's own refusal, the
// one its handler knows.
func (r *Repository) AttachGroups(ctx context.Context, db datastore.Querier, userID uuid.UUID, groupIDs []string) error {
	ids := make([]uuid.UUID, 0, len(groupIDs))
	for _, wire := range groupIDs {
		id, err := ParseID(wire)
		if err != nil {
			return user.ErrGroupUnknown
		}
		ids = append(ids, IDToUUID(id))
	}
	if len(ids) == 0 {
		return nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(GroupMemberTable)
	ib.Cols("user_id", "user_group_id")
	for _, id := range ids {
		ib.Values(userID, id)
	}

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return user.ErrGroupUnknown
		}
		return fmt.Errorf("usergroup: attach groups: %w", err)
	}
	return nil
}

// summarize renders a group row as the fact the account view carries.
func summarize(group GroupSchema) user.GroupSummary {
	return user.GroupSummary{
		ID:          FormatID(IDToUUID(group.ID)),
		Name:        group.Name,
		DisplayName: group.DisplayName,
		CreatedAt:   group.CreatedAt,
		UpdatedAt:   group.UpdatedAt,
	}
}
