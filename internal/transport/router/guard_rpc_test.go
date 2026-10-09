package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identityv1connect "github.com/riipandi/saka/codegen/proto/go/saka/identity/v1/identityv1connect"
	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/transport/router"
	"github.com/riipandi/saka/modules/identity/session"
	"github.com/riipandi/saka/modules/identity/signup"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// callerAuthenticator answers the caller the request's token names, so a test
// can present any principal without minting a token: the subject is the
// account the request runs as, and `impersonated` adds the delegation an
// administrator's session carries.
func callerAuthenticator(subject string, admin, impersonated bool) router.Authenticator {
	return func(ctx context.Context, req *http.Request) (any, error) {
		if subject == "" {
			return nil, connect.NewError(connect.CodeUnauthenticated, "authentication required")
		}
		claims := jwtutils.AccessClaims{Username: subject, Roles: adminRoles(admin)}
		if impersonated {
			claims.ActorID = "01a0da1c-cb41-779d-bd02-99b3eb5da32a"
			claims.ActorUsername = "admin"
		}
		return &jwtutils.Caller{UserID: subject, AccessClaims: claims}, nil
	}
}

// newGuardedRouter mounts the account and signup features over the guard the
// transport installs, with the caller the test asks for.
func newGuardedRouter(t *testing.T, auth router.Authenticator) http.Handler {
	t.Helper()

	return router.NewRouter(router.Options{
		Config:        config.Default(),
		Checker:       health.NewChecker(),
		Authenticator: auth,
		Modules: []kernel.Module{
			// The services are nil on purpose: every case below is refused by
			// the guard, so a request that reached a service would panic and
			// fail the test rather than pass it.
			signup.NewModule(nil),
			user.NewModule(nil),
			session.NewModule(nil),
		},
	})
}

// TestTheGuardRefusesAnAdministrativeProcedureWithoutTheRole is the rule the
// inline checks used to carry, now enforced once for the surface: a caller
// who is authenticated but not an administrator is refused before any service
// runs.
func TestTheGuardRefusesAnAdministrativeProcedureWithoutTheRole(t *testing.T) {
	router := newGuardedRouter(t, callerAuthenticator("01a0da1c-cb41-779d-bd02-99b3eb5da32a", false, false))

	for name, tc := range map[string]struct {
		procedure string
		body      string
	}{
		"list users":  {identityv1connect.UserServiceListUsersProcedure, `{}`},
		"get user":    {identityv1connect.UserServiceGetUserProcedure, `{"id":"01a0da1c-cb41-779d-bd02-99b3eb5da32a"}`},
		"delete user": {identityv1connect.UserServiceDeleteUserProcedure, `{"id":"01a0da1c-cb41-779d-bd02-99b3eb5da32a"}`},
		// A body the contract would refuse, and a body it accepts: the answer
		// is the same, because authorization runs before the body is judged.
		"create token":               {identityv1connect.SignupServiceCreateSignupTokenProcedure, `{"ttl_seconds":86400}`},
		"create token, invalid body": {identityv1connect.SignupServiceCreateSignupTokenProcedure, `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequest(t, tc.procedure, tc.body))

			// The refusal is not_found, the shape that discloses least: a
			// caller without the role cannot tell an administrative procedure
			// from an absent one.
			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "not_found")
		})
	}
}

// TestTheGuardAnswersAnAdministrativeProcedureForAnAdministrator is the
// allowed half: the caller passes the guard, and what happens next is the
// service's business — here a nil service, which the recovery boundary turns
// into an internal error rather than a refusal.
func TestTheGuardAnswersAnAdministrativeProcedureForAnAdministrator(t *testing.T) {
	router := newGuardedRouter(t, callerAuthenticator("01a0da1c-cb41-779d-bd02-99b3eb5da32a", true, false))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserServiceListUsersProcedure, `{}`))

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "internal", body.Code)
}

// TestTheGuardRefusesASelfProcedureForAnotherAccount covers the self rule on
// the RPC surface: the account the request names must be the caller's own,
// and an administrator does not pass by virtue of the role.
func TestTheGuardRefusesASelfProcedureForAnotherAccount(t *testing.T) {
	const other = "01a0da1c-0000-7000-8000-000000000000"

	for name, tc := range map[string]struct {
		subject string
		admin   bool
	}{
		"a stranger":          {subject: other, admin: false},
		"an administrator":    {subject: other, admin: true},
		"an impersonated one": {subject: other, admin: true},
	} {
		t.Run(name, func(t *testing.T) {
			router := newGuardedRouter(t, callerAuthenticator(tc.subject, tc.admin, tc.admin))

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserServiceResetProfilePictureProcedure,
				`{"id":"01a0da1c-cb41-779d-bd02-99b3eb5da32a"}`))

			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "not_found")
		})
	}
}

// TestTheGuardAnswersASelfProcedureForItsOwnAccount keeps the allowed case
// reachable: the caller is the account the request names.
func TestTheGuardAnswersASelfProcedureForItsOwnAccount(t *testing.T) {
	const self = "01a0da1c-cb41-779d-bd02-99b3eb5da32a"
	router := newGuardedRouter(t, callerAuthenticator(wireID(t, self), false, false))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserServiceResetProfilePictureProcedure,
		`{"id":"`+wireID(t, self)+`"}`))

	// The guard allowed it; the nil service is what fails, which is the
	// proof the request got past the rule.
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
}

// TestTheGuardRefusesAnImpersonatedCallerOnASelfProcedure is the delegation
// rule: acting for another account does not grant the requests that belong to
// the account, even when the subject matches.
func TestTheGuardRefusesAnImpersonatedCallerOnASelfProcedure(t *testing.T) {
	const self = "01a0da1c-cb41-779d-bd02-99b3eb5da32a"
	router := newGuardedRouter(t, callerAuthenticator(self, true, true))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserServiceResetProfilePictureProcedure,
		`{"id":"`+self+`"}`))

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

// TestTheGuardRefusesAnUnauthenticatedCaller keeps the first answer a rule
// gives: with no credential the reply is unauthenticated, because presenting
// one is what the caller can do about it.
func TestTheGuardRefusesAnUnauthenticatedCaller(t *testing.T) {
	router := newGuardedRouter(t, callerAuthenticator("", false, false))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.UserServiceListUsersProcedure, `{}`))

	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "unauthenticated")
}

// TestTheGuardLeavesThePublicProceduresAlone pins the derivation: a procedure
// the table marks public is answered with no caller at all.
func TestTheGuardLeavesThePublicProceduresAlone(t *testing.T) {
	router := newGuardedRouter(t, callerAuthenticator("", false, false))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, identityv1connect.SignupServiceSignupProcedure,
		`{"username":"hermione","email":"hermione@example.com","password":"expecto-patronum","token":"elder-wand","first_name":"Hermione","last_name":"Granger"}`))

	// The guard allowed it and the nil service failed: not a refusal.
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
}
