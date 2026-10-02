package custom

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/modules/identity/oauthsso"
)

func TestAuthorizeURLCarriesThePKCEChallengeAndTheNonce(t *testing.T) {
	adapter := New(nil)
	conn := oauthsso.Connection{
		ClientID: "hogwarts-client-id",
		Scopes:   []string{"openid", "email"},
		Endpoints: oauthsso.Endpoints{
			Authorization: "https://sso.hogwarts.example/authorize",
			Token:         "https://sso.hogwarts.example/token",
		},
	}
	flow := oauthsso.FlowSecrets{
		State: "state-plain", Nonce: "nonce-plain", Verifier: "verifier-plain",
		RedirectURI: "https://app.hogwarts.example/api/oauth/hogwarts-sso/callback",
	}

	raw, err := adapter.AuthorizeURL(conn, flow)
	require.NoError(t, err)
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	assert.Equal(t, "sso.hogwarts.example", parsed.Host)
	assert.Equal(t, "nonce-plain", parsed.Query().Get("nonce"))
	assert.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	assert.Equal(t, pkceS256("verifier-plain"), parsed.Query().Get("code_challenge"))
}

func TestResolveReadsTheMappedClaimsFromTheUserinfoDocument(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			now := time.Now()
			claims, _ := json.Marshal(map[string]any{
				"iss": server.URL, "aud": "hogwarts-client-id", "sub": "gryffindor-subject",
				"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
			})
			header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test-key"})
			signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
			sum := sha256.Sum256([]byte(signing))
			sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "access-from-provider",
				"token_type":   "Bearer",
				"id_token":     signing + "." + base64.RawURLEncoding.EncodeToString(sig),
			})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
				"kty": "RSA", "kid": "test-key", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	fetcher := oauthsso.IdentityFetcherFunc(func(_ context.Context, rawurl, bearer string) (int, []byte, error) {
		assert.Equal(t, server.URL+"/userinfo", rawurl)
		assert.Equal(t, "access-from-provider", bearer)
		return http.StatusOK, []byte(`{"sub":"gryffindor-subject","mail":"hermione@hogwarts.example","email_verified":"true","first":"Hermione","last":"Granger"}`), nil
	})

	identity, err := New(fetcher).Resolve(t.Context(), oauthsso.Connection{
		ClientID:     "hogwarts-client-id",
		ClientSecret: "hogwarts-client-secret",
		Endpoints: oauthsso.Endpoints{
			Issuer: server.URL, Token: server.URL + "/token",
			Jwks: server.URL + "/jwks", Userinfo: server.URL + "/userinfo",
		},
		AttributeMapping: oauthsso.AttributeMapping{Email: "mail", GivenName: "first", FamilyName: "last"},
	}, oauthsso.FlowSecrets{RedirectURI: "https://app.hogwarts.example/callback"}, "code")
	require.NoError(t, err)
	assert.Equal(t, "gryffindor-subject", identity.ProviderAccountID)
	assert.Equal(t, "hermione@hogwarts.example", identity.Email)
	assert.True(t, identity.EmailVerified)
	assert.Equal(t, "Hermione", identity.GivenName)
	assert.Equal(t, "Granger", identity.FamilyName)
}
