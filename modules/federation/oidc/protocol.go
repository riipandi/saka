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

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/modules/identity/user"
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
	protocolRevokeEndpoint     = "/revoke"

	// ScopeOfflineAccess is the wire word a relying party asks for when
	// its refresh tokens must outlive a user session — the persistent
	// access the OIDC name carries. The consent question names it, and
	// only the authorization-code grant issues it.
	ScopeOfflineAccess         = "offline_access"
	protocolPAREndpoint        = "/par"
	protocolEndSessionEndpoint = "/end-session"
	protocolJWKSEndpoint       = "/.well-known/jwks.json"
	// The device endpoints are the library's defaults — v0.25.0 names no
	// setter for them. The verification endpoint is where the browser
	// enters the user code and answers the consent question; the device
	// itself reaches the authorization endpoint.
	protocolDeviceAuthorizeEndpoint    = "/device_authorization"
	protocolDeviceVerificationEndpoint = "/device"
	protocolRegisterEndpoint           = "/register"

	// interactionPath is the SPA route the sign-in-less browser lands on.
	interactionPath = "/interaction"

	// protocolPARLifetimeSecs is how long a pushed request stays valid —
	// the window the relying party has to send the browser into the flow
	// it pushed.
	protocolPARLifetimeSecs = 300

	// protocolAuthCodeLifetimeSecs is how long an authorization code
	// stays redeemable — one exchange, inside a minute.
	protocolAuthCodeLifetimeSecs = 60

	// clientCredentialsLifetimeSecs is the window a machine-to-machine
	// access token lives for: shorter than a user session's, because the
	// client can mint a successor with its credential whenever it needs.
	clientCredentialsLifetimeSecs = 3600

	// defaultTokenLifetimeSecs mirrors the library's own default (300s)
	// for every grant the options func answers — a zero would expire a
	// token at mint, so the default is spelled rather than left implicit.
	defaultTokenLifetimeSecs = 300
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
		provider.WithClaims(claimsSupported()...),
		// The request parameters the authorize endpoint accepts beyond the
		// mandatory ones: every display hint the specification names, and
		// the authentication context references the certification profiles
		// send — the suite's rehearsal vocabulary rides the LoA numbers,
		// and both lists advertise themselves in the discovery document.
		provider.WithDisplayValues(goidc.DisplayValuePage, goidc.DisplayValuePopup,
			goidc.DisplayValueTouch, goidc.DisplayValueWAP),
		provider.WithACRs(protocolACRs()...),
		provider.WithAuthCodeGrant(provider.AuthCodeGrantConfig{
			Manager:       authnStore{protocolStore: stores},
			ResponseTypes: []goidc.ResponseType{goidc.ResponseTypeCode},
		},
			provider.WithAuthPolicies(signInPolicy(service), interactionPolicy(service)),
			// The code's window is pinned: the grant document records the
			// deadline and the token endpoint refuses an expired one.
			provider.WithAuthCodeLifetime(protocolAuthCodeLifetimeSecs),
			// RFC 9700 §4.1.3: the verifier must be the S256 challenge —
			// plain is the method the specification tells servers to stop
			// offering, so the provider does not. The requirement itself
			// rides the client: the engine refuses a code request from a
			// public client that carries no challenge (OAuth 2.1's rule),
			// while a confidential client may omit PKCE — the Basic OP
			// profile the certification runs names requests without one.
			provider.WithPKCE([]goidc.CodeChallengeMethod{goidc.CodeChallengeMethodSHA256}),
			// The request object by value (JAR, RFC 9101): the unsigned
			// form the Basic OP profile sends — the object's state, nonce,
			// and the rest replace the query's — plus ES256 for a client
			// that registers a key set to sign one with.
			provider.WithJAR([]goidc.SignatureAlgorithm{goidc.SigAlgNone, goidc.SigAlgES256}),
			// Third-party initiated login: the authorization response carries
			// the iss parameter (RFC 9207), so a relying party that linked
			// the browser here can tell the answer apart from any other
			// provider's response landing on the same callback.
			provider.WithIssuerResponseParameter(),
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
		// The refresh windows ride the settings, read at every issuance
		// and rotation: the grant handler runs before the store saves a
		// new grant, and the rotation path rewrites the row the same way
		// the service's own expiry stamp does — a grant carrying
		// offline_access rides the long window, the rest the standard
		// one, and zero is the never-expiring token.
		provider.WithGrantHandler(func(ctx context.Context, grantType goidc.GrantType, grant *goidc.Grant) error {
			return stampRefreshWindow(ctx, service, grantType, grant)
		}),
		// The client-credentials grant is the machine-to-machine surface:
		// the token names the client itself as its subject, the client's
		// own allowed list judges the request, and the group restriction
		// is silent — no account is involved.
		provider.WithClientCredentialsGrant(),
		provider.WithTokenOptions(func(_ context.Context, grant *goidc.Grant, _ *goidc.Client) goidc.TokenOptions {
			// The client-credentials grant is the one whose subject is
			// the client itself — the library mints no refresh token for
			// it and no user session stands behind it. Its tokens live
			// the spelled hour; the user grants ride the library's
			// default, spelled too, because a zero would expire a token
			// at mint.
			lifetime := defaultTokenLifetimeSecs
			if grant.Subject == grant.ClientID && grant.Subject != "" {
				lifetime = clientCredentialsLifetimeSecs
			}
			return goidc.NewJWTTokenOptions(algs[0], lifetime)
		}),
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
		// The JTI consumer claims every jti a client-presented JWT
		// carries — a DPoP proof, a client assertion, a pushed request
		// object — so a replayed JWT is refused by the store and not by
		// luck. The sweep reaps the claims as they pass.
		provider.WithJTIConsumer(stores.consumeJTI),
		provider.WithIDTokenClaims(idTokenClaims(service)),
		provider.WithUserInfoClaims(userInfoClaims(service)),
		provider.WithLogout(provider.LogoutConfig{
			Manager:    logoutStore{protocolStore: stores},
			HandleFunc: defaultPostLogout,
		},
			provider.WithLogoutEndpoint(protocolEndSessionEndpoint),
			provider.WithLogoutPolicies(logoutPolicy(service))),
		// Introspection is client-scoped: a client reads only the tokens
		// minted to it. RFC 7662 leaves the policy open; saka's answer is
		// that a token says nothing to a stranger.
		provider.WithTokenIntrospection(func(_ context.Context, client *goidc.Client, info goidc.TokenInfo) bool {
			return info.ClientID == client.ID
		}),
		provider.WithTokenIntrospectionEndpoint(protocolIntrospectEndpoint),
		// RFC 7009 revocation is open to every registered client — the
		// ownership of the presented token is the library's own
		// client-scoped check — and an access-token hint must reach the
		// grant, because saka's access tokens are JWTs the store never
		// sees by themselves.
		provider.WithTokenRevocation(revocationPolicy(),
			provider.WithTokenRevocationRevokeGrantOnAccessToken(),
			provider.WithTokenRevocationEndpoint(protocolRevokeEndpoint)),
	)
	if err != nil {
		return nil, err
	}
	return &Protocol{provider: p, baseURL: baseURL}, nil
}

