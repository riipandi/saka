// Package federation is the federation area: the surfaces a deployment
// serves to applications that federate identities to it. The OIDC clients
// an operator administers and the protocol the relying parties speak live
// here; the custom claims and the SCIM sync join as they land.
//
// The split the protocol settled holds: the management surface is
// ConnectRPC — administering clients is backoffice work the SPA drives —
// and the protocol surface is REST, the shapes the specifications define.
// The area owns its own wiring, like every other: the registry names it and
// knows nothing about its services.
package federation

import (
	"context"
	"fmt"
	"uuid"

	"log/slog"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/fetcher"
	"github.com/riipandi/saka/internal/jobs"
	"github.com/riipandi/saka/internal/kernel"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/internal/storage"
	"github.com/riipandi/saka/modules/appconfig"
	"github.com/riipandi/saka/modules/federation/customclaim"
	"github.com/riipandi/saka/modules/federation/oidc"
	"github.com/riipandi/saka/modules/federation/scimsync"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
)

// ModuleName is the name the area reports under.
const ModuleName = "federation"

// Deps are the resolved services the area's features are built from. The
// registry resolves them; the area decides which feature takes which.
type Deps struct {
	// Clients administers the OIDC clients.
	Clients *oidc.Service

	// Claims administers the custom claims the tokens carry.
	Claims *customclaim.Service

	// Protocol is the OIDC provider the /oidc surface serves. It is
	// nil when the switch is off or the jwks service cannot sign.
	Protocol *oidc.Protocol

	// Scim runs the outbound provisioning passes and administers the
	// provider rows.
	Scim *scimsync.Service
}

// Module mounts every federation feature.
type Module struct {
	features []kernel.Module
}

// NewModule builds the area over its dependencies.
func NewModule(deps Deps) *Module {
	return &Module{features: features(deps)}
}

// Name reports the area in composition reports and logs.
func (m *Module) Name() string { return ModuleName }

// Mount registers every feature's endpoints on the router. It runs once, at
// startup, before the listener opens.
func (m *Module) Mount(r chi.Router) {
	kernel.Mount(r, m.features...)
}

// MountRPC registers the procedures of every feature that serves any. The
// area forwards because the composition root names the area alone.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	kernel.MountRPC(r, opts, m.features...)
}

// Package registers the services this area owns.
//
// The composition root applies it while the container is built, so it only
// registers: each service is constructed when something resolves it.
var Package = do.Package(
	do.Lazy(func(i do.Injector) (*oidc.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		users := do.MustInvoke[*user.Service](i)
		pictures := do.MustInvoke[*storage.Manager](i)
		claims := do.MustInvoke[*customclaim.Service](i)
		httpFetcher := do.MustInvoke[*fetcher.Client](i)
		// The account facts ride the user service directly — its GetUser
		// is the method set the preview's seam names — and the logos ride
		// the shared storage engine, the way the profile pictures do. A
		// container without the identity area hands a typed nil here, which
		// an interface would happily hold; the preview refuses a directory
		// it cannot call, so the typed nil is dropped at the seam.
		service := oidc.NewService(pool, recorder, log).WithPictures(pictures).
			WithClaimSource(claimAdapter{service: claims}).
			WithCIMDFetcher(oidc.FetcherAdapter(httpFetcher)).
			WithCIMDAllowlist(c.OIDC.CIMDURLAllowlist).
			// The end-session switch is a settings read, not a boot-time
			// value: the adapter resolves the feature the appconfig area
			// owns, and the logout flow asks it per call.
			WithEndSessionRevokesConsentSource(consentRevocationAdapter{
				settings: do.MustInvoke[*appconfig.Settings](i),
			}).
			// The back-channel logout delivery rides the same settings
			// feature for its switch, signs through the jwks service the
			// provider publishes from, and enqueues through the shared
			// queue client — a nil service of the three is the delivery
			// off, the way the test containers wire them.
			WithBackchannelLogoutSource(backchannelSwitchAdapter{
				settings: do.MustInvoke[*appconfig.Settings](i),
			}).
			WithBackchannelLogoutSigner(oidc.LogoutTokenSignerFor(
				do.MustInvoke[*jwks.Service](i), c.App.BaseURL,
			)).
			WithBackchannelLogoutDispatcher(backchannelDispatchAdapter{
				queue: do.MustInvoke[*queue.Client](i),
			}).
			// The refresh windows are settings reads: the standard window
			// and the offline_access one land on the next issuance or
			// rotation, never waiting for a restart.
			WithRefreshWindowSource(refreshWindowAdapter{
				settings: do.MustInvoke[*appconfig.Settings](i),
			})
		if users != nil {
			service = service.WithUserDirectory(users)
		}
		return service.WithBaseURL(c.App.BaseURL), nil
	}),

	do.Lazy(func(i do.Injector) (*customclaim.Service, error) {
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		return customclaim.NewService(pool, recorder, log), nil
	}),

	do.Lazy(func(i do.Injector) (*scimsync.Service, error) {
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		httpFetcher := do.MustInvoke[*fetcher.Client](i)
		c := do.MustInvoke[*config.Config](i)
		// The cipher unseals a provider token for the sync and seals a
		// Create's; a run without the app secret cannot do either, and the
		// service answers that at the call — a run that never provisions
		// still serves the reads.
		var cipher *crypto.Cipher
		if c.App.SecretKey != "" {
			built, err := crypto.NewCipherFromHex(c.App.SecretKey)
			if err != nil {
				return nil, fmt.Errorf("federation: scimsync cipher: %w", err)
			}
			cipher = built
		}
		service := scimsync.NewService(pool, scimsync.NewRepository(), recorder, cipher, httpFetcher, log)
		// The visibility roll's sources are the identity tables; the
		// adapters here read them directly rather than through the user
		// service, because the roll is the authorization's semantics and
		// must not depend on the feature's CRUD surface.
		return service.WithDirectories(scimsync.NewDirectory(), scimsync.NewGroupDirectory()), nil
	}),
)

