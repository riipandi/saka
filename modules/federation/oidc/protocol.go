package oidc

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/luikyv/go-oidc/pkg/provider"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/modules/identity/user"
)

// The paths the protocol serves. The endpoints keep the prefix the
// discovery document publishes; the discovery and JWKS documents stay at
// the well-known root.
const (
	protocolAuthorizeEndpoint  = "/oidc/authorize"
	protocolTokenEndpoint      = "/oidc/token"
	protocolUserInfoEndpoint   = "/oidc/userinfo"
	protocolEndSessionEndpoint = "/oidc/end-session"
	protocolJWKSEndpoint       = "/.well-known/jwks.json"

	// interactionPath is the SPA route the sign-in-less browser lands on.
	interactionPath = "/interaction"
)

// Protocol builds the OIDC provider the federation area mounts. The
// managers are the pgx stores; the identity facts ride the same seams the
// preview reads.
type Protocol struct {
	provider *provider.Provider
	baseURL  string
}

// NewProtocol wires the provider: the jwks service signs, the stores
// persist, the policy walks the browser through the SPA's interaction
// page.
func NewProtocol(pool *datastore.Postgres, service *Service, keys *jwks.Service, log *slog.Logger) (*Protocol, error) {
	baseURL := service.baseURL
	alg, err := keys.SigningAlgorithm()
	if err != nil {
		return nil, err
	}

	stores := protocolStore{pool: pool}

	clientManager := &clientStore{
		pool:    pool,
		repo:    NewRepository(),
		baseURL: baseURL,
		materialize: func(ctx context.Context, id string) error {
			_, matErr := service.MaterializeCIMDClient(ctx, id)
			return matErr
		},
	}

	idTokenAlg := goidc.SignatureAlgorithm(alg.String())
	p, err := provider.New(provider.Config{
		Issuer:      baseURL,
		JWKS:        jwksFunc(keys),
		IDTokenAlgs: []goidc.SignatureAlgorithm{idTokenAlg},
		Manager:     grantStore{protocolStore: stores},
	},
		// The client lookup runs through the DCR manager; the
		// registration endpoint it would mount stays unreachable —
		// the patterns Mount registers name the protocol's routes
		// alone.
		provider.WithDCR(clientManager),
		provider.WithTokenEndpoint(protocolTokenEndpoint),
		provider.WithAuthorizeEndpoint(protocolAuthorizeEndpoint),
		provider.WithUserInfoEndpoint(protocolUserInfoEndpoint),
		provider.WithJWKSEndpoint(protocolJWKSEndpoint),
		provider.WithScopes(protocolScopes()...),
		provider.WithClaims(profileClaims...),
		provider.WithAuthCodeGrant(provider.AuthCodeGrantConfig{
			Manager:       authnStore{protocolStore: stores},
			ResponseTypes: []goidc.ResponseType{goidc.ResponseTypeCode},
		},
			provider.WithAuthPolicies(signInPolicy(service), interactionPolicy(service)),
			provider.WithPKCE([]goidc.CodeChallengeMethod{goidc.CodeChallengeMethodSHA256, goidc.CodeChallengeMethodPlain},
				provider.WithPKCERequired()),
		),
		provider.WithRefreshTokenGrant(grantStore{protocolStore: stores}, provider.WithRefreshTokenRotation()),
		provider.WithClientSecretVerifier(clientSecretVerifier),
		provider.WithNoneAuthn(),
		provider.WithSecretPostAuthn(),
		provider.WithSecretBasicAuthn(),
		provider.WithIDTokenClaims(idTokenClaims(service)),
		provider.WithUserInfoClaims(userInfoClaims(service)),
		provider.WithLogout(provider.LogoutConfig{Manager: logoutStore{protocolStore: stores}},
			provider.WithLogoutEndpoint(protocolEndSessionEndpoint)),
	)
	if err != nil {
		return nil, err
	}
	return &Protocol{provider: p, baseURL: baseURL}, nil
}

// Mount registers the protocol's paths on the router. The patterns name
// exactly the endpoints the provider owns — the registration and other
// unmounted provider routes stay unreachable, so the guard's unnamed-path
// refusal covers everything under /oidc the protocol does not serve.
func (p *Protocol) Mount(r chi.Router) {
	handler := p.provider.Handler()
	r.Handle(protocolAuthorizeEndpoint, handler)
	r.Handle(protocolAuthorizeEndpoint+"/*", handler)
	r.Handle(protocolTokenEndpoint, handler)
	r.Handle(protocolUserInfoEndpoint, handler)
	r.Handle(protocolEndSessionEndpoint, handler)
	r.Handle("/.well-known/openid-configuration", handler)
}

