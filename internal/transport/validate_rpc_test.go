package transport_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/health"
	"github.com/riipandi/saka/internal/kernel"
	"github.com/riipandi/saka/internal/transport"
	"github.com/riipandi/saka/modules/identity/signup"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/modules/identity/verification"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// The declarative constraints live in the contracts, and the validate
// interceptor the handler options carry is what enforces them — before a
// handler runs, so a feature with no pool behind it still answers the
// refusal. A valid request would reach the service (and panic over the nil
// pool these tests carry), so every case below is one the contract itself
// refuses; the accepted-shape cases live in the feature's own tests.
//
// Identifier fields carry no shape constraint on purpose: a malformed
// identifier names no account or group, and the service is the boundary
// that refuses it with the not-found failure — the same answer an unknown
// one earns — so the wire cannot tell a caller which half was wrong.
func TestProtovalidateRefusesTheContractViolations(t *testing.T) {
	router := transport.NewRouter(transport.Options{
		Config:  config.Default(),
		Checker: health.NewChecker(),
		// Authorization runs before the contract is enforced, so the caller
		// must be one the guard answers: this test is about protovalidate, and
		// an unauthenticated request would be refused before validation ran.
		Authenticator: func(context.Context, *http.Request) (any, error) {
			return &jwtutils.Caller{
				UserID:       stubCallerWireID(),
				AccessClaims: jwtutils.AccessClaims{Username: "admin", Roles: []string{"administrator"}},
			}, nil
		},
		Modules: []kernel.Module{
			signup.NewModule(signup.NewService(nil, nil, nil)),
			user.NewModule(user.NewService(nil, nil, nil, nil)),
			verification.NewModule(verification.NewService(nil, nil, nil, nil, "", nil)),
		},
	})

	for name, tc := range map[string]struct {
		procedure string
		body      string
	}{
		"bad username": {
			"/saka.identity.v1.SignupService/Signup",
			`{"username":"a","email":"hermione@example.com","password":"expecto-patronum","token":"elder-wand"}`,
		},
		"missing password": {
			"/saka.identity.v1.SignupService/Signup",
			`{"username":"hermione","email":"hermione@example.com","token":"elder-wand"}`,
		},
		"bad email": {
			"/saka.identity.v1.SignupService/Signup",
			`{"username":"hermione","email":"hermione@example","password":"expecto-patronum","token":"elder-wand"}`,
		},
		"ttl below the window": {
			"/saka.identity.v1.SignupService/CreateSignupToken",
			`{"ttl_seconds":60}`,
		},
		"ttl above the window": {
			"/saka.identity.v1.SignupService/CreateSignupToken",
			`{"ttl_seconds":2592001}`,
		},
		"usage limit over budget": {
			"/saka.identity.v1.SignupService/CreateSignupToken",
			`{"ttl_seconds":86400,"usage_limit":1001}`,
		},
		"empty display name": {
			"/saka.identity.v1.UserService/UpdateUser",
			`{"id":"018f0000-0000-7000-8000-000000000000","username":"hermione","email":"hermione@example.com","display_name":""}`,
		},
		"bad username on update": {
			"/saka.identity.v1.UserService/UpdateUser",
			`{"id":"018f0000-0000-7000-8000-000000000000","username":"a b","email":"hermione@example.com","display_name":"Hermione"}`,
		},
		"empty verification token": {
			"/saka.identity.v1.EmailVerificationService/VerifyEmail",
			`{"token":""}`,
		},
		"empty first name on create": {
			"/saka.identity.v1.UserService/CreateUser",
			`{"username":"hermione","email":"hermione@example.com","first_name":"","last_name":"Granger"}`,
		},
		"empty names on signup": {
			"/saka.identity.v1.SignupService/Signup",
			`{"username":"hermione","email":"hermione@example.com","password":"expecto-patronum","token":"elder-wand"}`,
		},
		// ResetProfilePicture is deliberately absent: authorization runs before
		// the contract is enforced, so a caller who does not name their own
		// account is refused by the self rule and never reaches validation.
		// That ordering is the disclosure rule — a non-owner cannot tell a
		// malformed identifier from one that is not theirs.
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequest(t, tc.procedure, tc.body))

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

			var body struct {
				Code string `json:"code"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, "invalid_argument", body.Code)
		})
	}
}