// revocationPolicy is the RFC 7009 gate the provider runs after it has
// authenticated the client: any registered client may call the endpoint.
// Whether the presented token belongs to the caller is not this policy's
// question — the library answers it against the token's own client, and
// a stranger's token is refused there.
func revocationPolicy() goidc.IsClientAllowedFunc {
	return func(context.Context, *goidc.Client) bool {
		return true
	}
}

// stampRefreshWindow is the grant-handler body: the refresh window rides
// the settings, read at every issuance and rotation, and the offline
// scope picks the long window. Zero hours is the never-expiring token.
func stampRefreshWindow(ctx context.Context, service *Service, grantType goidc.GrantType, grant *goidc.Grant) error {
	if grantType != goidc.GrantAuthorizationCode && grantType != goidc.GrantRefreshToken {
		return nil
	}
	if hours := service.refreshWindowHours(ctx, grant); hours > 0 {
		grant.RefreshTokenExpiresAt = grant.CreatedAt + hours*3600
	} else {
		grant.RefreshTokenExpiresAt = 0
	}
	return nil
}

// Mount registers the protocol's paths on the router. The patterns name
// exactly the endpoints the provider owns — the registration and other
// unmounted provider routes stay unreachable, so the guard's unnamed-path
// refusal covers everything under /oidc the protocol does not serve.
func (p *Protocol) Mount(r chi.Router) {
	handler := p.provider.Handler()
	flatten := flattenBasicAuth()
	// The grant snapshot middleware arms the compare-and-swap a one-time
	// grant's consumption needs: on these routes the library reads a
	// grant and saves it back apart, and the save must find the row
	// holding the document the request read.
	snapshots := grantSnapshotMiddleware
	r.Handle(protocolPrefix+protocolAuthorizeEndpoint, handler)
	r.Handle(protocolPrefix+protocolAuthorizeEndpoint+"/*", handler)
	r.Handle(protocolPrefix+protocolTokenEndpoint, snapshots(flatten(handler)))
	r.Handle(protocolPrefix+protocolUserInfoEndpoint, handler)
	r.Handle(protocolPrefix+protocolIntrospectEndpoint, snapshots(flatten(handler)))
	r.Handle(protocolPrefix+protocolRevokeEndpoint, snapshots(flatten(handler)))
	r.Handle(protocolPrefix+protocolPAREndpoint, flatten(handler))
	r.Handle(protocolPrefix+protocolEndSessionEndpoint, handler)
	// The JWKS document rides the discovery document's jwks_uri, which the
	// library renders under the protocol prefix — the root path the jwks
	// feature serves is the legacy face, the prefix one is the advertised
	// one. Both must answer, or a relying party that fetched the metadata
	// finds a 404 where its keys should be.
	r.Handle(protocolPrefix+protocolJWKSEndpoint, handler)
	// The registration endpoint rides the discovery document because the
	// DCR manager is what resolves clients by. Registration itself is the
	// management surface's job — the store refuses every write — so the
	// mounted endpoint answers the library's registration error, the
	// metadata staying honest about where a registration request lands.
	r.Handle(protocolPrefix+protocolRegisterEndpoint, flatten(handler))
	// The device verification endpoint serves the browser's entry and
	// its callback continuation, so the subtree mounts with the handler.
	r.Handle(protocolPrefix+protocolDeviceAuthorizeEndpoint, flatten(handler))
	r.Handle(protocolPrefix+protocolDeviceVerificationEndpoint, handler)
	r.Handle(protocolPrefix+protocolDeviceVerificationEndpoint+"/*", handler)
	r.Handle("/.well-known/openid-configuration", handler)
	// The RFC 8414 alias is the OAuth face of the same document: the
	// provider registers the discovery handler on the openid-configuration
	// path alone, so the alias re-points the request at it. The document
	// already names every authorization-server member an RFC 8414 client
	// reads — issuer, the endpoints, grant types, scopes, and the
	// introspection and revocation surfaces with their authentication
	// methods — and one document cannot drift from itself.
	r.Handle("/.well-known/oauth-authorization-server",
		requestAt("/.well-known/openid-configuration", handler))
}