// Mount resolves what this area's features need and builds the module the
// router mounts. It is the other half of the seam the composition root uses.
func Mount(i do.Injector) (kernel.Module, error) {
	c := do.MustInvoke[*config.Config](i)
	deps := Deps{
		Clients: do.MustInvoke[*oidc.Service](i),
		Claims:  do.MustInvoke[*customclaim.Service](i),
		Scim:    do.MustInvoke[*scimsync.Service](i),
	}
	if c.OIDC.Enabled {
		keys := do.MustInvoke[*jwks.Service](i)
		protocol, err := oidc.NewProtocol(do.MustInvoke[*datastore.Postgres](i), deps.Clients, keys, do.MustInvoke[*slog.Logger](i))
		if err != nil {
			return nil, err
		}
		deps.Protocol = protocol
	}
	return NewModule(deps), nil
}

// features is the area's feature list, the one place a federation feature is
// named. A nil service is skipped, the way every area's list does it.
func features(deps Deps) []kernel.Module {
	modules := []kernel.Module{}
	if deps.Clients != nil {
		modules = append(modules, oidc.NewModule(deps.Clients))
	}
	if deps.Claims != nil {
		modules = append(modules, customclaim.NewModule(deps.Claims))
	}
	if deps.Protocol != nil {
		modules = append(modules, oidc.NewProtocolModule(deps.Protocol))
	}
	if deps.Scim != nil {
		modules = append(modules, scimsync.NewModule(deps.Scim))
	}
	return modules
}

// claimAdapter maps the customclaim service's claims onto the preview
// seam's — the same key/value shape with a different type identity, which
// is the adapter's whole reason to exist: the seam's method set is the
// consuming feature's contract, and the claim feature must not import the
// client feature to satisfy it.
type claimAdapter struct {
	service *customclaim.Service
}

// consentRevocationAdapter reads the end-session switch from the settings
// feature, the catalog key the appconfig area owns. The interface is the
// oidc package's, so the logout flow stays ignorant of where the value
// rests.
type consentRevocationAdapter struct {
	settings *appconfig.Settings
}

func (a consentRevocationAdapter) EndSessionRevokesConsent(ctx context.Context) (bool, error) {
	return a.settings.GetBool(ctx, appconfig.SettingOIDCEndSessionRevokesConsent)
}

// backchannelSwitchAdapter reads the back-channel logout switch from the
// settings feature, the same catalog the consent switch rides.
type backchannelSwitchAdapter struct {
	settings *appconfig.Settings
}

func (a backchannelSwitchAdapter) BackchannelLogoutEnabled(ctx context.Context) (bool, error) {
	return a.settings.GetBool(ctx, appconfig.SettingOIDCBackchannelLogoutEnabled)
}

// backchannelDispatchAdapter enqueues one delivery through the shared
// queue client: the task type and the retry live in internal/jobs, and
// the adapter is the seam the oidc service's contract names.
type backchannelDispatchAdapter struct {
	queue *queue.Client
}

func (a backchannelDispatchAdapter) DispatchBackchannelLogout(ctx context.Context, dispatch oidc.BackchannelLogoutDispatch) error {
	_, err := a.queue.Add(jobs.BackchannelLogoutTask{
		ClientID: dispatch.ClientID,
		URL:      dispatch.URI,
		Token:    dispatch.Token,
	}).Ctx(ctx).Save()
	if err != nil {
		return fmt.Errorf("federation: enqueue back-channel logout: %w", err)
	}
	return nil
}

// refreshWindowAdapter reads the two refresh windows from the settings
// feature, the catalog keys the appconfig area owns.
type refreshWindowAdapter struct {
	settings *appconfig.Settings
}

func (a refreshWindowAdapter) RefreshTokenHours(ctx context.Context) (int, error) {
	hours, err := a.settings.GetInt64(ctx, appconfig.SettingOIDCRefreshTokenHours)
	if err != nil {
		return 0, err
	}
	return int(hours), nil
}

func (a refreshWindowAdapter) OfflineRefreshTokenHours(ctx context.Context) (int, error) {
	hours, err := a.settings.GetInt64(ctx, appconfig.SettingOIDCOfflineRefreshTokenHours)
	if err != nil {
		return 0, err
	}
	return int(hours), nil
}

func (a claimAdapter) UserClaims(ctx context.Context, userID uuid.UUID) ([]oidc.Claim, error) {
	claims, err := a.service.UserClaims(ctx, userID)
	if err != nil {
		return nil, err
	}
	return convertClaims(claims), nil
}

func (a claimAdapter) GroupClaims(ctx context.Context, groupIDs []uuid.UUID) ([]oidc.Claim, error) {
	claims, err := a.service.GroupClaims(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	return convertClaims(claims), nil
}

func convertClaims(claims []customclaim.Claim) []oidc.Claim {
	converted := make([]oidc.Claim, 0, len(claims))
	for _, claim := range claims {
		converted = append(converted, oidc.Claim{Key: claim.Key, Value: claim.Value})
	}
	return converted
}