// jwksFunc renders the signing key set the provider signs and publishes
// from. The jwx key marshals to its standard JWK document — private
// material included — and the provider's JSONWebKey reads that document.
func jwksFunc(keys *jwks.Service) goidc.JWKSFunc {
	return func(ctx context.Context) (goidc.JSONWebKeySet, error) {
		key, err := keys.SignKey(ctx)
		if err != nil {
			return goidc.JSONWebKeySet{}, err
		}
		document, err := json.Marshal(key)
		if err != nil {
			return goidc.JSONWebKeySet{}, err
		}
		var webKey goidc.JSONWebKey
		if err := json.Unmarshal(document, &webKey); err != nil {
			return goidc.JSONWebKeySet{}, err
		}
		return goidc.JSONWebKeySet{Keys: []goidc.JSONWebKey{webKey}}, nil
	}
}

// protocolScopes are the scopes a relying party may ask for. Each is its
// own word: a scope matches when it is named exactly.
func protocolScopes() []goidc.Scope {
	scopes := []goidc.Scope{{ID: "openid"}}
	for _, id := range []string{"profile", "email", "groups"} {
		scoped := id
		scopes = append(scopes, goidc.Scope{ID: scoped, Matches: func(requested string) bool {
			return requested == scoped
		}})
	}
	return scopes
}

// profileClaims are the claims the profile scope publishes; openid is
// required and the subject rides every token regardless.
var profileClaims = []string{"given_name", "family_name", "name", "display_name", "preferred_username", "picture", "email", "email_verified", "groups"}

// idTokenClaims merges the account facts and the operator-defined claims
// into an ID token at issuance. An account the directory no longer knows
// names no claims — the grant fails closed downstream.
func idTokenClaims(service *Service) goidc.IDTokenClaimsFunc {
	return func(ctx context.Context, grant *goidc.Grant) map[string]any {
		return subjectClaims(ctx, service, service.claims, grant)
	}
}

// userInfoClaims is the same map the userinfo endpoint answers with.
func userInfoClaims(service *Service) goidc.UserInfoClaimsFunc {
	return func(ctx context.Context, grant *goidc.Grant) map[string]any {
		return subjectClaims(ctx, service, service.claims, grant)
	}
}

// subjectClaims builds the claim map for one grant's subject: the scope
// words choose the sections, the custom claims join under profile.
func subjectClaims(ctx context.Context, service *Service, claims ClaimSource, grant *goidc.Grant) map[string]any {
	result := map[string]any{}
	view, err := service.users.GetUser(ctx, grant.Subject)
	if err != nil || view.ID == "" {
		return result
	}
	scopes := strings.Fields(grant.Scopes)

	if slices.Contains(scopes, "profile") {
		if view.FirstName != nil && *view.FirstName != "" {
			result["given_name"] = *view.FirstName
		}
		if view.LastName != nil && *view.LastName != "" {
			result["family_name"] = *view.LastName
		}
		if view.DisplayName != "" {
			result["name"] = view.DisplayName
			result["display_name"] = view.DisplayName
		}
		if view.Username != "" {
			result["preferred_username"] = view.Username
		}
	}
	if slices.Contains(scopes, "email") && view.Email != "" {
		result["email"] = view.Email
		result["email_verified"] = view.EmailVerified
	}
	if slices.Contains(scopes, "groups") && len(view.Groups) > 0 {
		names := make([]string, 0, len(view.Groups))
		for _, group := range view.Groups {
			names = append(names, group.Name)
		}
		result["groups"] = names
	}
	if slices.Contains(scopes, "profile") && claims != nil {
		id, idErr := user.UUIDFromWire(view.ID)
		if idErr == nil {
			if own, err := claims.UserClaims(ctx, id); err == nil {
				for _, claim := range own {
					result[claim.Key] = claim.Value
				}
			}
			if groupClaims, err := claims.GroupClaims(ctx, groupIDs(view)); err == nil {
				for _, claim := range groupClaims {
					result[claim.Key] = claim.Value
				}
			}
		}
	}
	return result
}
