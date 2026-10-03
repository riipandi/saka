package user

import (
	"context"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

// stubDirectory is a GroupDirectory the account tests answer through
// without the group feature built: it records what the service attached
// and hands back the memberships a caller asks for, so the attach-in-
// transaction semantics and the view shaping are pinned without a second
// container.
type stubDirectory struct {
	groups   map[uuid.UUID][]GroupSummary
	attached []attachCall
}

type attachCall struct {
	userID uuid.UUID
	ids    []string
}

func (d *stubDirectory) GroupsOfUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]GroupSummary, error) {
	return d.groups[userID], nil
}

func (d *stubDirectory) GroupsOfUsers(ctx context.Context, db datastore.Querier, userIDs []uuid.UUID) (map[uuid.UUID][]GroupSummary, error) {
	out := make(map[uuid.UUID][]GroupSummary, len(userIDs))
	for _, id := range userIDs {
		out[id] = d.groups[id]
	}
	return out, nil
}

func (d *stubDirectory) AttachGroups(ctx context.Context, db datastore.Querier, userID uuid.UUID, groupIDs []string) error {
	d.attached = append(d.attached, attachCall{userID: userID, ids: groupIDs})
	return nil
}

func TestCreateUserOpensTheMembershipsItNames(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	directory := &stubDirectory{groups: map[uuid.UUID][]GroupSummary{}}
	service.WithGroups(directory)
	ctx := t.Context()

	created, err := service.CreateUser(ctx, CreateParams{
		Username:    "vittoria_vetra",
		Email:       "vittoria.vetra@infinite.bound",
		DisplayName: "Vittoria Vetra",
		GroupIDs:    []string{"ugrp_01h45ytscbexq2vgq6x8f6p3d4"},
	})
	require.NoError(t, err)
	require.Len(t, directory.attached, 1,
		"the creation carries the memberships the request named")
	assert.Equal(t, created.ID, FormatID(directory.attached[0].userID),
		"the memberships open for the account the request created")
	assert.Equal(t, []string{"ugrp_01h45ytscbexq2vgq6x8f6p3d4"}, directory.attached[0].ids)
}

func TestCreateUserWithoutGroupsAttachesNothing(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	directory := &stubDirectory{groups: map[uuid.UUID][]GroupSummary{}}
	service.WithGroups(directory)

	_, err := service.CreateUser(t.Context(), CreateParams{
		Username:    "robert_langdon",
		Email:       "robert.langdon@harvard.edu",
		DisplayName: "Robert Langdon",
	})
	require.NoError(t, err)
	assert.Empty(t, directory.attached,
		"a memberless creation opens no membership write")
}

func TestViewsCarryTheMembershipsTheDirectoryAnswers(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := t.Context()

	created, err := service.CreateUser(ctx, CreateParams{
		Username:    "hermione_granger",
		Email:       "hermione.granger@hogwarts.edu",
		DisplayName: "Hermione",
	})
	require.NoError(t, err)

	summary := GroupSummary{ID: "ugrp_01h45ytscbexq2vgq6x8f6p3d4", Name: "editors", DisplayName: "Editors"}
	directory := &stubDirectory{groups: map[uuid.UUID][]GroupSummary{IDToUUID(mustID(t, created.ID)): {summary}}}
	service.WithGroups(directory)

	read, err := service.GetUser(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, read.Groups, 1)
	assert.Equal(t, "editors", read.Groups[0].Name)

	list, _, err := service.ListUsers(ctx, "hermione", "", false, 1, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "editors", list[0].Groups[0].Name,
		"the page carries the memberships one batch read filled")
}

func TestGrouplessViewsAnswerWithoutTheDirectory(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.CreateUser(t.Context(), CreateParams{
		Username:    "hermione_granger",
		Email:       "hermione.granger@hogwarts.edu",
		DisplayName: "Hermione",
	})
	require.NoError(t, err)
	assert.Empty(t, created.Groups,
		"a service without the seam answers memberships empty, not an error")

	read, err := service.GetUser(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Empty(t, read.Groups)
}
