package authorization

import (
	"log/slog"
	"slices"
	"strings"
	"testing"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jetify.com/typeid"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/testutils"
)

// migratedPool opens a database the migrations have built, so the
// authorization tables exist.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "authorization_test")
}

// testService builds the service with a recorder that writes for real — a
// record is part of the transaction it describes, so the assertions read the
// table the writer fills.
func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	return NewService(pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), nil)
}

// seedCatalog writes the permission catalog and the system roles, the way
// the seed does, so the service has the rows its procedures reference.
func seedCatalog(t *testing.T, pool *datastore.Postgres) {
	t.Helper()

	for _, permission := range authz.Catalog() {
		_, err := pool.Exec(t.Context(), `
			INSERT INTO public.permissions (slug, description) VALUES ($1, $2)
			ON CONFLICT (slug) DO NOTHING`, permission.Slug, permission.Description)
		require.NoError(t, err)
	}
	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.roles (name, slug, description, type)
		VALUES ('Administrator', 'administrator', 'Full access.', 'system')
		ON CONFLICT (slug) DO NOTHING`)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From("public.roles")
	sb.Where(sb.Equal("slug", authz.AdministratorRole))

	query, args := sb.Build()
	var roleID string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&roleID))

	// The insert rides a select over the permissions table, so the whole
	// catalog grants in one statement. It is a test fixture, where raw SQL
	// is the house rule.
	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.role_permissions (role_id, permission_id)
		SELECT $1::uuid, p.id FROM public.permissions p
		ON CONFLICT DO NOTHING`, roleID)
	require.NoError(t, err)
}

// seedAccount inserts an account and answers its wire identifier.
func seedAccount(t *testing.T, pool *datastore.Postgres, username string) string {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (username, email, first_name, last_name, display_name)
		VALUES ($1::citext, $1::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger')`,
		username)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From("public.users")
	sb.Where(sb.Equal("username", username))

	query, args := sb.Build()
	var rawID string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&rawID))
	id, err := user.IDFromUUIDString(rawID)
	require.NoError(t, err)
	return id.String()
}

