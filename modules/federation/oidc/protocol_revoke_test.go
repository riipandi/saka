package oidc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/luikyv/go-oidc/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/datastore"
)

// The RFC 7009 revocation surface, driven through a provider assembled
// with saka's real stores: a client revokes its own refresh token, a
// stranger cannot, an unknown token is a quiet success.

// revokePool pairs the migrated pool with the service its client rows
// come from, so the helpers reach both.
type revokePool struct {
	*datastore.Postgres
	Service *Service
}

func newRevokePool(t *testing.T) *revokePool {
	t.Helper()
	pool := migratedPool(t)
	return &revokePool{Postgres: pool, Service: testService(t, pool)}
}

// readAll answers the response body as text, for assertions that name the
// error code the endpoint answered with.
func readAll(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return string(body)
}

// revokeProvider mounts the protocol slice the revocation test needs: the
// client and grant stores over the migrated pool, secret-post client
// authentication, and the revocation options NewProtocol wires.
func revokeProvider(t *testing.T, pool *revokePool) http.Handler {
	t.Helper()

	grants := grantStore{protocolStore: protocolStore{pool: pool.Postgres}}
	clients := clientStore{
		pool:    pool.Postgres,
		repo:    NewRepository(),
		baseURL: "https://saka.example",
	}
	p, err := provider.New(provider.Config{
		Issuer: "https://saka.example",
		JWKS: func(context.Context) (goidc.JSONWebKeySet, error) {
			return goidc.JSONWebKeySet{}, nil
		},
		// The revocation surface signs nothing, but the provider demands
		// an ID-token algorithm before it builds at all.
		IDTokenAlgs: []goidc.SignatureAlgorithm{goidc.SigAlgES256},
		Manager:     grants,
	},
		provider.WithPathPrefix(protocolPrefix),
		provider.WithDCR(clients),
		provider.WithRefreshTokenGrant(grants),
		provider.WithClientSecretVerifier(clientSecretVerifier),
		provider.WithSecretPostAuthn(),
		provider.WithTokenRevocation(revocationPolicy(),
			provider.WithTokenRevocationRevokeGrantOnAccessToken(),
			provider.WithTokenRevocationEndpoint(protocolRevokeEndpoint)),
	)
	require.NoError(t, err)
	return p.Handler()
}

// seedRevokableGrant writes a grant whose refresh token a revocation can
// reach, the pointer row included.
func seedRevokableGrant(t *testing.T, pool *revokePool, clientID, refreshToken string) {
	t.Helper()

	grants := grantStore{protocolStore: protocolStore{pool: pool.Postgres}}
	require.NoError(t, grants.SaveGrant(t.Context(), &goidc.Grant{
		ID:                    "g-" + refreshToken,
		ClientID:              clientID,
		Subject:               "sophie",
		Username:              "sophie",
		Scopes:                "openid",
		CreatedAt:             int(time.Now().Unix()),
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: int(time.Now().Add(time.Hour).Unix()),
	}))
}

func TestTheRevocationEndpointKillsTheGrant(t *testing.T) {
	pool := newRevokePool(t)
	owner := seedAccount(t, pool.Postgres, "sophie")
	issued, err := pool.Service.Create(t.Context(), owner, createParams("Rennes Portal"))
	require.NoError(t, err)
	seedRevokableGrant(t, pool, issued.Client.ID, "rt-cluedo")

	server := httptest.NewServer(revokeProvider(t, pool))
	t.Cleanup(server.Close)

	response, err := http.PostForm(server.URL+protocolPrefix+protocolRevokeEndpoint, url.Values{
		"client_id":     {issued.Client.ID},
		"client_secret": {issued.Secret},
		"token":         {"rt-cluedo"},
	})
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode,
		"RFC 7009 answers a successful revocation with an empty 200")

	// The grant is marked revoked, and the refresh token's next
	// redemption meets the library's invalid-grant refusal.
	grants := grantStore{protocolStore: protocolStore{pool: pool.Postgres}}
	grant, err := grants.GrantByRefreshToken(t.Context(), "rt-cluedo")
	require.NoError(t, err)
	assert.NotZero(t, grant.RevokedAt, "the grant carries the revocation mark")

	redemption, err := http.PostForm(server.URL+protocolPrefix+protocolTokenEndpoint, url.Values{
		"client_id":     {issued.Client.ID},
		"client_secret": {issued.Secret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {"rt-cluedo"},
	})
	require.NoError(t, err)
	defer redemption.Body.Close()
	assert.Equal(t, http.StatusBadRequest, redemption.StatusCode,
		"the revoked refresh token redeems no token")
}

func TestTheRevocationEndpointRefusesAStrangersToken(t *testing.T) {
	pool := newRevokePool(t)
	owner := seedAccount(t, pool.Postgres, "langdon")
	issued, err := pool.Service.Create(t.Context(), owner, createParams("Neufchateau Portal"))
	require.NoError(t, err)
	seedRevokableGrant(t, pool, issued.Client.ID, "rt-abarthel")

	stranger := seedAccount(t, pool.Postgres, "chretien")
	strangerClient, err := pool.Service.Create(t.Context(), stranger, createParams("Louvre Portal"))
	require.NoError(t, err)

	server := httptest.NewServer(revokeProvider(t, pool))
	t.Cleanup(server.Close)

	response, err := http.PostForm(server.URL+protocolPrefix+protocolRevokeEndpoint, url.Values{
		"client_id":     {strangerClient.Client.ID},
		"client_secret": {strangerClient.Secret},
		"token":         {"rt-abarthel"},
	})
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusForbidden, response.StatusCode,
		"a token minted to another client is refused")

	body := readAll(t, response)
	assert.True(t, strings.Contains(body, "access_denied"),
		"the refusal names the access-denied error, got %q", body)

	grants := grantStore{protocolStore: protocolStore{pool: pool.Postgres}}
	grant, err := grants.GrantByRefreshToken(t.Context(), "rt-abarthel")
	require.NoError(t, err)
	assert.Zero(t, grant.RevokedAt, "the stranger's refusal revokes nothing")
}

func TestTheRevocationEndpointAnswersAnUnknownTokenQuietly(t *testing.T) {
	pool := newRevokePool(t)
	owner := seedAccount(t, pool.Postgres, "vetra")
	issued, err := pool.Service.Create(t.Context(), owner, createParams("Sistene Portal"))
	require.NoError(t, err)

	server := httptest.NewServer(revokeProvider(t, pool))
	t.Cleanup(server.Close)

	response, err := http.PostForm(server.URL+protocolPrefix+protocolRevokeEndpoint, url.Values{
		"client_id":     {issued.Client.ID},
		"client_secret": {issued.Secret},
		"token":         {"rt-nonexistent"},
	})
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode,
		"RFC 7009 treats an unknown token as already gone")
}
