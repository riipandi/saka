package usergroup

import (
	"log/slog"
	"testing"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jetify.com/typeid"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/modules/identity/user"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// migratedPool opens a database the migrations have built, so the group
// tables exist.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "usergroup_test")
}

// testService builds the service with a recorder that writes for real — a
// record is part of the transaction it describes, so the assertions read the
// table the writer fills.
func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	return NewService(pool, fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
}

// seedAccount inserts an account directly, the way a fixture does, and
// answers its identifier. A member must exist before it can be named.
// wireOf renders an account's row identifier in the wire form the member
// list carries, the shape the request speaks.
func wireOf(t *testing.T, raw string) string {
	t.Helper()
	id, err := user.IDFromUUIDString(raw)
	require.NoError(t, err)
	return id.String()
}

func seedAccount(t *testing.T, pool *datastore.Postgres, username string) string {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (username, email, first_name, last_name, display_name)
		VALUES ($1::citext, $1::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger')`,
		username)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TableUsers)
	sb.Where(sb.Equal("username", username))

	query, args := sb.Build()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// membershipCount reads how many accounts belong to a group, straight from
// the junction table the foreign keys guard.
func membershipCount(t *testing.T, pool *datastore.Postgres, groupID string) int {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TableUserGroupsUsers)
	sb.Where(sb.Equal("user_group_id", groupID))

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}

func TestCreateGroupStoresTheRowAndRefusesADuplicateName(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.CreateUserGroup(t.Context(), CreateParams{
		Name:        "gryffindor",
		DisplayName: "Gryffindor",
	})
	require.NoError(t, err)
	assert.Equal(t, "gryffindor", created.Name)
	assert.Equal(t, "Gryffindor", created.DisplayName)
	assert.Equal(t, 0, created.UserCount, "a new group holds no member")
	assert.NotEmpty(t, created.ID)
	assert.Nil(t, created.UpdatedAt, "a row never updated carries no update instant")

	// The name is unique by index, and the failure is the duplicate the
	// caller is told about, not an internal error.
	_, err = service.CreateUserGroup(t.Context(), CreateParams{
		Name:        "gryffindor",
		DisplayName: "Another Gryffindor",
	})
	assert.ErrorIs(t, err, ErrGroupExists)

	// The record of the creation is in the log, and it names the group.
	assert.Equal(t, 1, auditCount(t, pool, audit.EventGroupCreated, rowID(t, created.ID)))
}

func TestListGroupsSearchesPaginatesAndSorts(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	gryffindor := created(t, service, "gryffindor", "Gryffindor")
	slytherin := created(t, service, "slytherin", "Slytherin")
	created(t, service, "ravenclaw", "Ravenclaw")

	// The member counts travel with the rows: one group holds two accounts,
	// another one, the last none.
	hermione := seedAccount(t, pool, "hermione")
	ron := seedAccount(t, pool, "ron")
	_, err := service.SetUserGroupMembers(t.Context(), gryffindor.ID.String(), []string{wireOf(t, hermione), wireOf(t, ron)})
	require.NoError(t, err)
	_, err = service.SetUserGroupMembers(t.Context(), slytherin.ID.String(), []string{wireOf(t, hermione)})
	require.NoError(t, err)

	// The default order is the display name, ascending.
	page, pagination, err := service.ListGroups(t.Context(), "", "", true, 1, 10)
	require.NoError(t, err)
	require.Len(t, page, 3)
	assert.Equal(t, "Gryffindor", page[0].DisplayName)
	assert.Equal(t, "Ravenclaw", page[1].DisplayName)
	assert.Equal(t, "Slytherin", page[2].DisplayName)
	require.NotNil(t, pagination.TotalItems)
	assert.Equal(t, 3, *pagination.TotalItems)

	// The count rides the row: two, one, zero in the page above.
	assert.Equal(t, 2, page[0].UserCount)
	assert.Equal(t, 0, page[1].UserCount)
	assert.Equal(t, 1, page[2].UserCount)

	// The member count is sortable, descending first.
	page, _, err = service.ListGroups(t.Context(), "", "user_count", false, 1, 10)
	require.NoError(t, err)
	require.Len(t, page, 3)
	assert.Equal(t, 2, page[0].UserCount)

	// The name is sortable, and case-insensitively: an upper-case display
	// name does not push its row past every lower-case one.
	page, _, err = service.ListGroups(t.Context(), "", "name", true, 1, 10)
	require.NoError(t, err)
	require.Len(t, page, 3)
	assert.Equal(t, "gryffindor", page[0].Name)

	// The search matches both name columns and filters the page.
	page, pagination, err = service.ListGroups(t.Context(), "slyth", "", true, 1, 10)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, slytherin.ID.String(), page[0].ID.String())
	assert.Equal(t, 1, *pagination.TotalItems)
}

