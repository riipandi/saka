package oauthsso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/riipandi/saka/framework/datastore"
)

// The flow's fixed terms: the window one ceremony stays answerable, the
// SPA route the browser lands on after the callback, and the sizes the
// secrets are drawn at. A ceremony the browser walks in ten seconds does
// not need more; a lost browser's row is dead weight the sweep collects.
const (
	flowLifetime = 10 * time.Minute

	// spaCallbackPath is the SPA route the callback redirects to, with
	// the flow token or the error code riding the query. The path sits
	// OUTSIDE the reserved surface prefixes (/oauth among them) — the
	// shell never serves those as the SPA document, so a redirect into
	// one would answer the envelope's 404, never the page.
	spaCallbackPath = "/auth/callback"
	secretBytes     = 32
)

// The failures the flow reports. The handler maps them onto the connect
// codes and the redirect the browser crosses.
var (
	// ErrConnectionUnavailable is a begin for a slug that names no
	// connection, or a disabled one. The same answer both ways keeps the
	// begin from enumerating the deployment's connections.
	ErrConnectionUnavailable = errors.New("oauthsso: no enabled connection answers this provider")

	// ErrFlowUnknown is a state or a flow token that names no live
	// ceremony: unknown, spent, or expired. The same answer all three
	// ways keeps a replay from learning which half failed.
	ErrFlowUnknown = errors.New("oauthsso: no live flow answers this handle")
)

// WithBaseURL sets the origin the redirect URI and the SPA redirect are
// built from. A configuration without one fails the flow at the begin —
// the redirect URI is what the connection registered with the provider.
func (s *Service) WithBaseURL(baseURL string) *Service {
	s.baseURL = baseURL
	return s
}

// WithOAuthEnabled wires the configuration's master switch over the whole
// surface. The providers are runtime data the connection rows own; this
// switch is the configuration's one word in the matter, and a change to it
// takes the restart every configuration key takes.
func (s *Service) WithOAuthEnabled(enabled bool) *Service {
	s.ssoEnabled = enabled
	return s
}

// Begin opens one authorization-code ceremony: it draws the secrets,
// renders the provider's authorize URL, and stores the pending row the
// callback will consume. The raw state and verifier never rest anywhere —
// the row carries their hash and their sealed form, and the browser
// carries the raw state to the provider and back.
func (s *Service) Begin(ctx context.Context, provider string) (string, error) {
	if !s.ssoEnabled {
		return "", ErrConnectionUnavailable
	}
	conn, err := s.repo.ByProvider(ctx, s.pool, provider)
	if errors.Is(err, ErrConnectionNotFound) {
		return "", ErrConnectionUnavailable
	}
	if err != nil {
		return "", err
	}
	if !conn.Enabled {
		return "", ErrConnectionUnavailable
	}
	adapter, err := s.providerFor(conn)
	if err != nil {
		return "", err
	}
	secret, err := s.unseal(conn.ClientSecret)
	if err != nil {
		return "", err
	}
	conn.ClientSecret = secret

	state, err := newSecret()
	if err != nil {
		return "", err
	}
	verifier, err := newSecret()
	if err != nil {
		return "", err
	}
	// The nonce rides every OIDC authorize request; an OAuth2-only
	// provider ignores it, so one draw serves both shapes.
	nonce, err := newSecret()
	if err != nil {
		return "", err
	}

	secrets := FlowSecrets{
		State:       state,
		Nonce:       nonce,
		Verifier:    verifier,
		RedirectURI: s.redirectURI(conn.Provider),
	}
	authorizeURL, err := adapter.AuthorizeURL(conn, secrets)
	if err != nil {
		return "", err
	}

	sealedVerifier, err := s.seal(verifier)
	if err != nil {
		return "", err
	}
	if _, err := s.repo.CreateFlow(ctx, s.pool, conn.ID,
		hashSecret(state), nonce, sealedVerifier, s.now().Add(flowLifetime)); err != nil {
		return "", err
	}
	return authorizeURL, nil
}

