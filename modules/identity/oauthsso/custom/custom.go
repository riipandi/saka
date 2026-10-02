// Package custom is the generic OIDC adapter: the provider every
// operator-configured custom connection talks through. The endpoints it
// reads are the connection row's own — either the discovery document's
// resolution or the operator's manual set, validated when the row was
// written — so the adapter carries no endpoints of its own.
//
// The claims come from the id_token, verified against the connection's
// key set; when the connection names a userinfo endpoint, the userinfo
// response is the claim source and the id_token only proves the
// identity. The attribute mapping names which claim answers each account
// field, with the standard claim names as the defaults.
package custom

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

// Provider is the generic OIDC adapter.
type Provider struct {
	fetcher oauthsso.IdentityFetcher
}

// New builds the adapter. A nil fetcher leaves the userinfo read
// unavailable — a connection that names a userinfo endpoint then refuses
// at the resolution rather than skipping the source the operator chose.
func New(fetcher oauthsso.IdentityFetcher) *Provider {
	return &Provider{fetcher: fetcher}
}

// AuthorizeURL renders the connection's authorize URL: the PKCE
// challenge and the nonce the id_token must answer.
func (p *Provider) AuthorizeURL(conn oauthsso.Connection, flow oauthsso.FlowSecrets) (string, error) {
	cfg := p.config(conn, flow)
	return cfg.AuthCodeURL(flow.State,
		oauth2.SetAuthURLParam("code_challenge", pkceS256(flow.Verifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oidc.Nonce(flow.Nonce),
	), nil
}

// Resolve exchanges the code and proves the identity: the id_token's
// signature against the connection's key set, its audience against the
// connection's client id, its nonce against the flow's, and its issuer
// against the connection's — skipped only when the manual endpoint set
// named no issuer. The claims follow the mapping, and a configured
// userinfo endpoint is the claim source that overrides the id_token's.
func (p *Provider) Resolve(ctx context.Context, conn oauthsso.Connection, flow oauthsso.FlowSecrets, code string) (oauthsso.ExternalIdentity, error) {
	cfg := p.config(conn, flow)
	token, err := cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", flow.Verifier))
	if err != nil {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: %v", oauthsso.ErrResolutionFailed, err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the token endpoint answered no id_token", oauthsso.ErrResolutionFailed)
	}

	keySet := oidc.NewRemoteKeySet(ctx, conn.Endpoints.Jwks)
	verifierConfig := &oidc.Config{ClientID: conn.ClientID}
	// A manual endpoint set may name no issuer; the discovered one
	// always does. An unchecked issuer narrows the proof to the
	// signature and the audience — the row's own endpoints were
	// validated when it was written.
	verifierConfig.SkipIssuerCheck = conn.Endpoints.Issuer == ""
	verifier := oidc.NewVerifier(conn.Endpoints.Issuer, keySet, verifierConfig)
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the id_token did not verify: %v", oauthsso.ErrResolutionFailed, err)
	}
	if flow.Nonce != "" && idToken.Nonce != flow.Nonce {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the id_token answered a stale nonce", oauthsso.ErrResolutionFailed)
	}

	claims := claimSet{}
	if err := idToken.Claims(&claims); err != nil {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the id_token's claims did not parse: %v", oauthsso.ErrResolutionFailed, err)
	}

	// The userinfo response is the claim source when the connection
	// names it: the id_token proved the identity, the endpoint speaks
	// for the attributes.
	if conn.Endpoints.Userinfo != "" {
		userinfo, err := p.readUserinfo(ctx, token.AccessToken, conn.Endpoints.Userinfo)
		if err != nil {
			return oauthsso.ExternalIdentity{}, err
		}
		claims = claims.merged(userinfo)
	}

	// The bindability judge is the mapped email, not the standard claim
	// alone: a connection that maps `mail` into the account's address
	// names no `email` claim at all, and the mapping is what speaks.
	email := claims.mappedEmail(conn.AttributeMapping)
	if claims.Subject == "" || email == "" {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the provider named no bindable identity", oauthsso.ErrIdentityInvalid)
	}

	profile, _ := json.Marshal(claims.raw)
	return oauthsso.ExternalIdentity{
		ProviderAccountID: claims.Subject,
		Email:             email,
		EmailVerified:     claims.EmailVerified.value(),
		GivenName:         claims.mappedGiven(conn.AttributeMapping),
		FamilyName:        claims.mappedFamily(conn.AttributeMapping),
		Profile:           profile,
		AccessToken:       token.AccessToken,
		RefreshToken:      token.RefreshToken,
	}, nil
}

