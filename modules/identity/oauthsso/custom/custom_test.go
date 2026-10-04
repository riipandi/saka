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

	"github.com/riipandi/saka/modules/identity/oauthsso"
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

// resolveAgainstUserinfo spins one fake provider whose userinfo document
// is the given JSON — the id_token names only the subject, so the
// userinfo is the claim source every mapping reads.
func resolveAgainstUserinfo(t *testing.T, userinfo string, mapping oauthsso.AttributeMapping) (oauthsso.ExternalIdentity, error) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			now := time.Now()
			claims, _ := json.Marshal(map[string]any{
				"iss": server.URL, "aud": "hogwarts-client-id", "sub": "token-subject",
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

	fetcher := oauthsso.IdentityFetcherFunc(func(_ context.Context, rawurl, _ string) (int, []byte, error) {
		return http.StatusOK, []byte(userinfo), nil
	})

	return New(fetcher).Resolve(t.Context(), oauthsso.Connection{
		ClientID:         "hogwarts-client-id",
		ClientSecret:     "hogwarts-client-secret",
		AttributeMapping: mapping,
		Endpoints: oauthsso.Endpoints{
			Issuer: server.URL, Token: server.URL + "/token",
			Jwks: server.URL + "/jwks", Userinfo: server.URL + "/userinfo",
		},
	}, oauthsso.FlowSecrets{RedirectURI: "https://app.hogwarts.example/callback"}, "code")
}

func TestResolveReadsTheExtendedMappedClaims(t *testing.T) {
	identity, err := resolveAgainstUserinfo(t,
		`{"uid":"hufflepuff-7","mail_verified":true,"handle":"luna","portrait":"https://cdn.hogwarts.example/luna.png",
		  "mail":"luna@hogwarts.example","first":"Luna","last":"Lovegood"}`,
		oauthsso.AttributeMapping{
			Email: "mail", Subject: "uid", EmailVerified: "mail_verified",
			Username: "handle", AvatarURL: "portrait",
			GivenName: "first", FamilyName: "last",
		})
	require.NoError(t, err)
	assert.Equal(t, "hufflepuff-7", identity.ProviderAccountID, "the mapped subject is the identity")
	assert.True(t, identity.EmailVerified, "the mapped verified claim is read")
	assert.Equal(t, "luna", identity.Username)
	assert.Equal(t, "https://cdn.hogwarts.example/luna.png", identity.AvatarURL)
}

func TestResolveAnswersTheMappingDefaultsOnAbsentClaims(t *testing.T) {
	// A silent mapping keeps the standard claims: the subject binds, an
	// absent verified flag falls to its default, and no username or
	// picture claim names either.
	identity, err := resolveAgainstUserinfo(t,
		`{"sub":"token-subject","email":"cho@hogwarts.example"}`,
		oauthsso.AttributeMapping{})
	require.NoError(t, err)
	assert.Equal(t, "token-subject", identity.ProviderAccountID)
	assert.False(t, identity.EmailVerified)
	assert.Empty(t, identity.Username)
	assert.Empty(t, identity.AvatarURL)

	// The default verified flag is the operator's word, answered only
	// when the provider names no verified claim.
	identity, err = resolveAgainstUserinfo(t,
		`{"sub":"token-subject","email":"cho@hogwarts.example"}`,
		oauthsso.AttributeMapping{EmailVerifiedDefault: true})
	require.NoError(t, err)
	assert.True(t, identity.EmailVerified)

	// A mapped verified claim the provider does not answer falls to the
	// default too.
	identity, err = resolveAgainstUserinfo(t,
		`{"sub":"token-subject","email":"cho@hogwarts.example"}`,
		oauthsso.AttributeMapping{EmailVerified: "mail_verified", EmailVerifiedDefault: true})
	require.NoError(t, err)
	assert.True(t, identity.EmailVerified)
}

func TestResolveBindsOnTheMappedSubjectAlone(t *testing.T) {
	// A provider whose identity claim is not `sub`: the mapped claim is
	// the identity the binding judges.
	identity, err := resolveAgainstUserinfo(t,
		`{"uuid":"prov-77","email":"cho@hogwarts.example"}`,
		oauthsso.AttributeMapping{Subject: "uuid"})
	require.NoError(t, err)
	assert.Equal(t, "prov-77", identity.ProviderAccountID)

	// A mapped subject the provider answers nothing for names no
	// bindable identity — the refusal, not a silent fallback.
	_, err = resolveAgainstUserinfo(t,
		`{"email":"cho@hogwarts.example"}`,
		oauthsso.AttributeMapping{Subject: "uuid"})
	assert.ErrorIs(t, err, oauthsso.ErrIdentityInvalid)

	// The same strictness for an explicitly mapped address: the
	// standard claim is not read behind the mapping's back.
	_, err = resolveAgainstUserinfo(t,
		`{"sub":"token-subject","email":"cho@hogwarts.example"}`,
		oauthsso.AttributeMapping{Email: "mail"})
	assert.ErrorIs(t, err, oauthsso.ErrIdentityInvalid)
}

func TestResolveKeepsArrayClaimsInTheRawDocument(t *testing.T) {
	identity, err := resolveAgainstUserinfo(t,
		`{"sub":"token-subject","email":"cho@hogwarts.example","departments":["charms","potions"]}`,
		oauthsso.AttributeMapping{})
	require.NoError(t, err)

	// The raw document rides along as it arrived — an array claim is
	// kept, the source a custom attribute reads later.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(identity.Profile, &raw))
	assert.Equal(t, []any{"charms", "potions"}, raw["departments"])
}