func TestUpdateGroupReplacesTheFieldsAndRefusesADuplicate(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	first := created(t, service, "gryffindor", "Gryffindor")
	second := created(t, service, "slytherin", "Slytherin")

	updated, err := service.UpdateUserGroup(t.Context(), first.ID.String(), CreateParams{
		Name:        "gryffindor-house",
		DisplayName: "Gryffindor House",
	})
	require.NoError(t, err)
	assert.Equal(t, "gryffindor-house", updated.Name)
	assert.Equal(t, "Gryffindor House", updated.DisplayName)
	assert.NotNil(t, updated.UpdatedAt, "an updated row carries its update instant")

	// A name another group holds is refused, and the refused write leaves
	// the other group intact.
	_, err = service.UpdateUserGroup(t.Context(), first.ID.String(), CreateParams{
		Name:        "slytherin",
		DisplayName: "Not Slytherin",
	})
	assert.ErrorIs(t, err, ErrGroupExists)
	intact, err := service.GetGroup(t.Context(), second.ID.String())
	require.NoError(t, err)
	assert.Equal(t, "Slytherin", intact.DisplayName)

	// An unknown identifier is the not-found failure.
	_, err = service.UpdateUserGroup(t.Context(), "01a0da3e-1111-7000-8000-000000000009", CreateParams{
		Name:        "hufflepuff",
		DisplayName: "Hufflepuff",
	})
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestDeleteGroupRemovesTheMemberships(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	group := created(t, service, "gryffindor", "Gryffindor")
	hermione := seedAccount(t, pool, "hermione")
	_, err := service.SetUserGroupMembers(t.Context(), group.ID.String(), []string{wireOf(t, hermione)})
	require.NoError(t, err)

	require.NoError(t, service.DeleteUserGroup(t.Context(), group.ID.String()))

	// The junction rows died with the group by the cascade, and the account
	// itself is untouched.
	assert.Equal(t, 0, membershipCount(t, pool, rowID(t, group.ID)))
	assert.Equal(t, 1, auditCount(t, pool, audit.EventGroupDeleted, rowID(t, group.ID)))

	_, err = service.GetGroup(t.Context(), group.ID.String())
	assert.ErrorIs(t, err, ErrGroupNotFound)

	// A second deletion names nothing, which is the not-found failure.
	assert.ErrorIs(t, service.DeleteUserGroup(t.Context(), group.ID.String()), ErrGroupNotFound)
}

func TestSetMembersReplacesTheWholeSet(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	group := created(t, service, "gryffindor", "Gryffindor")
	hermione := seedAccount(t, pool, "hermione")
	ron := seedAccount(t, pool, "ron")
	neville := seedAccount(t, pool, "neville")

	// Two members in, one different member in: the replacement is the whole
	// set, not a delta, so the answer is one member and the junction holds
	// one row.
	_, err := service.SetUserGroupMembers(t.Context(), group.ID.String(), []string{wireOf(t, hermione), wireOf(t, ron)})
	require.NoError(t, err)
	updated, err := service.SetUserGroupMembers(t.Context(), group.ID.String(), []string{wireOf(t, neville)})
	require.NoError(t, err)
	assert.Equal(t, 1, updated.UserCount)
	require.Len(t, updated.Members, 1)
	assert.Equal(t, "neville", updated.Members[0].Username)
	assert.Equal(t, 1, membershipCount(t, pool, rowID(t, group.ID)))

	// A member that does not exist refuses the replacement whole: the set
	// the group holds is the one it held before.
	_, err = service.SetUserGroupMembers(t.Context(), group.ID.String(), []string{wireOf(t, hermione), "01a0da3e-1111-7000-8000-000000000009"})
	assert.ErrorIs(t, err, ErrMemberNotFound)
	unchanged, err := service.GetGroup(t.Context(), group.ID.String())
	require.NoError(t, err)
	assert.Equal(t, 1, unchanged.UserCount)

	// The empty list empties the group — a group may be emptied.
	_, err = service.SetUserGroupMembers(t.Context(), group.ID.String(), nil)
	require.NoError(t, err)
	assert.Equal(t, 0, membershipCount(t, pool, rowID(t, group.ID)))
	assert.Equal(t, 3, auditCount(t, pool, audit.EventGroupMembersUpdated, rowID(t, group.ID)))
}

// created is a fixture group with a fixed display name, for the tests that
// need more than one.
func created(t *testing.T, service *Service, name, displayName string) GroupDetailView {
	t.Helper()

	group, err := service.CreateUserGroup(t.Context(), CreateParams{
		Name:        name,
		DisplayName: displayName,
	})
	require.NoError(t, err)
	return group
}

// auditCount reads how many records the log holds of one event about one
// group.
// rowID unwraps the typed identifier the views carry into the UUID string
// the rows and the audit columns key on.
func rowID(t *testing.T, id GroupID) string {
	t.Helper()
	return id.UUID()
}

// userAuditCount reads how many records the log holds of one event about one
// account — the per-user membership change names its account, not a group.
func userAuditCount(t *testing.T, pool *datastore.Postgres, event, userID string) int {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TableAuditLogs)
	sb.Where(sb.Equal("event", event), sb.Equal("user_id", userID))

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}

