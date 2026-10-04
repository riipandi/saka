package oauthsso

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/riipandi/saka/internal/fetcher"
)

// The failures the provider adapters report. The handler maps them onto
// the codes the Connect protocol carries and the callback renders as the
// redirect the browser crosses; the service defines what happened, not
// how it is answered.
var (
	// ErrResolutionFailed is a code exchange or an identity read the
	// provider refused: a spent or wrong code, a verifier that does not
	// match, a token endpoint that answered an error.
	ErrResolutionFailed = errors.New("oauthsso: the provider refused the code exchange")

	// ErrIdentityInvalid is a verified response that does not name an
	// identity the feature can bind: no provider account id, or no
	// address at all.
	ErrIdentityInvalid = errors.New("oauthsso: the provider answered no bindable identity")
)

// ExternalIdentity is what an adapter answers after the code exchange:
// the provider identity the resolution binds, with the flags and names
// the account model reads. The raw claim document rides along so a
// custom mapping's source of truth is never lost.
type ExternalIdentity struct {
	// ProviderAccountID is the provider's own stable identifier for the
	// identity — the `sub` claim of a verified id_token, the account id
	// of the provider's user API.
	ProviderAccountID string
	// Email is the address the identity carries.
	Email string
	// EmailVerified is the provider's own word for the address: the
	// linking rule and the JIT gate judge this flag, never the address's
	// shape.
	EmailVerified bool
	// GivenName and FamilyName are the names the provider answered;
	// either may be empty, and an empty pair is what sends the flow to
	// the names stage.
	GivenName  string
	FamilyName string
	// Username is the provider's word for the account's username — read
	// at the first sign-in only, the account's own derivation keeping
	// the fallback. Empty is a provider that answered none.
	Username string
	// AvatarURL is the address the account's picture is read from;
	// empty is a provider that answered none.
	AvatarURL string
	// Profile is the raw claim document the answer came from, stored
	// with the linked account for the operator's forensics.
	Profile []byte
	// AccessToken and RefreshToken are the tokens the provider minted.
	// Either may be empty — a provider that answered no refresh token
	// for the scopes asked.
	AccessToken  string
	RefreshToken string
	// AccessTokenExpiresAt is when the access token dies, named by the
	// provider's expires_in. Zero is a provider that answered none — the
	// token's age is then unknown to the binding.
	AccessTokenExpiresAt time.Time
}

// FlowSecrets are the per-flow values the authorize request and the code
// exchange carry: the state the callback matches, the nonce an OIDC
// id_token must answer, the PKCE verifier the token exchange presents,
// and the redirect URI the connection registered with the provider. The
// secrets travel into the adapter unsealed — the service unseals the
// verifier and the secret at the boundary and never stores the raw forms.
type FlowSecrets struct {
	State       string
	Nonce       string
	Verifier    string
	RedirectURI string
}

// Provider is one adapter's contract: turn a flow's secrets into an
// authorize URL, and turn the callback's code into a verified identity.
//
// The connection the adapters receive carries the client secret in the
// clear — the service unseals it at the boundary, and the sealed form
// never leaves it. The endpoints a builtin adapter talks to are its own;
// a custom adapter reads the endpoints the row resolved at write time.
type Provider interface {
	// AuthorizeURL renders the provider's authorize URL for the flow.
	AuthorizeURL(conn Connection, flow FlowSecrets) (string, error)
	// Resolve exchanges the code for tokens and reads the verified
	// identity. The nonce the flow minted is what an OIDC id_token must
	// answer; an OAuth2-only provider ignores it.
	Resolve(ctx context.Context, conn Connection, flow FlowSecrets, code string) (ExternalIdentity, error)
	// TokenEndpoint is the URL the token client presents the refresh
	// grant to — the adapter's own fixed endpoint for a builtin, the
	// connection's stored endpoint for a custom one. Empty is a
	// connection the client cannot refresh against.
	TokenEndpoint(conn Connection) string
}

// IdentityFetcher is the authenticated read an adapter makes against a
// provider's identity API: the bearer token the exchange minted, the
// document the identity comes from. The shared outbound client satisfies
// it; the tests hand a stub.
type IdentityFetcher interface {
	Do(ctx context.Context, url string, bearer string) (status int, body []byte, err error)
}

// BearerFetchAdapter adapts the shared fetcher client onto the identity
// read seam.
func BearerFetchAdapter(client *fetcher.Client) IdentityFetcher {
	return IdentityFetcherFunc(func(ctx context.Context, rawurl, bearer string) (int, []byte, error) {
		res, err := client.Do(ctx, fetcher.Request{
			Method:  http.MethodGet,
			URL:     rawurl,
			Headers: http.Header{"Authorization": []string{"Bearer " + bearer}},
		})
		if err != nil {
			return 0, nil, err
		}
		return res.StatusCode, res.Body, nil
	})
}

// IdentityFetcherFunc adapts a function onto the identity read seam.
type IdentityFetcherFunc func(ctx context.Context, url string, bearer string) (int, []byte, error)

// Do runs the function.
func (f IdentityFetcherFunc) Do(ctx context.Context, url string, bearer string) (int, []byte, error) {
	return f(ctx, url, bearer)
}

// TokenPoster is the form POST the token client presents the refresh
// grant with: the endpoint URL, the form fields the grant carries. The
// shared outbound client satisfies it; the tests hand a stub.
type TokenPoster interface {
	PostForm(ctx context.Context, url string, form url.Values) (status int, body []byte, err error)
}

// TokenPosterFunc adapts a function onto the token POST seam.
type TokenPosterFunc func(ctx context.Context, url string, form url.Values) (int, []byte, error)

// PostForm runs the function.
func (f TokenPosterFunc) PostForm(ctx context.Context, url string, form url.Values) (int, []byte, error) {
	return f(ctx, url, form)
}

// FormPostAdapter adapts the shared fetcher client onto the token POST
// seam: the form encodes as the body string, the content type names it,
// and the client's policy — timeouts, body bound, no credential logged —
// is the one every outbound call shares. A classified answer still
// carries the response: the token endpoint's refusal is the protocol's
// own answer (the RFC 6749 error code rides the 4xx body the caller
// parses), so the response wins over the fetcher's verdict — only a
// call that never answered is a transport failure.
func FormPostAdapter(client *fetcher.Client) TokenPoster {
	return TokenPosterFunc(func(ctx context.Context, rawurl string, form url.Values) (int, []byte, error) {
		res, err := client.Do(ctx, fetcher.Request{
			Method:  http.MethodPost,
			URL:     rawurl,
			Headers: http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}},
			Body:    form.Encode(),
		})
		if res != nil {
			return res.StatusCode, res.Body, nil
		}
		if err != nil {
			return 0, nil, err
		}
		return 0, nil, fmt.Errorf("fetcher: the token endpoint answered no response")
	})
}