// seedRole writes a custom role and answers its wire identifier.
func seedRole(t *testing.T, pool *datastore.Postgres, name, slug string) string {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.roles (name, slug, type) VALUES ($1, $2, 'custom')`,
		name, slug)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From("public.roles")
	sb.Where(sb.Equal("slug", slug))

	query, args := sb.Build()
	var rawID string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&rawID))
	return roleIDFromString(t, rawID)
}

func TestListPermissionsAnswersTheCatalog(t *testing.T) {
	pool := migratedPool(t)
	seedCatalog(t, pool)
	service := testService(t, pool)

	catalog, err := service.ListPermissions(t.Context(), false, "", "", "", true)
	require.NoError(t, err)

	expected := authz.Catalog()
	require.Len(t, catalog, len(expected))
	seen := make(map[string]string, len(catalog))
	for _, entry := range catalog {
		assert.NotEmpty(t, entry.ID.String(), "permission id")
		assert.True(t, strings.HasPrefix(entry.ID.String(), "perm_"), entry.ID.String())
		seen[entry.Slug] = entry.Description
	}
	for _, permission := range expected {
		assert.Equal(t, permission.Description, seen[permission.Slug])
	}
}

func TestListPermissionsNarrowsAndOrders(t *testing.T) {
	pool := migratedPool(t)
	seedCatalog(t, pool)
	service := testService(t, pool)

	roles, err := service.ListPermissions(t.Context(), false, "", "role", "", true)
	require.NoError(t, err)
	require.NotEmpty(t, roles)
	for _, entry := range roles {
		assert.True(t, strings.HasPrefix(entry.Slug, "role:"), entry.Slug)
	}

	found, err := service.ListPermissions(t.Context(), false, "delete", "", "", true)
	require.NoError(t, err)
	require.NotEmpty(t, found)
	for _, entry := range found {
		assert.Contains(t, entry.Slug+entry.Description, "delete")
	}

	bySlug, err := service.ListPermissions(t.Context(), false, "", "", "slug", true)
	require.NoError(t, err)
	for i := 1; i < len(bySlug); i++ {
		assert.LessOrEqual(t, bySlug[i-1].Slug, bySlug[i].Slug)
	}

	descending, err := service.ListPermissions(t.Context(), false, "", "", "slug", false)
	require.NoError(t, err)
	slices.Reverse(bySlug)
	require.Equal(t, bySlug, descending)
}

func TestListRolesNarrowsByKind(t *testing.T) {
	pool := migratedPool(t)
	seedCatalog(t, pool)
	service := testService(t, pool)
	seedRole(t, pool, "Content Editor", "content-editor")

	custom, pagination, err := service.ListRoles(t.Context(), false, "", RoleTypeCustom, "", true, 1, 100)
	require.NoError(t, err)
	require.NotNil(t, pagination.TotalItems)
	require.Equal(t, 1, *pagination.TotalItems)
	require.Len(t, custom, 1)
	assert.Equal(t, "content-editor", custom[0].Slug)

	every, pagination, err := service.ListRoles(t.Context(), false, "", "", "", true, 1, 100)
	require.NoError(t, err)
	require.NotNil(t, pagination.TotalItems)
	require.Equal(t, 2, *pagination.TotalItems)
	require.Len(t, every, 2)
}

func TestCreateRoleNamesASlugOnce(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.CreateRole(t.Context(), CreateParams{
		Name:        "Content Editor",
		Slug:        "content-editor",
		Description: "Edits the content",
	})
	require.NoError(t, err)
	assert.Equal(t, "content-editor", created.Slug)
	assert.Equal(t, RoleTypeCustom, created.Type)
	assert.Empty(t, created.Permissions, "a role is born with no permissions")

	_, err = service.CreateRole(t.Context(), CreateParams{Name: "Another", Slug: "content-editor"})
	assert.ErrorIs(t, err, ErrRoleExists)

	_, err = service.CreateRole(t.Context(), CreateParams{Name: "Content Editor", Slug: "other"})
	assert.ErrorIs(t, err, ErrRoleExists, "the name is unique too")
}

func TestUpdateAndDeleteRefuseASystemRole(t *testing.T) {
	pool := migratedPool(t)
	seedCatalog(t, pool)
	service := testService(t, pool)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From("public.roles")
	sb.Where(sb.Equal("slug", authz.AdministratorRole))

	query, args := sb.Build()
	var rawID string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&rawID))
	roleWire := roleIDFromString(t, rawID)

	_, err := service.UpdateRole(t.Context(), roleWire, CreateParams{Name: "Renamed"})
	assert.ErrorIs(t, err, ErrSystemRole)

	err = service.DeleteRole(t.Context(), roleWire)
	assert.ErrorIs(t, err, ErrSystemRole)
}

func TestDeleteRoleRefusesARoleAccountsStillHold(t *testing.T) {
	pool := migratedPool(t)
	seedCatalog(t, pool)
	service := testService(t, pool)

	roleID := seedRole(t, pool, "Viewer", "viewer")
	accountID := seedAccount(t, pool, "hermione")

	_, err := service.SetUserRoles(t.Context(), accountID, []string{roleID}, "")
	require.NoError(t, err)

	err = service.DeleteRole(t.Context(), roleID)
	assert.ErrorIs(t, err, ErrRoleInUse)

	// Once the grant is lifted, the deletion answers.
	_, err = service.SetUserRoles(t.Context(), accountID, nil, "")
	require.NoError(t, err)
	require.NoError(t, service.DeleteRole(t.Context(), roleID))
}

func TestSetRolePermissionsReplacesTheSet(t *testing.T) {
	pool := migratedPool(t)
	seedCatalog(t, pool)
	service := testService(t, pool)

	roleID := seedRole(t, pool, "Auditor", "auditor")

	updated, err := service.SetRolePermissions(t.Context(), roleID, []string{
		"audit_log:*:read", "audit_log:*:list",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"audit_log:*:list", "audit_log:*:read"}, updated.Permissions)

	_, err = service.SetRolePermissions(t.Context(), roleID, []string{"made_up:*:read"})
	assert.ErrorIs(t, err, ErrPermissionNotFound, "a slug the catalog does not carry grants nothing")
}

func TestSetUserRolesRevokesAndRevives(t *testing.T) {
	pool := migratedPool(t)
	seedCatalog(t, pool)
	service := testService(t, pool)

	accountID := seedAccount(t, pool, "hermione")
	editor := seedRole(t, pool, "Editor", "editor")
	auditor := seedRole(t, pool, "Auditor", "auditor")

	roles, err := service.SetUserRoles(t.Context(), accountID, []string{editor, auditor}, "")
	require.NoError(t, err)
	require.Len(t, roles, 2)

	roles, err = service.SetUserRoles(t.Context(), accountID, []string{editor}, "")
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, "editor", roles[0].Slug)

	// The re-grant opens a fresh row: the revoked one stays history.
	roles, err = service.SetUserRoles(t.Context(), accountID, []string{editor, auditor}, "")
	require.NoError(t, err)
	require.Len(t, roles, 2)

	var history int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.user_roles WHERE user_id = $1 AND revoked_at IS NOT NULL",
		userIDOf(t, pool, accountID)).Scan(&history))
	assert.Equal(t, 1, history, "one revoked grant must remain as history")

	_, err = service.SetUserRoles(t.Context(), accountID, []string{wireOfUnknownRole(t)}, "")
	assert.ErrorIs(t, err, ErrRoleNotFound)
}

func TestSetUserPermissionsReplacesTheDirectGrants(t *testing.T) {
	pool := migratedPool(t)
	seedCatalog(t, pool)
	service := testService(t, pool)

	accountID := seedAccount(t, pool, "hermione")

	granted, err := service.SetUserPermissions(t.Context(), accountID, []string{"user:*:ban"}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"user:*:ban"}, granted)

	granted, err = service.SetUserPermissions(t.Context(), accountID, []string{"notification:*:create"}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"notification:*:create"}, granted)

	_, err = service.SetUserPermissions(t.Context(), accountID, []string{"made_up:*:read"}, "")
	assert.ErrorIs(t, err, ErrPermissionNotFound)

	// The claims the account's next token would carry answer through the
	// loader the token mints share: roles none, the direct grant only.
	roles, permissions, err := user.LoadGrants(t.Context(), pool, userIDOf(t, pool, accountID))
	require.NoError(t, err)
	assert.Empty(t, roles)
	assert.Equal(t, []string{"notification:*:create"}, permissions)
}

func TestUserProceduresRefuseAnUnknownAccount(t *testing.T) {
	pool := migratedPool(t)
	seedCatalog(t, pool)
	service := testService(t, pool)

	_, err := service.ListUserRoles(t.Context(), "usr_01a0e2000000000000000000000")
	assert.ErrorIs(t, err, ErrUserNotFound)

	_, err = service.SetUserRoles(t.Context(), "usr_01a0e2000000000000000000000", nil, "")
	assert.ErrorIs(t, err, ErrUserNotFound)

	_, err = service.ListUserPermissions(t.Context(), "usr_01a0e2000000000000000000000")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// wireOfUnknownRole builds a well-formed role identifier that names no row:
// the not-found the per-role procedures answer.
func wireOfUnknownRole(t *testing.T) string {
	t.Helper()

	id, err := typeid.New[RoleID]()
	require.NoError(t, err)
	return id.String()
}

// roleIDFromString reads the row's identifier out of the UUID string the
// column stores, and answers it in the wire form.
func roleIDFromString(t *testing.T, raw string) string {
	t.Helper()

	parsed, err := uuid.Parse(raw)
	require.NoError(t, err)
	id, err := IDFromUUID(parsed)
	require.NoError(t, err)
	return id.String()
}

// userIDOf reads the row's UUID out of the account's wire form.
func userIDOf(t *testing.T, pool *datastore.Postgres, wire string) uuid.UUID {
	t.Helper()

	id, err := user.UUIDFromWire(wire)
	require.NoError(t, err)
	return id
}
