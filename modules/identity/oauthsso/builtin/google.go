package builtin

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/riipandi/saka/modules/identity/oauthsso"
)

// Google is the OIDC provider. The scopes are the minimum the identity
// resolution reads: the address, its verified flag, and the names.
var Google = Definition{
	Slug:             "google",
	DisplayName:      "Google",
	Scopes:           []string{"openid", "email", "profile"},
	OIDC:             true,
	Issuer:           "https://accounts.google.com",
	AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:         "https://oauth2.googleapis.com/token",
	JwksURL:          "https://www.googleapis.com/oauth2/v3/certs",
}

// googleProvider is the Google adapter: an OIDC provider whose id_token
// carries the whole identity — subject, address, verified flag, names —
// so no userinfo read rides the flow. The endpoints are the code's own;
// a test that drives the flow against a fake provider replaces them in
// this package.
type googleProvider struct {
	issuer  string
	jwksURL string
	def     Definition
}

// NewGoogle builds the Google adapter over the shipped definition.
func NewGoogle() *googleProvider {
	return &googleProvider{issuer: Google.Issuer, jwksURL: Google.JwksURL, def: Google}
}

// TokenEndpoint is the provider's fixed token endpoint — the URL the
// token client presents the refresh grant to.
func (g *googleProvider) TokenEndpoint(conn oauthsso.Connection) string {
	return g.def.TokenURL
}

// AuthorizeURL renders Google's authorize URL: the PKCE challenge, the
// nonce the id_token must answer, and offline access so a refresh token
// rides the answer when the provider grants one.
func (g *googleProvider) AuthorizeURL(conn oauthsso.Connection, flow oauthsso.FlowSecrets) (string, error) {
	url, err := authorizeURL(g.def, conn, flow)
	if err != nil {
		return "", err
	}
	return url, nil
}

// Resolve exchanges the code and verifies the id_token against the
// published key set: issuer, audience, signature, and the nonce the flow
// minted. The claims the resolution reads are the id_token's alone.
func (g *googleProvider) Resolve(ctx context.Context, conn oauthsso.Connection, flow oauthsso.FlowSecrets, code string) (oauthsso.ExternalIdentity, error) {
	cfg := configOf(g.def, conn, flow)
	token, err := exchangeCode(ctx, cfg, code, flow.Verifier)
	if err != nil {
		return oauthsso.ExternalIdentity{}, err
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the token endpoint answered no id_token", oauthsso.ErrResolutionFailed)
	}

	keySet := oidc.NewRemoteKeySet(ctx, g.jwksURL)
	verifier := oidc.NewVerifier(g.issuer, keySet, &oidc.Config{ClientID: conn.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the id_token did not verify: %v", oauthsso.ErrResolutionFailed, err)
	}
	// The nonce check is the caller's, not the library's: a replayed
	// authorization response carries a stale nonce.
	if flow.Nonce != "" && idToken.Nonce != flow.Nonce {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the id_token answered a stale nonce", oauthsso.ErrResolutionFailed)
	}

	var claims struct {
		Subject       string       `json:"sub"`
		Email         string       `json:"email"`
		EmailVerified flexibleBool `json:"email_verified"`
		GivenName     string       `json:"given_name"`
		FamilyName    string       `json:"family_name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the id_token's claims did not parse: %v", oauthsso.ErrResolutionFailed, err)
	}
	if claims.Subject == "" || claims.Email == "" {
		return oauthsso.ExternalIdentity{}, fmt.Errorf("%w: the id_token named no bindable identity", oauthsso.ErrIdentityInvalid)
	}

	return oauthsso.ExternalIdentity{
		ProviderAccountID:    claims.Subject,
		Email:                claims.Email,
		EmailVerified:        bool(claims.EmailVerified),
		GivenName:            claims.GivenName,
		FamilyName:           claims.FamilyName,
		AccessToken:          token.AccessToken,
		RefreshToken:         token.RefreshToken,
		AccessTokenExpiresAt: token.Expiry,
	}, nil
}