// config renders the oauth2 client the connection's endpoints name. An
// empty scope list falls back to the standard OIDC trio — an authorize
// request without `openid` answers no id_token, and a connection the
// operator scoped loosely must still resolve.
func (p *Provider) config(conn oauthsso.Connection, flow oauthsso.FlowSecrets) oauth2.Config {
	scopes := conn.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	return oauth2.Config{
		ClientID:     conn.ClientID,
		ClientSecret: conn.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  conn.Endpoints.Authorization,
			TokenURL: conn.Endpoints.Token,
		},
		RedirectURL: flow.RedirectURI,
		Scopes:      scopes,
	}
}

// readUserinfo performs the bearer-authenticated userinfo read.
func (p *Provider) readUserinfo(ctx context.Context, bearer, url string) (claimSet, error) {
	if p.fetcher == nil {
		return claimSet{}, fmt.Errorf("oauthsso: no outbound client is configured to read the userinfo endpoint")
	}
	status, body, err := p.fetcher.Do(ctx, url, bearer)
	if err != nil {
		return claimSet{}, fmt.Errorf("%w: the userinfo read failed: %v", oauthsso.ErrResolutionFailed, err)
	}
	if status != 200 {
		return claimSet{}, fmt.Errorf("%w: the userinfo read answered %d", oauthsso.ErrResolutionFailed, status)
	}
	set := claimSet{raw: map[string]any{}}
	if err := json.Unmarshal(body, &set); err != nil {
		return claimSet{}, fmt.Errorf("%w: the userinfo document did not parse: %v", oauthsso.ErrResolutionFailed, err)
	}
	return set, nil
}

// claimSet is the claim document one identity read answered. The named
// fields are what every provider carries; raw keeps the whole document
// so a custom mapping's source of truth survives the read.
type claimSet struct {
	Subject       string       `json:"sub"`
	Email         string       `json:"email"`
	EmailVerified flexibleBool `json:"email_verified"`
	GivenName     string       `json:"given_name"`
	FamilyName    string       `json:"family_name"`

	raw map[string]any
}

// UnmarshalJSON decodes the named fields and keeps the whole document.
func (c *claimSet) UnmarshalJSON(data []byte) error {
	type named struct {
		Subject       string       `json:"sub"`
		Email         string       `json:"email"`
		EmailVerified flexibleBool `json:"email_verified"`
		GivenName     string       `json:"given_name"`
		FamilyName    string       `json:"family_name"`
	}
	var fields named
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	c.Subject = fields.Subject
	c.Email = fields.Email
	c.EmailVerified = fields.EmailVerified
	c.GivenName = fields.GivenName
	c.FamilyName = fields.FamilyName
	if c.raw == nil {
		c.raw = map[string]any{}
	}
	if err := json.Unmarshal(data, &c.raw); err != nil {
		return err
	}
	return nil
}

// merged folds a second claim document over the first: the userinfo's
// answer wins where it names a value, and its raw claims join the
// document the mapping reads — the userinfo response is the claim source
// the operator configured, so a mapped name must find it there.
func (c claimSet) merged(other claimSet) claimSet {
	if other.Email != "" {
		c.Email = other.Email
	}
	if other.raw != nil {
		if _, ok := other.raw["email_verified"]; ok {
			c.EmailVerified = other.EmailVerified
		}
	}
	if other.GivenName != "" {
		c.GivenName = other.GivenName
	}
	if other.FamilyName != "" {
		c.FamilyName = other.FamilyName
	}
	if other.Subject != "" {
		c.Subject = other.Subject
	}
	if c.raw == nil {
		c.raw = other.raw
		return c
	}
	for key, value := range other.raw {
		c.raw[key] = value
	}
	return c
}

// mappedEmail reads the claim the mapping names — `email` when the
// mapping is silent — from the raw document.
func (c claimSet) mappedEmail(mapping oauthsso.AttributeMapping) string {
	name := mapping.Email
	if name == "" {
		name = "email"
	}
	return c.string(name, c.Email)
}

// mappedGiven reads the claim the mapping names — `given_name` when the
// mapping is silent — from the raw document.
func (c claimSet) mappedGiven(mapping oauthsso.AttributeMapping) string {
	name := mapping.GivenName
	if name == "" {
		name = "given_name"
	}
	return c.string(name, c.GivenName)
}

// mappedFamily reads the claim the mapping names — `family_name` when
// the mapping is silent — from the raw document.
func (c claimSet) mappedFamily(mapping oauthsso.AttributeMapping) string {
	name := mapping.FamilyName
	if name == "" {
		name = "family_name"
	}
	return c.string(name, c.FamilyName)
}

// string reads one claim from the raw document, answering the fallback
// when the claim is absent or not a string.
func (c claimSet) string(name, fallback string) string {
	if c.raw == nil {
		return fallback
	}
	if value, ok := c.raw[name].(string); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

// pkceS256 derives the S256 challenge the authorize request carries.
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

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

// value answers the bool the claim carries.
func (b flexibleBool) value() bool { return bool(b) }
