package usergroup

import (
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/modules/identity/user"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// The directory the account views read through: what one account belongs
// to, what many belong to, and which memberships a creation opens. These
// tests pin the user package's contract answered from this feature's
// tables.

func TestGroupsOfUserAnswersTheMembershipsOrderedByDisplayName(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "usergroup_test")
	service := testService(t, pool)
	ctx := t.Context()

	account := seedAccount(t, pool, "sophie_neveu")
	userID, err := user.UUIDFromWire(wireOf(t, account))
	require.NoError(t, err)

	alpha := mustCreateGroup(t, service, "alpha_editors", "Alpha Editors")
	beta := mustCreateGroup(t, service, "beta_reviewers", "Beta Reviewers")
	_, err = service.SetUserGroupMembers(t.Context(), alpha.ID.String(), []string{wireOf(t, account)})
	require.NoError(t, err)
	_, err = service.SetUserGroupMembers(t.Context(), beta.ID.String(), []string{wireOf(t, account)})
	require.NoError(t, err)

	directory := NewRepository()
	groups, err := directory.GroupsOfUser(ctx, pool, userID)
	require.NoError(t, err)
	require.Len(t, groups, 2)
	assert.Equal(t, "Alpha Editors", groups[0].DisplayName,
		"the memberships order by the display name, the way the group list does")
	assert.Equal(t, alpha.ID.String(), groups[0].ID)
}

func TestGroupsOfUsersAnswersAPageInOneRead(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "usergroup_test")
	service := testService(t, pool)
	ctx := t.Context()

	firstWire := seedAccount(t, pool, "hermione_granger")
	secondWire := seedAccount(t, pool, "vittoria_vetra")
	first, err := user.UUIDFromWire(wireOf(t, firstWire))
	require.NoError(t, err)
	second, err := user.UUIDFromWire(wireOf(t, secondWire))
	require.NoError(t, err)

	group := mustCreateGroup(t, service, "editors", "Editors")
	_, err = service.SetUserGroupMembers(t.Context(), group.ID.String(), []string{wireOf(t, firstWire)})
	require.NoError(t, err)

	directory := NewRepository()
	byUser, err := directory.GroupsOfUsers(ctx, pool, []uuid.UUID{first, second})
	require.NoError(t, err)
	require.Len(t, byUser[first], 1)
	assert.Equal(t, "editors", byUser[first][0].Name)
	assert.Empty(t, byUser[second],
		"an account without memberships answers none, not an absence")
}

func TestAttachGroupsOpensAndRefusesTheMemberships(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "usergroup_test")
	service := testService(t, pool)
	ctx := t.Context()

	accountWire := seedAccount(t, pool, "robert_langdon")
	userID, err := user.UUIDFromWire(wireOf(t, accountWire))
	require.NoError(t, err)
	group := mustCreateGroup(t, service, "editors", "Editors")

	directory := NewRepository()
	require.NoError(t, directory.AttachGroups(ctx, pool, userID, []string{group.ID.String()}))

	groups, err := directory.GroupsOfUser(ctx, pool, userID)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, group.ID.String(), groups[0].ID)

	err = directory.AttachGroups(ctx, pool, userID, []string{"ugrp_01h45ytscbexq2vgq6x8f6p3d4"})
	assert.ErrorIs(t, err, user.ErrGroupUnknown,
		"a creation naming a group the deployment does not hold is refused, not half-applied")

	err = directory.AttachGroups(ctx, pool, userID, []string{"not-a-group-id"})
	assert.ErrorIs(t, err, user.ErrGroupUnknown,
		"a malformed identifier names no group, the not-found the seam reports")
}

func mustCreateGroup(t *testing.T, service *Service, name, displayName string) GroupSchema {
	t.Helper()

	group, err := service.CreateUserGroup(t.Context(), CreateParams{
		Name:        name,
		DisplayName: displayName,
	})
	require.NoError(t, err)
	return group.GroupSchema
}
