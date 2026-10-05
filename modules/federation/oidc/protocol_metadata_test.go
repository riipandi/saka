package oidc

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		Issuer: "https://saka.example",
		JWKS: func(context.Context) (goidc.JSONWebKeySet, error) {
			return goidc.JSONWebKeySet{}, nil
		},
		IDTokenAlgs: []goidc.SignatureAlgorithm{goidc.SigAlgES256},
	},
		provider.WithPathPrefix(protocolPrefix),
		provider.WithJWKSEndpoint(protocolJWKSEndpoint),
		provider.WithDCR(clientManagerStub{}),
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
			r.URL.Path = "/.well-known/openid-configuration"
		}
		// The jwks and registration paths pass through unchanged: the
		// provider's own mux matches them under the prefix, which is the
		// shape Mount registers.
		handler.ServeHTTP(w, r)
	})
}

// clientManagerStub is the DCR manager the metadata test's provider needs:
// its lookup answers not-found, its writes refuse — the shape saka's
// clientStore keeps, the registration endpoint mounted yet closed.
type clientManagerStub struct{}

func (clientManagerStub) Client(context.Context, string) (*goidc.Client, error) {
	return nil, goidc.ErrNotFound
}

func (clientManagerStub) SaveClient(context.Context, *goidc.Client) error {
	return goidc.ErrNotFound
}

func (clientManagerStub) DeleteClient(context.Context, string) error {
	return goidc.ErrNotFound
}

// TestTheDiscoveryDocumentPinsThePKCEAndClaimSurface answers for the two
// metadata hygiene fixes the phase 1 audit asked for: the PKCE method list
// names S256 alone (RFC 9700 §4.1.3 retires plain), and claims_supported
// carries the registered claims beside the scope-gated ones — at_hash
// absent on purpose, the code flow's ID token does not mint it.
func TestTheDiscoveryDocumentPinsThePKCEAndClaimSurface(t *testing.T) {
	rp := &goidc.Client{
		ID:            "rp",
		RedirectURIs:  []string{"https://rp.example/cb"},
		GrantTypes:    []goidc.GrantType{goidc.GrantAuthorizationCode},
		ResponseTypes: []goidc.ResponseType{goidc.ResponseTypeCode},
		ScopeIDs:      "openid",
	}
	p, err := provider.New(provider.Config{
		Issuer: "https://saka.example",
		JWKS: func(context.Context) (goidc.JSONWebKeySet, error) {
			return goidc.JSONWebKeySet{}, nil
		},
		IDTokenAlgs: []goidc.SignatureAlgorithm{goidc.SigAlgES256},
	},
		provider.WithPathPrefix(protocolPrefix),
		provider.WithStaticClients(rp),
		provider.WithScopes(protocolScopes()...),
		provider.WithClaims(claimsSupported()...),
		provider.WithAuthCodeGrant(provider.AuthCodeGrantConfig{
			ResponseTypes: []goidc.ResponseType{goidc.ResponseTypeCode},
		},
			provider.WithPKCE([]goidc.CodeChallengeMethod{goidc.CodeChallengeMethodSHA256}),
		),
	)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var document struct {
		PKCEMethods []string `json:"code_challenge_methods_supported"`
		Claims      []string `json:"claims_supported"`
		Scopes      []string `json:"scopes_supported"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &document))
	assert.Equal(t, []string{"S256"}, document.PKCEMethods,
		"the provider offers the S256 challenge alone")
	assert.NotContains(t, document.PKCEMethods, "plain")
	assert.Contains(t, document.Scopes, "openid")

	assert.Contains(t, document.Claims, "sub")
	assert.Contains(t, document.Claims, "iss")
	assert.Contains(t, document.Claims, "email")
	assert.NotContains(t, document.Claims, "at_hash")
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
		Issuer               string   `json:"issuer"`
		JWKSURI              string   `json:"jwks_uri"`
		RegistrationEndpoint string   `json:"registration_endpoint"`
		RevocationEndpoint   string   `json:"revocation_endpoint"`
		RevocationAuthn      []string `json:"revocation_endpoint_auth_methods_supported"`
		IntrospectEndpoint   string   `json:"introspection_endpoint"`
		Scopes               []string `json:"scopes_supported"`
	}
	require.NoError(t, json.Unmarshal(aliasBody, &document))
	assert.Equal(t, "https://saka.example", document.Issuer)
	assert.Equal(t, "https://saka.example"+protocolPrefix+protocolRevokeEndpoint,
		document.RevocationEndpoint)
	assert.NotEmpty(t, document.RevocationAuthn, "the revocation surface names its client authentication methods")
	assert.Equal(t, "https://saka.example"+protocolPrefix+protocolIntrospectEndpoint,
		document.IntrospectEndpoint)
	assert.NotEmpty(t, document.Scopes)

	// The jwks_uri and the registration endpoint the document advertises
	// must both answer: a relying party that fetches the metadata holds
	// the document's word, and a 404 where keys should be is a broken
	// discovery contract. The advertised URLs name the issuer's host; the
	// probe re-points them at the test server.
	for _, advertised := range []string{document.JWKSURI, document.RegistrationEndpoint} {
		answer, fetchErr := server.Client().Get(strings.Replace(advertised, document.Issuer, server.URL, 1))
		require.NoError(t, fetchErr)
		defer answer.Body.Close()
		assert.NotEqual(t, http.StatusNotFound, answer.StatusCode,
			"the advertised endpoint %s must not answer 404", advertised)
	}

	// The registration endpoint is mounted but closed: the write refuses,
	// and the refusal is the RFC 7591 error shape, not a bare 404 or a 500.
	registration, err := server.Client().Post(strings.Replace(document.RegistrationEndpoint, document.Issuer, server.URL, 1),
		"application/json", strings.NewReader(`{"redirect_uris":["https://rp.example/cb"]}`))
	require.NoError(t, err)
	defer registration.Body.Close()
	assert.Equal(t, http.StatusBadRequest, registration.StatusCode)
	registrationBody, err := io.ReadAll(registration.Body)
	require.NoError(t, err)
	var refusal struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(registrationBody, &refusal))
	assert.Equal(t, "invalid_client_metadata", refusal.Error)
}
