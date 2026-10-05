package builtin

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

	"github.com/riipandi/saka/modules/identity/oauthsso"
)

// oidcFake is a token endpoint and a key set the adapter tests drive a
// resolution against. The id_token it mints is signed by the key the
// key set publishes, so the verification is the library's real one.
type oidcFake struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	issuer string
}

func newOIDCFake(t *testing.T) *oidcFake {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	fake := &oidcFake{key: key}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token":  "access-from-provider",
				"refresh_token": "refresh-from-provider",
				"token_type":    "Bearer",
				"id_token":      fake.sign(t, r),
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwkOf(key)}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.server.Close)
	fake.issuer = fake.server.URL
	return fake
}

// sign mints an id_token. The nonce claim is empty unless the token
// request named one — the adapter does not send the nonce at the token
// endpoint, so a flow that minted one refuses the answer.
func (f *oidcFake) sign(t *testing.T, r *http.Request) string {
	t.Helper()
	_ = r.ParseForm()
	now := time.Now()
	claims := map[string]any{
		"iss":            f.issuer,
		"aud":            "hogwarts-client-id",
		"sub":            "gryffindor-subject",
		"email":          "hermione@hogwarts.example",
		"email_verified": true,
		"given_name":     "Hermione",
		"family_name":    "Granger",
		"nonce":          r.Form.Get("nonce"),
		"iat":            now.Unix(),
		"exp":            now.Add(5 * time.Minute).Unix(),
	}
	return signRS256(t, f.key, claims)
}

func signRS256(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	require.NoError(t, err)
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func jwkOf(key *rsa.PrivateKey) map[string]string {
	return map[string]string{
		"kty": "RSA",
		"kid": "test-key",
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

func testConnection() oauthsso.Connection {
	return oauthsso.Connection{
		Kind:         oauthsso.KindBuiltin,
		Provider:     "google",
		ClientID:     "hogwarts-client-id",
		ClientSecret: "hogwarts-client-secret",
		Scopes:       []string{"openid", "email", "profile"},
	}
}

func TestGoogleAuthorizeURLCarriesThePKCEChallengeAndTheNonce(t *testing.T) {
	adapter := NewGoogle()
	flow := oauthsso.FlowSecrets{
		State:       "state-plain",
		Nonce:       "nonce-plain",
		Verifier:    "verifier-plain",
		RedirectURI: "https://app.hogwarts.example/api/oauth/google/callback",
	}

	raw, err := adapter.AuthorizeURL(testConnection(), flow)
	require.NoError(t, err)
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	assert.Equal(t, "https", parsed.Scheme)
	assert.Equal(t, "accounts.google.com", parsed.Host)
	assert.Equal(t, "state-plain", parsed.Query().Get("state"))
	assert.Equal(t, "nonce-plain", parsed.Query().Get("nonce"))
	assert.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	assert.Equal(t, pkceS256("verifier-plain"), parsed.Query().Get("code_challenge"))
	assert.Equal(t, flow.RedirectURI, parsed.Query().Get("redirect_uri"))
	assert.Contains(t, parsed.Query().Get("scope"), "openid")
}

func TestGoogleResolveVerifiesTheIDToken(t *testing.T) {
	fake := newOIDCFake(t)
	adapter := NewGoogle()
	adapter.issuer = fake.issuer
	adapter.jwksURL = fake.server.URL + "/jwks"
	adapter.def.TokenURL = fake.server.URL + "/token"

	identity, err := adapter.Resolve(t.Context(), testConnection(), oauthsso.FlowSecrets{
		Verifier:    "verifier-plain",
		RedirectURI: "https://app.hogwarts.example/api/oauth/google/callback",
	}, "code-from-provider")
	require.NoError(t, err)
	assert.Equal(t, "gryffindor-subject", identity.ProviderAccountID)
	assert.Equal(t, "hermione@hogwarts.example", identity.Email)
	assert.True(t, identity.EmailVerified)
	assert.Equal(t, "Hermione", identity.GivenName)
	assert.Equal(t, "Granger", identity.FamilyName)
	assert.Equal(t, "access-from-provider", identity.AccessToken)
	assert.Equal(t, "refresh-from-provider", identity.RefreshToken)
}

func TestGoogleResolveRefusesAStaleNonce(t *testing.T) {
	fake := newOIDCFake(t)
	adapter := NewGoogle()
	adapter.issuer = fake.issuer
	adapter.jwksURL = fake.server.URL + "/jwks"
	adapter.def.TokenURL = fake.server.URL + "/token"

	_, err := adapter.Resolve(t.Context(), testConnection(), oauthsso.FlowSecrets{
		Nonce:       "the-nonce-the-flow-minted",
		Verifier:    "verifier-plain",
		RedirectURI: "https://app.hogwarts.example/api/oauth/google/callback",
	}, "code-from-provider")
	require.ErrorIs(t, err, oauthsso.ErrResolutionFailed)
}

func TestGitHubResolveReadsThePrimaryVerifiedEmail(t *testing.T) {
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "access-from-github",
			"token_type":   "Bearer",
		})
	}))
	t.Cleanup(token.Close)

	adapter := NewGitHub(stubFetcher{
		"https://api.github.com/user": {http.StatusOK, []byte(`{"id":7,"login":"hermione","name":"Hermione Granger"}`)},
		"https://api.github.com/user/emails": {http.StatusOK, []byte(
			`[{"email":"public@hogwarts.example","primary":false,"verified":false},` +
				`{"email":"hermione@hogwarts.example","primary":true,"verified":true}]`)},
	})
	adapter.def.TokenURL = token.URL

	identity, err := adapter.Resolve(t.Context(), oauthsso.Connection{
		ClientID:     "github-client-id",
		ClientSecret: "github-client-secret",
	}, oauthsso.FlowSecrets{RedirectURI: "https://app.hogwarts.example/api/oauth/github/callback"}, "code")
	require.NoError(t, err)
	assert.Equal(t, "7", identity.ProviderAccountID)
	assert.Equal(t, "hermione@hogwarts.example", identity.Email)
	assert.True(t, identity.EmailVerified)
	assert.Equal(t, "Hermione", identity.GivenName)
	assert.Equal(t, "Granger", identity.FamilyName)
	assert.Equal(t, "access-from-github", identity.AccessToken)
}

// stubFetcher answers the documents a test named, keyed by URL.
type stubFetcher map[string]stubAnswer

type stubAnswer struct {
	status int
	body   []byte
}

func (s stubFetcher) Do(_ context.Context, rawurl, _ string) (int, []byte, error) {
	answer, ok := s[rawurl]
	if !ok {
		return http.StatusNotFound, nil, nil
	}
	return answer.status, answer.body, nil
}
