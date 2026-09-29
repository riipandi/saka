package oidc

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/luikyv/go-oidc/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The RFC 8414 alias: the authorization-server document answers at the
// alias path with the same bytes the OIDC discovery document answers
// with, and names the revocation surface phase 2 delivered.

func metadataProvider(t *testing.T) http.Handler {
	t.Helper()

	p, err := provider.New(provider.Config{
		Issuer: "https://tango.example",
		JWKS: func(context.Context) (goidc.JSONWebKeySet, error) {
			return goidc.JSONWebKeySet{}, nil
		},
		IDTokenAlgs: []goidc.SignatureAlgorithm{goidc.SigAlgES256},
	},
		provider.WithPathPrefix(protocolPrefix),
		provider.WithTokenIntrospection(func(_ context.Context, client *goidc.Client, info goidc.TokenInfo) bool {
			return info.ClientID == client.ID
		}),
		provider.WithTokenRevocation(revocationPolicy(),
			provider.WithTokenRevocationRevokeGrantOnAccessToken(),
			provider.WithTokenRevocationEndpoint(protocolRevokeEndpoint)),
	)
	require.NoError(t, err)

	handler := p.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The alias wiring Mount performs.
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			handler = requestAt("/.well-known/openid-configuration", handler)
		}
		handler.ServeHTTP(w, r)
	})
}

func TestTheRFC8414AliasServesTheDiscoveryDocument(t *testing.T) {
	server := httptest.NewServer(metadataProvider(t))
	t.Cleanup(server.Close)

	openid, err := server.Client().Get(server.URL + "/.well-known/openid-configuration")
	require.NoError(t, err)
	defer openid.Body.Close()
	require.Equal(t, http.StatusOK, openid.StatusCode)
	openidBody, err := io.ReadAll(openid.Body)
	require.NoError(t, err)

	alias, err := server.Client().Get(server.URL + "/.well-known/oauth-authorization-server")
	require.NoError(t, err)
	defer alias.Body.Close()
	require.Equal(t, http.StatusOK, alias.StatusCode)
	aliasBody, err := io.ReadAll(alias.Body)
	require.NoError(t, err)

	assert.JSONEq(t, string(openidBody), string(aliasBody),
		"one document cannot drift from itself")

	var document struct {
		Issuer             string   `json:"issuer"`
		RevocationEndpoint string   `json:"revocation_endpoint"`
		RevocationAuthn    []string `json:"revocation_endpoint_auth_methods_supported"`
		IntrospectEndpoint string   `json:"introspection_endpoint"`
		Scopes             []string `json:"scopes_supported"`
	}
	require.NoError(t, json.Unmarshal(aliasBody, &document))
	assert.Equal(t, "https://tango.example", document.Issuer)
	assert.Equal(t, "https://tango.example"+protocolPrefix+protocolRevokeEndpoint,
		document.RevocationEndpoint)
	assert.NotEmpty(t, document.RevocationAuthn, "the revocation surface names its client authentication methods")
	assert.Equal(t, "https://tango.example"+protocolPrefix+protocolIntrospectEndpoint,
		document.IntrospectEndpoint)
	assert.NotEmpty(t, document.Scopes)
}
