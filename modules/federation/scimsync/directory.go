package scimsync

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/federation/oidc"
	"github.com/riipandi/saka/modules/identity/restrictions"
)

// The visibility roll the sync applies is the OIDC authorization's own: a
// client without the group restriction admits every account, a restricted
// one admits its allowed groups' members. The two adapters read the same
// tables the preview and the protocol read — the roll must not diverge
// between what a sign-in admits and what provisioning pushes.

// Directory reads the account facts the sync pushes, against the shared
// pool. It is the concrete source the area wires the service with.
type directory struct{}

// NewDirectory builds the account source.
func NewDirectory() Directory { return directory{} }

// UsersForClient answers every account the client's visibility roll admits,
// ordered by id so a pass is deterministic.
func (directory) UsersForClient(ctx context.Context, db datastore.Querier, clientID string) ([]ProvisionedUser, error) {
	restriction, err := clientRestriction(ctx, db, clientID)
	if err != nil {
		return nil, err
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("u.id", "u.username", "u.email", "u.first_name", "u.last_name",
		"u.display_name", "u.disabled", "ar.started_at AS banned_at", "u.updated_at")
	sb.From(UserTable + " u")
	// The ban fields are the active ban restriction's read model — the
	// SCIM surface's suspended state answers from the row, not a column.
	sb.JoinWithOption(sqlbuilder.LeftJoin, restrictions.RestrictionTable+" ar",
		"ar.user_id = u.id AND ar.kind = 'ban' AND ar.lifted_at IS NULL AND (ar.expires_at IS NULL OR ar.expires_at > now())")
	if restriction.IsGroupRestricted {
		if len(restriction.AllowedGroupIDs) == 0 {
			return nil, nil
		}
		sb.JoinWithOption(sqlbuilder.LeftJoin, GroupMemberTable+" m", "m.user_id = u.id")
		sb.Where(sb.In("m.user_group_id", toAny(restriction.AllowedGroupIDs)...))
	}
	sb.OrderBy("u.id")
	query, args := sb.Build()

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scimsync: read the client's accounts: %w", err)
	}
	defer rows.Close()

	var users []ProvisionedUser
	for rows.Next() {
		var (
			u        ProvisionedUser
			disabled bool
			bannedAt *time.Time
		)
		// first_name and last_name are nullable columns; the SCIM name is
		// optional, so a NULL reads as the empty half.
		var first, last *string
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &first, &last,
			&u.DisplayName, &disabled, &bannedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scimsync: scan account: %w", err)
		}
		if first != nil {
			u.FirstName = *first
		}
		if last != nil {
			u.LastName = *last
		}
		// A banned account is as absent as a disabled one: the remote
		// should not keep a working sign-in the local side has refused.
		// The join answers only the active ban, so a present row is the
		// whole question.
		u.Active = !disabled && bannedAt == nil
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scimsync: read the client's accounts: %w", err)
	}
	return users, nil
}

// GroupDirectory reads the group facts the sync pushes.
type groupDirectory struct{}

// NewGroupDirectory builds the group source.
func NewGroupDirectory() GroupDirectory { return groupDirectory{} }

// GroupsForClient answers every group the client's visibility roll admits,
// each with the members the roll admits — a restricted client's group
// lists only the members who can actually sign in to it.
func (groupDirectory) GroupsForClient(ctx context.Context, db datastore.Querier, clientID string) ([]ProvisionedGroup, error) {
	restriction, err := clientRestriction(ctx, db, clientID)
	if err != nil {
		return nil, err
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("g.id", "g.display_name", "g.updated_at")
	sb.From(GroupTable + " g")
	if restriction.IsGroupRestricted {
		if len(restriction.AllowedGroupIDs) == 0 {
			return nil, nil
		}
		sb.Where(sb.In("g.id", toAny(restriction.AllowedGroupIDs)...))
	}
	sb.OrderBy("g.id")
	query, args := sb.Build()

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scimsync: read the client's groups: %w", err)
	}
	defer rows.Close()

	var groups []ProvisionedGroup
	for rows.Next() {
		var g ProvisionedGroup
		if err := rows.Scan(&g.ID, &g.DisplayName, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scimsync: scan group: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scimsync: read the client's groups: %w", err)
	}

	// The members ride one second query: a per-group member query would be
	// the N+1 the schema's junction makes so easy to write.
	for i := range groups {
		members, err := groupMembers(ctx, db, groups[i].ID, restriction)
		if err != nil {
			return nil, err
		}
		groups[i].MemberIDs = members
	}
	return groups, nil
}

// groupMembers answers one group's visible member ids.
func groupMembers(ctx context.Context, db datastore.Querier, groupID uuid.UUID, restriction ClientRestriction) ([]uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("m.user_id")
	sb.From(GroupMemberTable + " m")
	sb.Where(sb.Equal("m.user_group_id", groupID))
	query, args := sb.Build()

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scimsync: read a group's members: %w", err)
	}
	defer rows.Close()

	var members []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scimsync: scan member: %w", err)
		}
		members = append(members, id)
	}
	return members, rows.Err()
}

// clientRestriction reads the client-side group restriction with the same
// fail-closed rule the protocol and the catalogue use: a client is
// restricted when its flag is set or its allowed-groups roll carries rows —
// flag set with an empty roll admits nobody. The inverse table — the
// groups' own client allowlists — does not participate: a client's
// provisioning roll is what the client names, not what a group does.
func clientRestriction(ctx context.Context, db datastore.Querier, clientID string) (ClientRestriction, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("c.is_group_restricted")
	sb.From(oidc.ClientTable + " c")
	sb.Where(sb.Equal("c.id", clientID))
	query, args := sb.Build()

	var flagRestricted bool
	err := db.QueryRow(ctx, query, args...).Scan(&flagRestricted)
	if errors.Is(err, datastore.ErrNoRows) {
		return ClientRestriction{}, ErrNoProvider
	}
	if err != nil {
		return ClientRestriction{}, fmt.Errorf("scimsync: read the client's restriction: %w", err)
	}

	gb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	gb.Select("j.user_group_id")
	gb.From(oidc.AllowedGroupsTable + " j")
	gb.Where(gb.Equal("j.oidc_client_id", clientID))
	gq, gargs := gb.Build()

	grows, err := db.Query(ctx, gq, gargs...)
	if err != nil {
		return ClientRestriction{}, fmt.Errorf("scimsync: read the client's allowed groups: %w", err)
	}
	defer grows.Close()

	restriction := ClientRestriction{}
	for grows.Next() {
		var id uuid.UUID
		if err := grows.Scan(&id); err != nil {
			return ClientRestriction{}, fmt.Errorf("scimsync: scan allowed group: %w", err)
		}
		restriction.AllowedGroupIDs = append(restriction.AllowedGroupIDs, id)
	}
	if err := grows.Err(); err != nil {
		return ClientRestriction{}, fmt.Errorf("scimsync: read the client's allowed groups: %w", err)
	}

	restriction.IsGroupRestricted = flagRestricted || len(restriction.AllowedGroupIDs) > 0
	return restriction, nil
}

func toAny[T any](values []T) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
