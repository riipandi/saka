package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/health"
	"github.com/riipandi/saka/internal/kernel"
	"github.com/riipandi/saka/internal/transport"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// stubAuthenticator accepts the one bearer token the tests carry and answers
// an administrator's claims; every other token, and an absent one, is refused.
func stubAuthenticator() transport.Authenticator {
	return func(ctx context.Context, req *http.Request) (any, error) {
		token, ok := authn.BearerToken(req)
		if !ok || token != "secret" {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
		return &jwtutils.Caller{
			UserID:       stubCallerWireID(),
			AccessClaims: jwtutils.AccessClaims{Username: "admin", Roles: []string{"administrator"}},
		}, nil
	}
}

// newRPCRouterWithAuth builds the router the way a run does, with the
// authenticator the composition root supplies.
func newRPCRouterWithAuth(t *testing.T, auth transport.Authenticator) http.Handler {
	t.Helper()

	checker := health.NewChecker(health.WithCheck(health.Check{
		Name: "database",
		Check: func(context.Context) error {
			return nil
		},
	}))
	return transport.NewRouter(transport.Options{
		Config:        config.Default(),
		Checker:       checker,
		Authenticator: auth,
		Modules:       []kernel.Module{&rpcFeature{}},
	})
}

func TestRPCRefusesACallWithoutAToken(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.test.v1.FeatureService/Ping", "{}"))

	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())

	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "unauthenticated", body.Code)
}

func TestRPCAcceptsTheBearerToken(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	req := rpcRequest(t, "/saka.test.v1.FeatureService/Ping", "{}")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"pong":true}`, rec.Body.String())
}

func TestRPCPublicProcedureNeedsNoToken(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.system.v1.HealthService/Check", "{}"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"healthy"`)
}

// TestRPCUnknownProcedureIsUnauthenticatedWithoutAToken pins the disclosure
// rule: a path no procedure claims is not named to a caller without a token —
// the answer is the refusal, not the unimplemented code.
func TestRPCUnknownProcedureIsUnauthenticatedWithoutAToken(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.system.v1.HealthService/Absent", "{}"))

	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.True(t, strings.Contains(rec.Body.String(), "unauthenticated"))
}

// TestRPCNilAuthenticatorLeavesTheSurfaceOpen pins the test state: a router
// without an authenticator answers everything, which is what the older tests
// rely on.
func TestRPCNilAuthenticatorLeavesTheSurfaceOpen(t *testing.T) {
	router := newRPCRouterWithAuth(t, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.test.v1.FeatureService/Ping", "{}"))

	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// stubCallerWireID is the stub caller's subject in the wire form the
// contracts carry, the shape the real issuer signs.
func stubCallerWireID() string {
	id, _ := user.IDFromUUIDString("01a0da1c-cb41-779d-bd02-99b3eb5da32a")
	return id.String()
}
