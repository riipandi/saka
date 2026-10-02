package builtin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/riipandi/tango/modules/identity/oauthsso"
)

// flexibleBool reads an email_verified claim that arrives as a JSON
// boolean or as one of the strings some providers send instead.
type flexibleBool bool

// UnmarshalJSON accepts the shapes providers actually send.
func (b *flexibleBool) UnmarshalJSON(data []byte) error {
	var asBool bool
	if err := json.Unmarshal(data, &asBool); err == nil {
		*b = flexibleBool(asBool)
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		*b = flexibleBool(strings.EqualFold(asString, "true"))
		return nil
	}
	return fmt.Errorf("oauthsso: email_verified is neither a boolean nor a string")
}

// pkceS256 derives the S256 challenge the authorize request carries from
// the verifier the flow sealed.
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// configOf renders the oauth2 client a builtin provider's connection
// talks through. The connection's client secret is the clear form — the
// service unsealed it at the boundary — and the scope list is the row's
// own, falling back to the definition's when the operator stored none.
func configOf(def Definition, conn oauthsso.Connection, flow oauthsso.FlowSecrets) oauth2.Config {
	scopes := conn.Scopes
	if len(scopes) == 0 {
		scopes = def.Scopes
	}
	return oauth2.Config{
		ClientID:     conn.ClientID,
		ClientSecret: conn.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  def.AuthorizationURL,
			TokenURL: def.TokenURL,
		},
		RedirectURL: flow.RedirectURI,
		Scopes:      scopes,
	}
}

// authorizeURL renders the provider's authorize URL with the PKCE
// challenge; an OIDC provider carries the nonce beside it.
func authorizeURL(def Definition, conn oauthsso.Connection, flow oauthsso.FlowSecrets) (string, error) {
	cfg := configOf(def, conn, flow)
	options := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_challenge", pkceS256(flow.Verifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}
	if def.OIDC {
		options = append(options, oidc.Nonce(flow.Nonce))
	}
	return cfg.AuthCodeURL(flow.State, options...), nil
}

// exchangeCode spends the callback's code at the token endpoint with the
// PKCE verifier. Every failure — a spent code, a wrong verifier, a
// refused client — is the same resolution failure; the answer never says
// which.
func exchangeCode(ctx context.Context, cfg oauth2.Config, code, verifier string) (*oauth2.Token, error) {
	token, err := cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", oauthsso.ErrResolutionFailed, err)
	}
	if token == nil || token.AccessToken == "" {
		return nil, fmt.Errorf("%w: the token endpoint answered no access token", oauthsso.ErrResolutionFailed)
	}
	return token, nil
}