// TestGetUserGroupsAnswersTheAccountSMembership reads the groups back from
// the per-user direction, ordered by display name, after the memberships
// were written from the per-group one. An account in no group answers an
// empty set, not an error.
func TestGetUserGroupsAnswersTheAccountSMembership(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	gryffindor := created(t, service, "gryffindor", "Gryffindor")
	slytherin := created(t, service, "slytherin", "Slytherin")

	hermione := seedAccount(t, pool, "hermione")
	_, err := service.SetUserGroupMembers(t.Context(), slytherin.ID.String(), []string{wireOf(t, hermione)})
	require.NoError(t, err)

	groups, err := service.GetUserGroups(t.Context(), wireOf(t, hermione))
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "slytherin", groups[0].Name)

	// The replace from the other direction, then the read again: the two
	// directions share the same junction table.
	_, err = service.UpdateUserGroups(t.Context(), wireOf(t, hermione), []string{gryffindor.ID.String()})
	require.NoError(t, err)
	groups, err = service.GetUserGroups(t.Context(), wireOf(t, hermione))
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "gryffindor", groups[0].Name)

	ron := seedAccount(t, pool, "ron")
	groups, err = service.GetUserGroups(t.Context(), wireOf(t, ron))
	require.NoError(t, err)
	assert.Empty(t, groups)
}

// TestUpdateUserGroupsReplacesAndRefusesTheUnknown pins the replace: the
// request names the whole set, an empty list ungroups the account, and a
// group that does not exist refuses the call whole — a membership is not
// created into nothing.
func TestUpdateUserGroupsReplacesAndRefusesTheUnknown(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	gryffindor := created(t, service, "gryffindor", "Gryffindor")
	slytherin := created(t, service, "slytherin", "Slytherin")

	hermione := seedAccount(t, pool, "hermione")
	hermioneWire := wireOf(t, hermione)

	groups, err := service.UpdateUserGroups(t.Context(), hermioneWire, []string{slytherin.ID.String(), gryffindor.ID.String()})
	require.NoError(t, err)
	require.Len(t, groups, 2)

	// An empty list is the ungrouped state.
	groups, err = service.UpdateUserGroups(t.Context(), hermioneWire, nil)
	require.NoError(t, err)
	assert.Empty(t, groups)

	// A group that does not exist refuses the whole replace.
	unknown, err := usergroupUnknownID(t)
	require.NoError(t, err)
	_, err = service.UpdateUserGroups(t.Context(), hermioneWire, []string{unknown})
	assert.ErrorIs(t, err, ErrGroupNotFound)

	// An account that does not exist is the same refusal from the other end.
	_, err = service.UpdateUserGroups(t.Context(), "user_00000000000000000000000000", nil)
	assert.ErrorIs(t, err, ErrMemberNotFound)

	// The record of the change names the account, not a group.
	assert.Equal(t, 2, userAuditCount(t, pool, audit.EventUserGroupsUpdated, hermione))
}

// usergroupUnknownID mints a well-formed group identifier no row answers,
// for the refusal boundary.
func usergroupUnknownID(t *testing.T) (string, error) {
	t.Helper()
	id, err := typeid.New[GroupID]()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func auditCount(t *testing.T, pool *datastore.Postgres, event, groupID string) int {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TableAuditLogs)
	sb.Where(sb.Equal("event", event), sb.Equal("resource_id", groupID))

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}
