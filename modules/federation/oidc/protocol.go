package oidc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/luikyv/go-oidc/pkg/provider"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/modules/identity/user"
)

// The paths the protocol serves. The provider registers its routes with
// its own endpoint prefix (WithPathPrefix), so the endpoints carry the
// bare names and every full path the router, the guard, and the policies
// see is protocolPrefix + the endpoint. The JWKS document stays at the
// well-known root — its chi mount is separate from the prefix.
const (
	protocolPrefix = "/oidc"

	protocolAuthorizeEndpoint  = "/authorize"
	protocolTokenEndpoint      = "/token"
	protocolUserInfoEndpoint   = "/userinfo"
	protocolIntrospectEndpoint = "/introspect"
	protocolPAREndpoint        = "/par"
	protocolEndSessionEndpoint = "/end-session"
	protocolJWKSEndpoint       = "/.well-known/jwks.json"
	// The device endpoints are the library's defaults — v0.25.0 names no
	// setter for them. The verification endpoint is where the browser
	// enters the user code and answers the consent question; the device
	// itself reaches the authorization endpoint.
	protocolDeviceAuthorizeEndpoint    = "/device_authorization"
	protocolDeviceVerificationEndpoint = "/device"

	// interactionPath is the SPA route the sign-in-less browser lands on.
	interactionPath = "/interaction"

	// protocolPARLifetimeSecs is how long a pushed request stays valid —
	// the window the relying party has to send the browser into the flow
	// it pushed.
	protocolPARLifetimeSecs = 300
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
// The database is the OIDC signing authority: the configured key pair is
// the internal one, and every token the provider mints is signed with an
// active signing row. A run whose table carries no pair fails here — an
// issuer that cannot sign is a run that must not open its listener.
func NewProtocol(pool *datastore.Postgres, service *Service, keys *jwks.Service, log *slog.Logger) (*Protocol, error) {
	baseURL := service.baseURL
	signingKeys, err := keys.OIDCSigningKeys(context.Background())
	if err != nil {
		return nil, fmt.Errorf("oidc: %w", err)
	}
	algs := make([]goidc.SignatureAlgorithm, 0, len(signingKeys))
	for _, key := range signingKeys {
		// jwx v3 reports the alg member as (value, ok).
		alg, ok := key.Algorithm()
		if !ok || alg.String() == jwa.NoSignature().String() {
			continue
		}
		algs = append(algs, goidc.SignatureAlgorithm(alg.String()))
	}
	if len(algs) == 0 {
		return nil, errors.New("oidc: the database signing rows name no signature algorithm")
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

	idTokenAlgs := algs
	p, err := provider.New(provider.Config{
		Issuer:      baseURL,
		JWKS:        jwksFunc(keys),
		IDTokenAlgs: idTokenAlgs,
		Manager:     grantStore{protocolStore: stores},
	},
		// The client lookup runs through the DCR manager; the
		// registration endpoint it would mount stays unreachable —
		// the patterns Mount registers name the protocol's routes
		// alone.
		provider.WithDCR(clientManager),
		provider.WithPathPrefix(protocolPrefix),
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
			// PAR rides the same session store: the pushed request creates
			// an authorization session whose id the authorize endpoint
			// resolves through the pointer row. One endpoint, short life —
			// the request URI is a one-time ticket into the flow.
			provider.WithPAR(authnStore{protocolStore: stores},
				provider.WithPAREndpoint(protocolPAREndpoint),
				provider.WithPARLifetime(protocolPARLifetimeSecs),
			),
		),
		provider.WithRefreshTokenGrant(grantStore{protocolStore: stores}, provider.WithRefreshTokenRotation()),
		// The device grant rides the same session store: the device code
		// and the user code resolve through hashed pointer rows, the
		// approval walks the SPA interaction like the authorization flow.
		provider.WithDeviceGrant(provider.DeviceGrantConfig{
			Manager: deviceStore{protocolStore: stores},
			// The prompt renders nothing a browser needs — every
			// verification visit redirects into the SPA, which carries
			// the user code in its route.
			PromptFunc: func(w http.ResponseWriter, r *http.Request) error {
				http.Redirect(w, r, interactionPath+"?callback="+url.QueryEscape(protocolDeviceVerificationEndpoint), http.StatusFound)
				return nil
			},
			// The approval ends at the SPA's device confirmation state;
			// a bare success body is all the library demands here.
			ConfirmationFunc: func(w http.ResponseWriter, _ *http.Request) error {
				w.WriteHeader(http.StatusOK)
				return nil
			},
		},
			provider.WithDeviceCodeFunc(defaultDeviceCodeFunc()),
			provider.WithDevicePolicies(devicePolicy(service)),
		),
		provider.WithClientSecretVerifier(clientSecretVerifier),
		provider.WithNoneAuthn(),
		provider.WithSecretPostAuthn(),
		provider.WithSecretBasicAuthn(),
		provider.WithIDTokenClaims(idTokenClaims(service)),
		provider.WithUserInfoClaims(userInfoClaims(service)),
		provider.WithLogout(provider.LogoutConfig{Manager: logoutStore{protocolStore: stores}},
			provider.WithLogoutEndpoint(protocolEndSessionEndpoint)),
		// Introspection is client-scoped: a client reads only the tokens
		// minted to it. RFC 7662 leaves the policy open; tango's answer is
		// that a token says nothing to a stranger.
		provider.WithTokenIntrospection(func(_ context.Context, client *goidc.Client, info goidc.TokenInfo) bool {
			return info.ClientID == client.ID
		}),
		provider.WithTokenIntrospectionEndpoint(protocolIntrospectEndpoint),
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
	flatten := flattenBasicAuth()
	r.Handle(protocolPrefix+protocolAuthorizeEndpoint, handler)
	r.Handle(protocolPrefix+protocolAuthorizeEndpoint+"/*", handler)
	r.Handle(protocolPrefix+protocolTokenEndpoint, flatten(handler))
	r.Handle(protocolPrefix+protocolUserInfoEndpoint, handler)
	r.Handle(protocolPrefix+protocolIntrospectEndpoint, flatten(handler))
	r.Handle(protocolPrefix+protocolPAREndpoint, flatten(handler))
	r.Handle(protocolPrefix+protocolEndSessionEndpoint, handler)
	// The device verification endpoint serves the browser's entry and
	// its callback continuation, so the subtree mounts with the handler.
	r.Handle(protocolPrefix+protocolDeviceAuthorizeEndpoint, flatten(handler))
	r.Handle(protocolPrefix+protocolDeviceVerificationEndpoint, handler)
	r.Handle(protocolPrefix+protocolDeviceVerificationEndpoint+"/*", handler)
	r.Handle("/.well-known/openid-configuration", handler)
}

// defaultDeviceCodeFunc draws the device code the same way the library
// draws its defaults: high-entropy random hex, opaque to every party.
func defaultDeviceCodeFunc() goidc.RandomFunc {
	return func(_ context.Context) string {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		return hex.EncodeToString(buf)
	}
}

// flattenBasicAuth copies the Authorization header's Basic credentials
// onto the form, when the form carries none of its own. The endpoints it
// wraps authenticate a client from the form — the library reads one
// authentication method per client, and secret-post is the one tango's
// clients declare — while RFC 6749 §2.3.1 lets the same client present
// the same pair in the Basic header.
func flattenBasicAuth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id, secret, ok := r.BasicAuth(); ok && id != "" &&
				r.PostFormValue("client_id") == "" && r.PostFormValue("client_secret") == "" {
				// The body is bounded before anything parses it: a
				// credentials document is a few hundred bytes, anything
				// larger is a request no endpoint here serves. The parse
				// marks the form filled, so the handler's own reads keep
				// the values this sets.
				r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
				if err := r.ParseForm(); err == nil {
					r.PostForm.Set("client_id", id)
					r.PostForm.Set("client_secret", secret)
					r.Form.Set("client_id", id)
					r.Form.Set("client_secret", secret)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// jwksFunc renders the signing key set the provider signs and publishes
// from: the database's active signing rows, private material included so
// the provider can sign, their public halves what discovery hands out.
// The set is read per call, so a rotation lands without a restart.
func jwksFunc(keys *jwks.Service) goidc.JWKSFunc {
	return func(ctx context.Context) (goidc.JSONWebKeySet, error) {
		stored, err := keys.OIDCSigningKeys(ctx)
		if err != nil {
			return goidc.JSONWebKeySet{}, err
		}
		set := goidc.JSONWebKeySet{}
		for _, key := range stored {
			document, err := json.Marshal(key)
			if err != nil {
				return goidc.JSONWebKeySet{}, err
			}
			var webKey goidc.JSONWebKey
			if err := json.Unmarshal(document, &webKey); err != nil {
				return goidc.JSONWebKeySet{}, err
			}
			// Token introspection only accepts a key that declares the
			// signature usage; the stored JWK document carries no "use"
			// member, so set it here where the set is published.
			webKey.Use = "sig"
			set.Keys = append(set.Keys, webKey)
		}
		return set, nil
	}
}

// protocolScopes are the scopes a relying party may ask for. Each is its
// own word: a scope matches when it is named exactly, openid included —
// the library calls the matcher for every requested word.
func protocolScopes() []goidc.Scope {
	scopes := []goidc.Scope{}
	for _, id := range []string{"openid", "profile", "email", "groups"} {
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
	// The grant carries the raw UUID the rows store; the directory
	// speaks the wire form.
	wire, err := user.IDFromUUIDString(grant.Subject)
	if err != nil {
		return result
	}
	view, err := service.users.GetUser(ctx, wire.String())
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