// Callback consumes the ceremony the provider returned the browser with:
// the pending row is spent by a guarded update that writes the resolved
// identity and the fresh flow token's hash, and the raw flow token is
// what the SPA redirect carries. A resolution the provider refused leaves
// the row pending — it expires on its own, and the code the provider
// minted is single-use anyway.
func (s *Service) Callback(ctx context.Context, provider, code, state string) (string, error) {
	conn, err := s.repo.ByProvider(ctx, s.pool, provider)
	if errors.Is(err, ErrConnectionNotFound) {
		return "", ErrConnectionUnavailable
	}
	if err != nil {
		return "", err
	}
	if !conn.Enabled {
		return "", ErrConnectionUnavailable
	}
	adapter, err := s.providerFor(conn)
	if err != nil {
		return "", err
	}
	secret, err := s.unseal(conn.ClientSecret)
	if err != nil {
		return "", err
	}
	conn.ClientSecret = secret

	flow, err := s.repo.PendingByState(ctx, hashSecret(state))
	if errors.Is(err, datastore.ErrNoRows) {
		return "", ErrFlowUnknown
	}
	if err != nil {
		return "", err
	}
	verifier, err := s.unseal(flow.CodeVerifier)
	if err != nil {
		return "", err
	}

	identity, err := adapter.Resolve(ctx, conn, FlowSecrets{
		State:       state,
		Nonce:       flow.Nonce,
		Verifier:    verifier,
		RedirectURI: s.redirectURI(conn.Provider),
	}, code)
	if err != nil {
		return "", err
	}
	if identity.Email == "" || identity.ProviderAccountID == "" {
		return "", ErrIdentityInvalid
	}

	sealedAccess, err := s.seal(identity.AccessToken)
	if err != nil {
		return "", err
	}
	sealedRefresh, err := s.seal(identity.RefreshToken)
	if err != nil {
		return "", err
	}
	flowToken, err := newSecret()
	if err != nil {
		return "", err
	}
	consumed, err := s.repo.ConsumePending(ctx, s.pool, flow.ID, FlowResolution{
		FlowTokenHash:      hashSecret(flowToken),
		ProviderAccountID:  identity.ProviderAccountID,
		Email:              identity.Email,
		EmailVerified:      identity.EmailVerified,
		GivenName:          identity.GivenName,
		FamilyName:         identity.FamilyName,
		Username:           identity.Username,
		Picture:            identity.Picture,
		Profile:            identity.Profile,
		SealedAccessToken:  sealedAccess,
		SealedRefreshToken: sealedRefresh,
		AccessExpiresAt:    expiresAtOf(identity),
	})
	if err != nil {
		return "", err
	}
	if !consumed {
		// A concurrent callback spent the state first: the ceremony is
		// gone, and the answer says nothing about who won.
		return "", ErrFlowUnknown
	}
	return flowToken, nil
}

// FlowByToken reads the live ceremony the SPA's handle names, restricted
// to the stages the caller serves. It is the completion paths' door, and
// the one read the flow token answers.
func (s *Service) FlowByToken(ctx context.Context, flowToken string, stages ...FlowStage) (Flow, error) {
	flow, err := s.repo.LiveByFlowToken(ctx, s.pool, hashSecret(flowToken), stages...)
	if errors.Is(err, datastore.ErrNoRows) {
		return Flow{}, ErrFlowUnknown
	}
	if err != nil {
		return Flow{}, err
	}
	return flow, nil
}

// redirectURI is the callback URL the connection registered with its
// provider: the feature's own route under the deployment's origin.
func (s *Service) redirectURI(provider string) string {
	return s.baseURL + "/oauth/" + url.PathEscape(provider) + "/callback"
}

// FlowRedirect builds the SPA redirect a resolved ceremony answers with:
// the flow token rides the query, and the SPA drives the rest.
func (s *Service) FlowRedirect(flowToken string) string {
	return s.baseURL + spaCallbackPath + "?flow_token=" + url.QueryEscape(flowToken)
}

// ErrorRedirect builds the SPA redirect a refused ceremony answers with.
// The code is one of the feature's own words — the browser is mid-redirect
// and no JSON envelope reaches it.
func (s *Service) ErrorRedirect(code string) string {
	return s.baseURL + spaCallbackPath + "?error=" + url.QueryEscape(code)
}

// expiresAtOf renders the identity's token expiry for the row: a
// provider that answered none stays NULL, the age the row cannot know.
func expiresAtOf(identity ExternalIdentity) *time.Time {
	if identity.AccessTokenExpiresAt.IsZero() {
		return nil
	}
	expires := identity.AccessTokenExpiresAt
	return &expires
}

// unseal opens a sealed value. A process without the cipher cannot open
// one, and the read fails closed rather than answering the ciphertext.
func (s *Service) unseal(sealed string) (string, error) {
	if s.cipher == nil {
		return "", fmt.Errorf("oauthsso: no cipher is configured to open a sealed value")
	}
	plain, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return "", fmt.Errorf("oauthsso: unseal: %w", err)
	}
	return plain, nil
}

// newSecret draws one 32-byte URL-safe secret: a state, a verifier, a
// nonce, or a flow token. The entropy is the credential's whole budget —
// nothing else guards a ceremony.
func newSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauthsso: draw secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// hashSecret is the SHA-256 digest the rows key the presented secret by.
// The hash is enough for a match and useless for a replay.
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
