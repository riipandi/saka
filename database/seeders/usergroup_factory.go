package seeders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/modules/identity/usergroup"
)

// UserGroupSeederName is the name this seeder reports under.
const UserGroupSeederName = "UserGroupSeeder"

// scenarioGroups are the groups a development database carries: one with
// members a notification audience can name, one with a single member, and
// one deliberately empty — the group a feature must handle without choking
// on a membership row that does not exist. The members are scenario accounts
// the user seeder writes, named by username.
var scenarioGroups = []scenarioGroup{
	{
		name:        "editors",
		displayName: "Editors",
		members:     []string{"robert_langdon", "sophie_neveu"},
	},
	{
		name:        "moderators",
		displayName: "Moderators",
		members:     []string{"hermione_granger"},
	},
	{
		name:        "archivists",
		displayName: "Archivists",
	},
}

// scenarioGroup is one group the seeder writes: the stable name, the label a
// UI renders, and the usernames that belong to it.
type scenarioGroup struct {
	name        string
	displayName string
	members     []string
}

// UserGroup returns the seeder for the development groups and their
// memberships. It runs after the user seeder: a membership names an account
// row.
func UserGroup() Seeder {
	return Seeder{
		Name:  UserGroupSeederName,
		Apply: applyUserGroups,
	}
}

// applyUserGroups creates the scenario groups and their memberships.
//
// The group insert is guarded by the name's unique index and the membership
// insert by the pair's primary key, so a second run keeps the existing rows
// and reports them as skipped. A member the database does not hold is not an
// error: the membership is the row the seeder cannot write, and the rest of
// the group still lands — a dry run reports the same shape.
func applyUserGroups(
	ctx context.Context,
	q datastore.Querier,
	dryRun bool,
) (created, skipped []string, err error) {
	for i := range scenarioGroups {
		group := scenarioGroups[i]

		groupID, inserted, groupErr := insertGroup(ctx, q, group, dryRun)
		if groupErr != nil {
			return nil, nil, groupErr
		}
		if inserted {
			created = append(created, group.name)
		} else {
			skipped = append(skipped, group.name)
		}

		for _, username := range group.members {
			written, memberErr := insertMembership(ctx, q, groupID, username, dryRun)
			if memberErr != nil {
				return nil, nil, memberErr
			}
			line := username + " in " + group.name
			if written {
				created = append(created, line)
			} else {
				skipped = append(skipped, line)
			}
		}
	}
	return created, skipped, nil
}

// insertGroup writes one group row and answers its identifier plus whether
// this run created it. A dry run answers a zero identifier — there is no row
// to join memberships against — and the membership writes plan themselves by
// existence checks instead.
func insertGroup(ctx context.Context, q datastore.Querier, group scenarioGroup, dryRun bool) (usergroup.GroupID, bool, error) {
	if dryRun {
		exists, err := groupExists(ctx, q, group.name)
		if err != nil {
			return usergroup.GroupID{}, false, err
		}
		return usergroup.GroupID{}, !exists, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(usergroup.GroupTable)
	ib.Cols("name", "display_name", "created_at")
	ib.Values(group.name, group.displayName, time.Now().UTC())
	// No conflict target: the group must not be created when its name is
	// already taken.
	ib.SQL("ON CONFLICT DO NOTHING")
	ib.Returning("id")

	query, args := ib.Build()
	var rawID string
	err := q.QueryRow(ctx, query, args...).Scan(&rawID)
	if errors.Is(err, pgx.ErrNoRows) {
		id, idErr := groupIDByName(ctx, q, group.name)
		if idErr != nil {
			return usergroup.GroupID{}, false, idErr
		}
		return id, false, nil
	}
	if err != nil {
		return usergroup.GroupID{}, false, fmt.Errorf("usergroup seeder: %s: %w", group.name, err)
	}
	parsed, err := uuid.Parse(rawID)
	if err != nil {
		return usergroup.GroupID{}, false, fmt.Errorf("usergroup seeder: %s: %w", group.name, err)
	}
	id, err := usergroup.IDFromUUID(parsed)
	if err != nil {
		return usergroup.GroupID{}, false, fmt.Errorf("usergroup seeder: %s: %w", group.name, err)
	}
	return id, true, nil
}

// insertMembership writes one membership row and answers whether this run
// created it. A dry run plans from the database: the membership is planned
// when the account exists and the pair does not. An account the database
// does not hold is skipped, on a real run and in a plan alike.
func insertMembership(ctx context.Context, q datastore.Querier, groupID usergroup.GroupID, username string, dryRun bool) (bool, error) {
	userID, err := userIDByName(ctx, q, username)
	if err != nil {
		return false, err
	}
	if userID == (uuid.UUID{}) {
		return false, nil
	}

	if dryRun {
		exists, existsErr := membershipExists(ctx, q, userID, groupID)
		if existsErr != nil {
			return false, existsErr
		}
		return !exists, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(usergroup.GroupMemberTable)
	ib.Cols("user_id", "user_group_id")
	ib.Values(userID, usergroup.IDToUUID(groupID))
	ib.SQL("ON CONFLICT DO NOTHING")

	query, args := ib.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("usergroup seeder: membership %s: %w", username, err)
	}
	return tag.RowsAffected() > 0, nil
}

// groupExists reports whether a group already carries the name.
func groupExists(ctx context.Context, q datastore.Querier, name string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1").From(usergroup.GroupTable).Where(sb.Equal("name", name))

	query, args := sb.Build()
	var one int
	err := q.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// groupIDByName reads the identifier of the group the name names.
func groupIDByName(ctx context.Context, q datastore.Querier, name string) (usergroup.GroupID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id").From(usergroup.GroupTable).Where(sb.Equal("name", name))

	query, args := sb.Build()
	var rawID string
	if err := q.QueryRow(ctx, query, args...).Scan(&rawID); err != nil {
		return usergroup.GroupID{}, fmt.Errorf("usergroup seeder: %s: %w", name, err)
	}
	parsed, err := uuid.Parse(rawID)
	if err != nil {
		return usergroup.GroupID{}, fmt.Errorf("usergroup seeder: %s: %w", name, err)
	}
	id, err := usergroup.IDFromUUID(parsed)
	if err != nil {
		return usergroup.GroupID{}, fmt.Errorf("usergroup seeder: %s: %w", name, err)
	}
	return id, nil
}

// userIDByName reads the identifier of the account the username names, or
// the zero UUID when no account answers — the value the membership write
// treats as "nothing to join".
func userIDByName(ctx context.Context, q datastore.Querier, username string) (uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id").From(user.UserTable).Where(sb.Equal("username", username))

	query, args := sb.Build()
	var rawID string
	err := q.QueryRow(ctx, query, args...).Scan(&rawID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, nil
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("usergroup seeder: %s: %w", username, err)
	}
	parsed, err := uuid.Parse(rawID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("usergroup seeder: %s: %w", username, err)
	}
	return parsed, nil
}

// membershipExists reports whether the pair is already joined.
func membershipExists(ctx context.Context, q datastore.Querier, userID uuid.UUID, groupID usergroup.GroupID) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1").From(usergroup.GroupMemberTable).
		Where(sb.Equal("user_id", userID), sb.Equal("user_group_id", usergroup.IDToUUID(groupID)))

	query, args := sb.Build()
	var one int
	err := q.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
