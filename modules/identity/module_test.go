package identity

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	authzv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authz/v1/authzv1connect"
	identityv1connect "github.com/riipandi/saka/codegen/proto/go/saka/identity/v1/identityv1connect"
	"github.com/riipandi/saka/framework/cache"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/fetcher"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/internal/storage"
	"github.com/riipandi/saka/modules/appconfig"
	"github.com/riipandi/saka/modules/identity/jwks"
)

// TestTheAreaForwardsFeatureProcedures pins the RPC forwarding through the
// seam the registry uses: the area's Package registers the services and its
// Mount resolves them into the Deps. A provider or a resolution missed there
// leaves the feature's service nil, features() skips it without an error,
// and every procedure it holds answers "unknown procedure" — an area test
// that hand-builds Deps pins nothing about that wiring.
func TestTheAreaForwardsFeatureProcedures(t *testing.T) {
	cfg := testConfig(t)
	i := do.New(
		do.Eager(&cfg),
		do.Eager[*slog.Logger](slog.New(slog.DiscardHandler)),
		do.Eager[*datastore.Postgres](nil),
		// The verification feature builds over the infrastructure the
		// registry resolves; the area test pins the forwarding, not the
		// mail or queue wiring, so nils stand in for what the composition
		// root guarantees to be present.
		do.Eager[*mailer.Service](nil),
		do.Eager[*queue.Client](nil),
		// The user feature stages its pictures into the engine; nil stands
		// in for the wiring the composition root guarantees, and the picture
		// procedures refuse while the account procedures serve.
		do.Eager[*storage.Manager](nil),
		// The features write audit records through the shared recorder; nil
		// stands in for the wiring the composition root guarantees, and the
		// recorder is nil-safe so a feature runs without one.
		do.Eager[*audit.Recorder](nil),
		// The settings and the authorization features read through the
		// shared cache; the driver that answers every read a miss stands in
		// for the wiring the composition root guarantees.
		do.Eager[cache.Cache](cache.Noop{}),
		// The passkey ceremonies read their knobs through the settings
		// feature; nil stands in for the wiring the composition root
		// guarantees, and the reads fail closed rather than dereference it.
		do.Eager[*appconfig.Settings](nil),
		// The credential policy's breach corpus rides the shared outbound
		// client; nil stands in for the wiring the composition root
		// guarantees, and the checker answers unavailable — the optional
		// feature's off state — rather than dialling anything.
		do.Eager[*fetcher.Client](nil),
	)
	Package(i)

	module, err := Mount(i)
	require.NoError(t, err)

	rpc, ok := module.(kernel.RPCModule)
	require.True(t, ok, "the area must implement kernel.RPCModule")

	router := chi.NewRouter()
	rpc.MountRPC(router)

	claimed := map[string]bool{}
	for _, route := range router.Routes() {
		claimed[route.Pattern] = true
	}
	assert.True(t, claimed[authnv1connect.AuthServiceSignInProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.SignupServiceSignupProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.SignupServiceCreateSignupTokenProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.SignupServiceListSignupTokensProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.SignupServiceDeleteSignupTokenProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceListUsersProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceGetUserProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceCreateUserProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceUpdateUserProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceDeleteUserProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceResetProfilePictureProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.EmailVerificationServiceSendEmailProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.EmailVerificationServiceVerifyEmailProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.OneTimeAccessServiceCreateTokenProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.OneTimeAccessServiceExchangeTokenProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.OneTimeAccessServiceRequestEmailAsAdminProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.OneTimeAccessServiceRequestEmailProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.OAuthSSOServiceBeginSignInProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.OAuthSSOServiceCreateConnectionProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.OAuthSSOServiceUnlinkConnectionProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.OAuthSSOServiceGetLinkedAccountTokensProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserGroupServiceListUserGroupsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserGroupServiceGetUserGroupProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserGroupServiceCreateUserGroupProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserGroupServiceUpdateUserGroupProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserGroupServiceDeleteUserGroupProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserGroupServiceSetUserGroupMembersProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserGroupServiceGetUserGroupsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserGroupServiceUpdateUserGroupsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceListPermissionsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceListRolesProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceGetRoleProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceCreateRoleProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceUpdateRoleProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceDeleteRoleProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceSetRolePermissionsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceListUserRolesProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceSetUserRolesProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceListUserPermissionsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authzv1connect.AuthorizationServiceSetUserPermissionsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceGetCurrentUserProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceUpdateCurrentUserProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceBanUserProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[identityv1connect.UserServiceUnbanUserProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.SessionServiceSignOutProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.SessionServiceGetSessionProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.SessionServiceListSessionsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.SessionServiceRevokeSessionProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.SessionServiceRefreshProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.SessionServiceSignOutOtherSessionsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.SessionServiceSignOutAllSessionsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.SessionServiceImpersonateUserProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.SessionServiceStopImpersonatingProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.WebAuthnServiceBeginRegistrationProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.WebAuthnServiceVerifyLoginProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.WebAuthnServiceListCredentialsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.WebAuthnServiceAdminListCredentialsProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.WebAuthnServiceAdminDeleteCredentialProcedure],
		"the area must forward its features' procedures to the RPC router")
	assert.True(t, claimed[authnv1connect.WebAuthnServiceReauthenticateProcedure],
		"the area must forward its features' procedures to the RPC router")
}

// TestTheAreaMountsItsFeatures is the reason the area exists: the registry
// names one module, and the features inside it are reachable without the
// registry knowing them.
func TestTheAreaMountsItsFeatures(t *testing.T) {
	// A service with no configured key still mounts: the endpoint serves an
	// empty set rather than being absent.
	router := chi.NewRouter()
	NewModule(Deps{KeySet: jwks.NewService(testConfig(t), nil, nil, nil)}).Mount(router)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, jwks.Path, nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"keys"`)
}

// TestNameIsTheArea keeps the composition report readable.
func TestNameIsTheArea(t *testing.T) {
	assert.Equal(t, "identity", NewModule(Deps{}).Name())
}

// TestTheAreaMountsWithoutEveryDependency covers the area before a feature
// lands: a nil service must not panic the mount, because a deployment without
// a key pair is a valid one.
func TestTheAreaMountsWithoutEveryDependency(t *testing.T) {
	assert.NotPanics(t, func() {
		NewModule(Deps{}).Mount(chi.NewRouter())
	})
}

// testConfig returns a configuration with no signing material, which is the
// state a fresh deployment whose signing key the database still holds back
// is in.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	// The passkey engine derives its RP identity from the public origin at
	// construction, and a default config carries no base URL; the test names
	// the dev one.
	cfg.App.BaseURL = "http://localhost:3080"
	return cfg
}
