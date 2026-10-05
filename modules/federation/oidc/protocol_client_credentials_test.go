package oidc

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/luikyv/go-oidc/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The client-credentials surface: the grant is opt-in per client through
// its allowed list, the token names the client itself, and a client the
// list does not admit is refused.

// testProviderSecret is the 32-byte HMAC secret the test provider signs
// with — the JWKS func publishes it with its private material, the way
// the real one does for the provider to sign from.
const testProviderSecret = "gringotts-vault-32-bytes-of-secret!!"

// clientCredentialsProvider assembles the protocol slice the grant tests
// need, the way NewProtocol wires it: real stores, secret-post client
// authentication, and the client-credentials grant enabled.
func clientCredentialsProvider(t *testing.T, pool *revokePool) http.Handler {
	t.Helper()

	keys := jwk.NewSet()
	symmetric, err := jwk.Import([]byte(testProviderSecret))
	require.NoError(t, err)
	require.NoError(t, symmetric.Set(jwk.KeyIDKey, "test-signing-key"))
	require.NoError(t, symmetric.Set(jwk.KeyTypeKey, "oct"))
	require.NoError(t, symmetric.Set(jwk.AlgorithmKey, "HS256"))
	require.NoError(t, keys.AddKey(symmetric))

	grants := grantStore{protocolStore: protocolStore{pool: pool.Postgres}}
	clients := clientStore{
		pool:    pool.Postgres,
		repo:    NewRepository(),
		baseURL: "https://saka.example",
	}
	p, err := provider.New(provider.Config{
		Issuer: "https://saka.example",
		JWKS: func(context.Context) (goidc.JSONWebKeySet, error) {
			document, docErr := json.Marshal(keys)
			require.NoError(t, docErr)
			var set goidc.JSONWebKeySet
			require.NoError(t, json.Unmarshal(document, &set))
			return set, nil
		},
		IDTokenAlgs: []goidc.SignatureAlgorithm{goidc.SigAlgHS256},
		Manager:     grants,
	},
		provider.WithPathPrefix(protocolPrefix),
		provider.WithDCR(clients),
		provider.WithClientCredentialsGrant(),
		provider.WithTokenOptions(func(_ context.Context, grant *goidc.Grant, _ *goidc.Client) goidc.TokenOptions {
			lifetime := defaultTokenLifetimeSecs
			if grant.Subject == grant.ClientID && grant.Subject != "" {
				lifetime = clientCredentialsLifetimeSecs
			}
			return goidc.NewJWTTokenOptions(goidc.SigAlgHS256, lifetime)
		}),
		provider.WithClientSecretVerifier(clientSecretVerifier),
		provider.WithSecretPostAuthn(),
	)
	require.NoError(t, err)
	return p.Handler()
}

// accessTokenClaims parses the compact token the provider minted, with
// the key the test provider signs from.
func accessTokenClaims(t *testing.T, token string) jwt.Token {
	t.Helper()
	parsed, err := jwt.ParseString(token, jwt.WithKey(jwa.HS256(), []byte(testProviderSecret)))
	require.NoError(t, err)
	return parsed
}

func TestClientCredentialsMintsATokenForTheAllowedClient(t *testing.T) {
	pool := newRevokePool(t)
	owner := seedAccount(t, pool.Postgres, "sophie")
	issued, err := pool.Service.Create(t.Context(), owner, CreateParams{
		Name:              "Pipeline Portal",
		CallbackURLs:      []string{"https://pipeline.example/callback"},
		AllowedGrantWires: []string{GrantClientCredentialsWire},
	})
	require.NoError(t, err)

	server := httptest.NewServer(clientCredentialsProvider(t, pool))
	t.Cleanup(server.Close)

	response, err := http.PostForm(server.URL+protocolPrefix+protocolTokenEndpoint, url.Values{
		"client_id":     {issued.Client.ID},
		"client_secret": {issued.Secret},
		"grant_type":    {GrantClientCredentialsWire},
	})
	require.NoError(t, err)
	defer response.Body.Close()
	body := readAll(t, response)
	require.Equal(t, http.StatusOK, response.StatusCode, body)

	var granted struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &granted))
	assert.Equal(t, "Bearer", granted.TokenType)
	assert.Equal(t, clientCredentialsLifetimeSecs, granted.ExpiresIn,
		"the machine token lives the spelled hour, not the user grant's window")

	// The subject is the client itself — no account stands behind it.
	parsed := accessTokenClaims(t, granted.AccessToken)
	sub, _ := parsed.Subject()
	assert.Equal(t, issued.Client.ID, sub)
}

func TestClientCredentialsRefusesAnUnlistedClient(t *testing.T) {
	pool := newRevokePool(t)
	owner := seedAccount(t, pool.Postgres, "langdon")
	// The default list — no client-credentials word.
	issued, err := pool.Service.Create(t.Context(), owner, createParams("Cloister Portal"))
	require.NoError(t, err)

	server := httptest.NewServer(clientCredentialsProvider(t, pool))
	t.Cleanup(server.Close)

	response, err := http.PostForm(server.URL+protocolPrefix+protocolTokenEndpoint, url.Values{
		"client_id":     {issued.Client.ID},
		"client_secret": {issued.Secret},
		"grant_type":    {GrantClientCredentialsWire},
	})
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode,
		"the library maps unauthorized_client to 401")

	body := readAll(t, response)
	assert.Contains(t, body, "unauthorized_client",
		"the client whose list does not name the grant is refused, not the credential")
}

func TestClientCredentialsRejectsAnUnknownGrantWord(t *testing.T) {
	pool := newRevokePool(t)
	owner := seedAccount(t, pool.Postgres, "vetra")

	_, err := pool.Service.Create(t.Context(), owner, CreateParams{
		Name:              "Lyon Portal",
		CallbackURLs:      []string{"https://lyon.example/callback"},
		AllowedGrantWires: []string{"password"},
	})
	require.ErrorIs(t, err, ErrUnknownGrantType)

	created, err := pool.Service.Create(t.Context(), owner, CreateParams{
		Name:              "Lyon Portal",
		CallbackURLs:      []string{"https://lyon.example/callback"},
		AllowedGrantWires: []string{GrantClientCredentialsWire, GrantRefreshTokenWire},
	})
	require.NoError(t, err)
	_, err = pool.Service.Update(t.Context(), created.Client.ID, UpdateParams{
		Name:              "Lyon Portal",
		CallbackURLs:      []string{"https://lyon.example/callback"},
		AllowedGrantWires: []string{"implicit"},
	})
	require.ErrorIs(t, err, ErrUnknownGrantType, "an update is judged like a create")
}
