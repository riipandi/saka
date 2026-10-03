package builtin

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"

	"github.com/riipandi/saka/modules/identity/oauthsso"
)

// githubProvider is the GitHub adapter: an OAuth2-only provider whose
// identity is read from its user API, with the verified flag carried by
// the address-list endpoint beside it. The primary verified email — not
// the profile's public one — is what the linking rule judges.
type githubProvider struct {
	def     Definition
	fetcher oauthsso.IdentityFetcher
}

// NewGitHub builds the GitHub adapter over the shipped definition and
// the shared outbound fetch the identity reads run through.
func NewGitHub(fetcher oauthsso.IdentityFetcher) *githubProvider {
	return &githubProvider{def: GitHub, fetcher: fetcher}
}

// AuthorizeURL renders GitHub's authorize URL with the PKCE challenge.
// GitHub speaks no OIDC, so no nonce travels.
func (g *githubProvider) AuthorizeURL(conn oauthsso.Connection, flow oauthsso.FlowSecrets) (string, error) {
	return authorizeURL(g.def, conn, flow)
}

// Resolve exchanges the code and reads the identity: the user API for
// the subject and the names, the address list for the verified flag.
func (g *githubProvider) Resolve(ctx context.Context, conn oauthsso.Connection, flow oauthsso.FlowSecrets, code string) (oauthsso.ExternalIdentity, error) {
	cfg := configOf(g.def, conn, flow)
	token, err := exchangeCode(ctx, cfg, code, flow.Verifier)
	if err != nil {
		return oauthsso.ExternalIdentity{}, err
	}

	var profile struct {
		ID    int64   `json:"id"`
		Login string  `json:"login"`
		Name  *string `json:"name"`
		Email *string `json:"email"`
	}
	if err := g.read(ctx, token.AccessToken, g.def.UserinfoURL, &profile); err != nil {
		return oauthsso.ExternalIdentity{}, err
	}
	if profile.ID == 0 {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the user API named no account id", oauthsso.ErrIdentityInvalid)
	}

	// The address list carries what the profile's public email cannot:
	// which address the account has proven, and which one it presents as
	// its own.
	email, verified := g.primaryEmail(ctx, token.AccessToken, profile.Email)

	given, family := splitName(profile.Name)
	profileJSON, _ := json.Marshal(map[string]string{"login": profile.Login})
	return oauthsso.ExternalIdentity{
		ProviderAccountID: strconv.FormatInt(profile.ID, 10),
		Email:             email,
		EmailVerified:     verified,
		GivenName:         given,
		FamilyName:        family,
		Profile:           profileJSON,
		AccessToken:       token.AccessToken,
		RefreshToken:      token.RefreshToken,
	}, nil
}

// primaryEmail reads the address list and answers the primary verified
// entry, falling back to the first verified one, then to the profile's
// own address — which no provider has verified, so the gate stands.
func (g *githubProvider) primaryEmail(ctx context.Context, bearer string, fallback *string) (string, bool) {
	if g.fetcher == nil || g.def.EmailsURL == "" {
		if fallback != nil {
			return *fallback, false
		}
		return "", false
	}

	status, body, err := g.fetcher.Do(ctx, g.def.EmailsURL, bearer)
	if err != nil || status != 200 {
		// A read the provider refused is not a failed resolution: the
		// profile's address stands, unverified, and the gate decides.
		if fallback != nil {
			return *fallback, false
		}
		return "", false
	}

	var entries []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		if fallback != nil {
			return *fallback, false
		}
		return "", false
	}
	for _, entry := range entries {
		if entry.Primary && entry.Verified {
			return entry.Email, true
		}
	}
	for _, entry := range entries {
		if entry.Verified {
			return entry.Email, true
		}
	}
	if fallback != nil {
		return *fallback, false
	}
	return "", false
}

// read performs one bearer-authenticated identity read through the
// shared seam.
func (g *githubProvider) read(ctx context.Context, bearer, url string, into any) error {
	if g.fetcher == nil {
		return fmt.Errorf("oauthsso: no outbound client is configured to read the provider's identity")
	}
	status, body, err := g.fetcher.Do(ctx, url, bearer)
	if err != nil {
		return fmt.Errorf("%w: the identity read failed: %v", oauthsso.ErrResolutionFailed, err)
	}
	if status != 200 {
		return fmt.Errorf("%w: the identity read answered %d", oauthsso.ErrResolutionFailed, status)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: the identity document did not parse: %v", oauthsso.ErrResolutionFailed, err)
	}
	return nil
}

// splitName breaks the provider's single display name into the account
// model's two: the first word is the given name, the rest the family's.
// A GitHub profile that named none answers an empty pair, which is what
// sends the flow to the names stage.
func splitName(name *string) (string, string) {
	if name == nil {
		return "", ""
	}
	words := strings.Fields(*name)
	if len(words) == 0 {
		return "", ""
	}
	if len(words) == 1 {
		return words[0], ""
	}
	return words[0], strings.Join(words[1:], " ")
}