// requestAt re-points a request's path before the handler sees it. The
// provider's mux matches on the URL path, so the alias is the same path
// the registration names.
func requestAt(path string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = path
		r.URL.RawPath = ""
		next.ServeHTTP(w, r)
	})
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
// authentication method per client, and secret-post is the one saka's
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

// protocolScopes are the scopes a relying party may ask for. Each is
// its own word: a scope matches when it is named exactly, openid
// included — the library calls the matcher for every requested word.
// offline_access is the persistent-access ask: the grant that carries
// it receives the long refresh window, and the consent question
// renders it as its own line.
func protocolScopes() []goidc.Scope {
	scopes := []goidc.Scope{}
	for _, id := range []string{"openid", "profile", "email", "groups", ScopeOfflineAccess} {
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

// registeredClaims are the JWT registered claims the tokens mint — the
// names a relying party can expect beside whatever the scopes gate.
// at_hash is absent on purpose: the engine's code flow does not carry the
// access token into the ID-token issuance, and the claim stays optional
// under OIDC Core §3.1.3.6.
var registeredClaims = []string{"sub", "iss", "aud", "exp", "iat", "auth_time", "nonce", "jti", "sid"}

// claimsSupported is the discovery document's claims_supported. The list is
// informational — the claims request parameter is not enabled — but it
// tells the relying party what the surface answers: the registered claims
// above, then the scope-gated profile claims.
func claimsSupported() []string {
	return append(append([]string{}, registeredClaims...), profileClaims...)
}

// protocolACRs are the authentication context references the authorize
// endpoint accepts. The rehearsal profiles send the LoA numbers — the
// values the conformance suite's Basic OP modules name — and the
// authentication the provider performs answers none of them specially
// today: the requested acr_values validate against this list, the token
// carries no acr claim until the sign-in policy learns to grade itself.
func protocolACRs() []goidc.ACR {
	return []goidc.ACR{"1", "2"}
}

// protectedClaimKeys are the claims a custom claim must never replace:
// the registered JWT claim names the library mints (it copies the claim
// map over its own, so a collision would overwrite the token's subject,
// issuer, audience, or times), the profile claims above, and the
// internal token-type discriminator. Legacy rows carrying such a key are
// silently dropped at issuance — the write side refuses them, this is
// the second line of defense.
var protectedClaimKeys = map[string]struct{}{
	"sub": {}, "iss": {}, "aud": {}, "exp": {}, "iat": {}, "nbf": {}, "jti": {},
	"sid": {}, "auth_time": {}, "nonce": {}, "acr": {}, "amr": {}, "azp": {}, "client_id": {},
	"at_hash": {}, "c_hash": {}, "s_hash": {},
	"given_name": {}, "family_name": {}, "name": {}, "display_name": {},
	"preferred_username": {}, "picture": {}, "email": {}, "email_verified": {},
	"updated_at":      {},
	"groups":          {},
	"saka:token_type": {},
}

// idTokenClaims merges the account facts and the operator-defined claims
// into an ID token at issuance. An account the directory no longer knows
// names no claims — the grant fails closed downstream. The sid member is
// the grant's own identifier: the RP's session correlation for Back-Channel
// Logout (a client registered `backchannel_logout_session_required` reads
// it from the logout token), and the end-session hint's correlation key.
func idTokenClaims(service *Service) goidc.IDTokenClaimsFunc {
	return func(ctx context.Context, grant *goidc.Grant) map[string]any {
		claims := subjectClaims(ctx, service, service.claims, grant)
		if grant.ID != "" {
			claims["sid"] = grant.ID
		}
		// The authentication instant the completion stamped — the claim
		// `max_age` is judged against, required whenever the request
		// carried the parameter. The stored grant round-trips through
		// JSON, so the number reads back as a float64.
		if raw, ok := grant.Store[authTimeStoreKey]; ok {
			switch at := raw.(type) {
			case int:
				if at > 0 {
					claims["auth_time"] = at
				}
			case float64:
				if at > 0 {
					claims["auth_time"] = int(at)
				}
			}
		}
		return claims
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
		if view.Picture != "" {
			// picture is the Standard Claim name for the account's
			// portrait — the composed public URL, absent for an account
			// with no picture rather than an empty string.
			result["picture"] = view.Picture
		}
		// updated_at is a JSON number, seconds since the Unix epoch — the
		// representation §5.1 names. A NULL stamp is the account no update
		// has touched: its information last changed when it was created,
		// so the claim falls back to the creation stamp.
		updated := view.UpdatedAt
		if updated == nil {
			updated = &view.CreatedAt
		}
		result["updated_at"] = updated.Unix()
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
					if _, protected := protectedClaimKeys[claim.Key]; !protected {
						result[claim.Key] = claim.Value
					}
				}
			}
			if groupClaims, err := claims.GroupClaims(ctx, groupIDs(view)); err == nil {
				for _, claim := range groupClaims {
					if _, protected := protectedClaimKeys[claim.Key]; !protected {
						result[claim.Key] = claim.Value
					}
				}
			}
		}
	}
	return result
}
