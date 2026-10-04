package federation

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	federationv1connect "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1/federationv1connect"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/fetcher"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/internal/storage"
	"github.com/riipandi/saka/modules/appconfig"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/modules/identity/user"
)

// TestTheAreaForwardsFeatureProcedures pins the RPC forwarding through the
// seam the registry uses: the area's Package registers the services and its
// Mount resolves them into the Deps. A provider or a resolution missed there
// leaves the feature's service nil, features() skips it without an error,
// and every procedure it holds answers "unknown procedure" — an area test
// that hand-builds Deps pins nothing about that wiring.
func TestTheAreaForwardsFeatureProcedures(t *testing.T) {
	cfg := testConfig(t)
	// The end-session switch reads the settings feature the appconfig area
	// owns. A settings service over the test injector: the constructor
	// touches no database, so the nil pool stands.
	settings, err := appconfig.NewSettings(nil, nil, nil, nil)
	require.NoError(t, err)
	i := do.New(
		do.Eager(&cfg),
		do.Eager[*slog.Logger](nil),
		do.Eager[*datastore.Postgres](nil),
		// The preview reads the account facts through the user service; a
		// nil stands in for the wiring the composition root guarantees,
		// and the preview refuses while the management procedures serve.
		do.Eager[*user.Service](nil),
		// The logos stage into the engine; nil stands in for the wiring
		// the composition root guarantees, and the logo procedures refuse
		// while the management procedures serve.
		do.Eager[*storage.Manager](nil),
		// The CIMD materializations fetch through the shared client; nil
		// stands in for the wiring the composition root guarantees, and
		// the CIMD path refuses while the management procedures serve.
		do.Eager[*fetcher.Client](nil),
		// The features write audit records through the shared recorder;
		// nil stands in for the wiring the composition root guarantees,
		// and the recorder is nil-safe so a feature runs without one.
		do.Eager[*audit.Recorder](nil),
		// The protocol feature resolves the signing service; nil stands
		// in for the wiring the composition root guarantees, and the
		// test's configuration switches the protocol off — the
		// forwarding contract is the management surface's.
		do.Eager[*jwks.Service](nil),
		// The back-channel logout dispatch enqueues through the shared
		// client; nil stands in for the wiring, the delivery off.
		do.Eager[*queue.Client](nil),
		do.Eager(settings),
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
	for _, procedure := range []string{
		federationv1connect.OidcClientServiceListClientsProcedure,
		federationv1connect.OidcClientServiceCreateClientProcedure,
		federationv1connect.OidcClientServiceGetClientProcedure,
		federationv1connect.OidcClientServiceUpdateClientProcedure,
		federationv1connect.OidcClientServiceDeleteClientProcedure,
		federationv1connect.OidcClientServiceUpdateAllowedUserGroupsProcedure,
		federationv1connect.OidcClientServiceGetClientMetaProcedure,
		federationv1connect.OidcClientServicePreviewClientProcedure,
		federationv1connect.OidcClientServiceUploadLogoProcedure,
		federationv1connect.OidcClientServiceDeleteLogoProcedure,
		federationv1connect.OidcClientServiceListSecretsProcedure,
		federationv1connect.OidcClientServiceCreateSecretProcedure,
		federationv1connect.OidcClientServiceDeleteSecretProcedure,
		federationv1connect.CustomClaimServiceSuggestProcedure,
		federationv1connect.CustomClaimServiceListUserClaimsProcedure,
		federationv1connect.CustomClaimServiceCreateUserClaimProcedure,
		federationv1connect.CustomClaimServiceUpdateUserClaimProcedure,
		federationv1connect.CustomClaimServiceDeleteUserClaimProcedure,
		federationv1connect.CustomClaimServiceListGroupClaimsProcedure,
		federationv1connect.CustomClaimServiceCreateGroupClaimProcedure,
		federationv1connect.CustomClaimServiceUpdateGroupClaimProcedure,
		federationv1connect.CustomClaimServiceDeleteGroupClaimProcedure,
		federationv1connect.OidcConsentServiceListMyAuthorizedClientsProcedure,
		federationv1connect.OidcConsentServiceRevokeMyAuthorizedClientProcedure,
		federationv1connect.OidcConsentServiceListMyClientsProcedure,
		federationv1connect.OidcConsentServiceListUserAuthorizedClientsProcedure,
		federationv1connect.OidcConsentServiceListAllAuthorizedClientsProcedure,
		federationv1connect.ScimProviderServiceGetByClientProcedure,
		federationv1connect.ScimProviderServiceCreateProcedure,
		federationv1connect.ScimProviderServiceUpdateProcedure,
		federationv1connect.ScimProviderServiceDeleteProcedure,
		federationv1connect.ScimProviderServiceSyncProcedure,
	} {
		assert.True(t, claimed[procedure],
			"the area must forward its features' procedures to the RPC router: %s", procedure)
	}

	// The logo read is the one REST route the feature claims: the sign-in
	// page fetches it through an <img> tag, without a protocol.
	httpRouter := chi.NewRouter()
	module.Mount(httpRouter)
	logoClaimed := false
	for _, route := range httpRouter.Routes() {
		if _, ok := route.Handlers[http.MethodGet]; ok && route.Pattern == "/oidc/clients/{id}/logo" {
			logoClaimed = true
		}
	}
	assert.True(t, logoClaimed, "the area must mount the logo route on the HTTP router")
}

// testConfig answers a configuration the area's providers read their values
// from — the base URL the logo URLs render against.
func testConfig(t *testing.T) config.Config {
	t.Helper()

	cfg := config.Default()
	cfg.App.BaseURL = "https://idp.example.com"
	// The forwarding contract is the management surface's; the protocol
	// feature needs a signing key the test container does not carry.
	cfg.OIDC.Enabled = false
	return cfg
}
