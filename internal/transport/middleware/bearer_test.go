package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/guard"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// authStub answers the caller the request's token names, and refuses
// everything else — the shape every real authenticator shares.
func authStub(refuse bool) Authenticator {
	return func(ctx context.Context, req *http.Request) (any, error) {
		if refuse {
			return nil, authn.Errorf("authentication required")
		}
		return &jwtutils.Caller{UserID: "01a0", AccessClaims: jwtutils.AccessClaims{Username: "hermione"}}, nil
	}
}

// TestRESTBearerProtectsByDefault keeps the rule the RPC surface's bearer
// middleware encodes: a route is protected unless it is named public, and the
// refusal is the REST envelope a REST caller reads.
func TestRESTBearerProtectsByDefault(t *testing.T) {
	for name, tc := range map[string]struct {
		method string
		path   string
		refuse bool
		code   int
	}{
		"public route":                          {http.MethodGet, "/api/users/01a0/profile-picture.png", false, http.StatusNoContent},
		"public route, other verb is protected": {http.MethodPut, "/api/users/01a0/profile-picture.png", true, http.StatusUnauthorized},
		"unlisted path":                         {http.MethodGet, "/api/users/01a0/avatar.png", true, http.StatusUnauthorized},
		"wildcard spans no more than one segment": {
			http.MethodGet, "/api/users/01a0/extra/profile-picture.png", true, http.StatusUnauthorized,
		},
	} {
		guarded := RESTBearer(authStub(tc.refuse), []guard.RestEntry{
			{Method: http.MethodGet, Pattern: "/api/users/{id}/profile-picture.png", Rule: guard.Public},
		})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, req)
		require.Equal(t, tc.code, rec.Code, name)
		if tc.code == http.StatusUnauthorized {
			assert.Contains(t, rec.Body.String(), "authentication required", name)
		}
	}
}

// TestRESTBearerWithoutAnAuthenticator covers the test state: a nil
// authenticator leaves the handler open, which is how a response-only test
// reads it.
func TestRESTBearerWithoutAnAuthenticator(t *testing.T) {
	handler := RESTBearer(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/anything", nil))
	assert.Equal(t, http.StatusTeapot, rec.Code)
}
