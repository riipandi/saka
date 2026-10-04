package router_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identityv1connect "github.com/riipandi/saka/codegen/proto/go/saka/identity/v1/identityv1connect"
	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/config"
	conttest "github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/internal/transport/router"
	"github.com/riipandi/saka/modules/identity/usergroup"
	"github.com/riipandi/saka/pkg/testutils"
)

// The fixture account, named after the test-copywriting convention.
const hermioneGroupMember = "01a0da3e-1111-7000-8000-000000000001"

// userGroupPool opens a migrated database with one account, so a membership
// has somewhere to land.
func userGroupPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator := conttest.TestMigrator(t, migrationDB)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "transport_usergroup_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(t.Context()) })

	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, first_name, last_name, display_name)
		VALUES ($1, $2::citext, $2::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger')`,
		hermioneGroupMember, "hermione")
	require.NoError(t, err)
	return pool
}

// newUserGroupRouter mounts the real group feature over the transport's
// guard, with the caller the test asks for.
func newUserGroupRouter(t *testing.T, auth router.Authenticator, pool *datastore.Postgres) http.Handler {
	t.Helper()

	service := usergroup.NewService(pool,
		fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	return router.NewRouter(router.Options{
		Config:        config.Default(),
		Checker:       health.NewChecker(),
		Authenticator: auth,
		Modules:       []kernel.Module{usergroup.NewModule(service)},
	})
}

// TestTheUserGroupGuardIsDeclared pins the six procedures' rule: every one is
// administrative, so a caller without a credential is refused as
// unauthenticated and a caller without the role is refused as not_found —
// the surface does not exist for them.
func TestTheUserGroupGuardIsDeclared(t *testing.T) {
	pool := userGroupPool(t)

	for name, tc := range map[string]struct {
		procedure string
		body      string
	}{
		"list":        {procedure: identityv1connect.UserGroupServiceListUserGroupsProcedure, body: `{}`},
		"get":         {procedure: identityv1connect.UserGroupServiceGetUserGroupProcedure, body: `{"id":"01a0da3e-1111-7000-8000-000000000002"}`},
		"create":      {procedure: identityv1connect.UserGroupServiceCreateUserGroupProcedure, body: `{"name":"gryffindor","display_name":"Gryffindor"}`},
		"update":      {procedure: identityv1connect.UserGroupServiceUpdateUserGroupProcedure, body: `{"id":"01a0da3e-1111-7000-8000-000000000002","name":"gryffindor","display_name":"Gryffindor"}`},
		"delete":      {procedure: identityv1connect.UserGroupServiceDeleteUserGroupProcedure, body: `{"id":"01a0da3e-1111-7000-8000-000000000002"}`},
		"set members": {procedure: identityv1connect.UserGroupServiceSetUserGroupMembersProcedure, body: `{"id":"01a0da3e-1111-7000-8000-000000000002","user_ids":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			for _, want := range map[string]struct {
				auth   router.Authenticator
				status int
				code   string
			}{
				"no credential": {auth: callerAuthenticator("", false, false), status: http.StatusUnauthorized, code: "unauthenticated"},
				"no role":       {auth: callerAuthenticator(wireID(t, hermioneGroupMember), false, false), status: http.StatusNotFound, code: "not_found"},
			} {
				router := newUserGroupRouter(t, want.auth, pool)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, rpcRequest(t, tc.procedure, tc.body))

				require.Equal(t, want.status, rec.Code, rec.Body.String())
				assert.Contains(t, rec.Body.String(), want.code)
			}
		})
	}
}

// TestTheUserGroupLoopEndsInAMemberList walks the flow an administrator
// performs: create the group, put an account in it, read the detail, replace
// the membership, and remove the group — every step answered over the real
// procedure, not a hand-built service.
func TestTheUserGroupLoopEndsInAMemberList(t *testing.T) {
	pool := userGroupPool(t)
	router := newUserGroupRouter(t, callerAuthenticator(wireID(t, hermioneGroupMember), true, false), pool)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserGroupServiceCreateUserGroupProcedure,
		`{"name":"gryffindor","display_name":"Gryffindor"}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var created struct {
		Group struct {
			ID string `json:"id"`
		} `json:"group"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.Group.ID)

	// The membership replace admits the fixture account, and the detail
	// that comes back names it.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserGroupServiceSetUserGroupMembersProcedure,
		`{"id":"`+created.Group.ID+`","user_ids":["`+wireID(t, hermioneGroupMember)+`"]}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var replaced struct {
		Group struct {
			UserCount int `json:"user_count"`
			Users     []struct {
				Username string `json:"username"`
			} `json:"users"`
		} `json:"group"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &replaced))
	assert.Equal(t, 1, replaced.Group.UserCount)
	assert.Equal(t, []string{"hermione"}, usernames(replaced.Group.Users))

	// The list answers the group with its count.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserGroupServiceListUserGroupsProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var listed struct {
		Groups []struct {
			Name      string `json:"name"`
			UserCount int    `json:"user_count"`
		} `json:"groups"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed.Groups, 1)
	assert.Equal(t, "gryffindor", listed.Groups[0].Name)
	assert.Equal(t, 1, listed.Groups[0].UserCount)

	// The deletion takes the membership with it; a second deletion answers
	// the not-found the identifier's emptiness is.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserGroupServiceDeleteUserGroupProcedure,
		`{"id":"`+created.Group.ID+`"}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserGroupServiceDeleteUserGroupProcedure,
		`{"id":"`+created.Group.ID+`"}`))
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

// usernames flattens the members' handles, for the membership assertions.
func usernames(users []struct {
	Username string `json:"username"`
}) []string {
	names := make([]string, 0, len(users))
	for _, member := range users {
		names = append(names, member.Username)
	}
	return names
}
