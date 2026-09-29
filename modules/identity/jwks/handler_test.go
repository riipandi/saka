package jwks

import (
	"context"
	stdhash "crypto"
	_ "crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/jwtutils"
)

// jwtutilsKeyProvider names the interface the signer and verifier read keys
// through, so the assertion below fails if the service stops satisfying it.
type jwtutilsKeyProvider = jwtutils.KeyProvider

// base64Decode reads the raw, unpadded base64 the key generator writes.
func base64Decode(encoded string) ([]byte, error) {
	return base64.RawStdEncoding.DecodeString(encoded)
}

// testConfig returns a configuration whose key pair is freshly generated, so
// no test depends on a checked-in key.
func testConfig(t *testing.T) config.Config {
	t.Helper()

	generator, err := crypto.NewKeyGenerator(crypto.DefaultSignatureAlgorithm)
	require.NoError(t, err)
	keys, err := generator.Generate()
	require.NoError(t, err)

	cfg := config.Default()
	cfg.Auth.PublicKey = keys[crypto.EnvAuthPublicKey]
	cfg.Auth.PrivateKey = keys[crypto.EnvAuthPrivateKey]
	return cfg
}

// stubSource is a Source whose answer the test controls.
type stubSource struct {
	keys []StoredKey
	err  error
}

func (s stubSource) ActiveSigningKeys(context.Context) ([]StoredKey, error) {
	return s.keys, s.err
}

// serve runs one request through the module's own router.
func serve(t *testing.T, service *Service) *httptest.ResponseRecorder {
	t.Helper()

	router := chi.NewRouter()
	NewModule(service).Mount(router)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))
	return rec
}

// decodeKeys reads the `keys` array of a response.
func decodeKeys(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()

	var body struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Keys
}

// storedPublicKey renders a stored row from a generated key pair.
func storedPublicKey(t *testing.T, algorithm, kid string) StoredKey {
	t.Helper()

	generator, err := crypto.NewKeyGenerator(algorithm)
	require.NoError(t, err)
	keys, err := generator.Generate()
	require.NoError(t, err)

	raw, err := base64Decode(keys[crypto.EnvAuthPublicKey])
	require.NoError(t, err)

	key, err := jwk.ParseKey(raw)
	require.NoError(t, err)
	require.NoError(t, key.Set(jwk.KeyIDKey, kid))

	encoded, err := json.Marshal(key)
	require.NoError(t, err)

	return StoredKey{
		KeyID:     kid,
		Algorithm: algorithm,
		PublicKey: encoded,
	}
}

func TestEndpointPublishesTheConfiguredKey(t *testing.T) {
	service := NewService(testConfig(t), nil, nil, nil)
	require.NoError(t, service.Err())

	rec := serve(t, service)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, rec.Header().Get("Cache-Control"), "max-age")

	keys := decodeKeys(t, rec)
	require.Len(t, keys, 1)
	assert.Equal(t, "EC", keys[0]["kty"])
	assert.Equal(t, "ES256", keys[0]["alg"])
	assert.Equal(t, KeyUsageSignature, keys[0]["use"])
	assert.NotEmpty(t, keys[0]["kid"])
}

// TestEndpointNeverPublishesPrivateMaterial is the security rule of this
// module: the published document is what an unauthenticated client reads, so
// a private field in it would be a key disclosure.
func TestEndpointNeverPublishesPrivateMaterial(t *testing.T) {
	service := NewService(testConfig(t), nil, nil, nil)
	rec := serve(t, service)

	for _, key := range decodeKeys(t, rec) {
		for _, private := range []string{"d", "p", "q", "dp", "dq", "qi", "k"} {
			assert.NotContains(t, key, private,
				"the published key must carry no private field")
		}
	}
}

func TestEndpointMergesTheStoredKeys(t *testing.T) {
	source := stubSource{keys: []StoredKey{storedPublicKey(t, "ES384", "stored-key-1")}}
	service := NewService(testConfig(t), source, nil, nil)

	keys := decodeKeys(t, serve(t, service))

	require.Len(t, keys, 2, "the configured key and the stored one")
	// The configured key is added first, so the stored one follows it.
	assert.Equal(t, "stored-key-1", keys[1]["kid"])
	assert.Equal(t, "ES384", keys[1]["alg"])
	assert.Equal(t, KeyUsageSignature, keys[1]["use"])
}

// TestConfiguredKeyWinsADuplicateKid pins the precedence: the configured key
// is the default, so a stored row carrying its id must not replace it and
// must not appear beside it.
func TestConfiguredKeyWinsADuplicateKid(t *testing.T) {
	cfg := testConfig(t)
	service := NewService(cfg, nil, nil, nil)
	require.NoError(t, service.Err())

	configured, err := service.SignKey(context.Background())
	require.NoError(t, err)
	kid, ok := configured.KeyID()
	require.True(t, ok)

	duplicate := storedPublicKey(t, "ES256", kid)
	merged := NewService(cfg, stubSource{keys: []StoredKey{duplicate}}, nil, nil)

	keys := decodeKeys(t, serve(t, merged))
	assert.Len(t, keys, 1, "one key named twice is a set a client cannot index")
}

// TestSourceFailureStillServesTheConfiguredKey pins the degradation: a
// provider table that cannot be read must not take the application's own
// verification down.
func TestSourceFailureStillServesTheConfiguredKey(t *testing.T) {
	source := stubSource{err: errors.New("connection refused")}
	service := NewService(testConfig(t), source, nil, nil)

	rec := serve(t, service)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, decodeKeys(t, rec), 1)
}

// TestUnusableStoredKeyIsSkipped pins that one bad row does not cost the
// whole set.
func TestUnusableStoredKeyIsSkipped(t *testing.T) {
	source := stubSource{keys: []StoredKey{
		{KeyID: "broken", Algorithm: "ES256", PublicKey: []byte("not a jwk")},
		storedPublicKey(t, "ES256", "good"),
	}}
	service := NewService(testConfig(t), source, nil, nil)

	keys := decodeKeys(t, serve(t, service))

	require.Len(t, keys, 2, "the configured key and the usable stored one")
	assert.Equal(t, "good", keys[1]["kid"])
}

func TestUnreadableConfiguredKeyFailsTheRun(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.PublicKey = "not base64!"

	service := NewService(cfg, nil, nil, nil)

	require.Error(t, service.Err(), "a key that cannot be read must fail before serving")
}

// TestHMACOnlyConfigurationHasNoKeySet covers the deployment that signs with
// the HMAC secret alone: there is no key pair to publish, and that is not an
// error.
func TestHMACOnlyConfigurationHasNoKeySet(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.PublicKey = ""
	cfg.Auth.PrivateKey = ""

	service := NewService(cfg, nil, nil, nil)

	require.NoError(t, service.Err())
	rec := serve(t, service)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, decodeKeys(t, rec))
}

func TestSignKeyRefusesWithoutAKeyPair(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.PublicKey = ""
	cfg.Auth.PrivateKey = ""

	service := NewService(cfg, nil, nil, nil)
	_, err := service.SignKey(context.Background())

	assert.ErrorIs(t, err, ErrNoSigningKey)
}

// TestServiceSatisfiesTheKeyProvider pins the wiring contract the signer and
// the verifier read through.
func TestServiceSatisfiesTheKeyProvider(t *testing.T) {
	var _ jwtutilsKeyProvider = (*Service)(nil)
}

// countingSource counts how many times the source was read, so the cache in
// front of it can be observed.
type countingSource struct {
	calls int
	keys  []StoredKey
	err   error
}

func (s *countingSource) ActiveSigningKeys(context.Context) ([]StoredKey, error) {
	s.calls++
	return s.keys, s.err
}

// TestTheCachedProviderReadsTheSourceOncePerTTL pins the reason the cache is
// wired at all: a client that verifies many tokens must not turn each
// verification into a query.
func TestTheCachedProviderReadsTheSourceOncePerTTL(t *testing.T) {
	source := &countingSource{}
	service := NewService(testConfig(t), source, nil, nil)
	cached := jwtutils.NewCachedKeyProvider(service, time.Hour)

	router := chi.NewRouter()
	NewModule(cached).Mount(router)

	for range 5 {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	assert.Equal(t, 1, source.calls,
		"five fetches inside the TTL must cost one read of the source")
}

// TestAMountedModuleWithoutAProviderFailsClosed covers the wiring defect: an
// endpoint with no key provider must never answer an empty set, which a
// client would read as "no key is valid".
func TestAMountedModuleWithoutAProviderFailsClosed(t *testing.T) {
	router := chi.NewRouter()
	NewModule(nil).Mount(router)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"keys"`)
}

// TestASymmetricStoredKeyIsNeverPublished is the disclosure rule: an HMAC
// key's "public" form is the secret itself, so a row carrying one must be
// refused rather than served to every client.
func TestASymmetricStoredKeyIsNeverPublished(t *testing.T) {
	secret := []byte("a-32-byte-hmac-secret-goes-here")
	oct, err := jwk.Import(secret)
	require.NoError(t, err)
	require.NoError(t, oct.Set(jwk.KeyIDKey, "hmac-key"))
	encoded, err := json.Marshal(oct)
	require.NoError(t, err)

	source := stubSource{keys: []StoredKey{{
		KeyID:     "hmac-key",
		Algorithm: "HS256",
		PublicKey: encoded,
	}}}
	service := NewService(testConfig(t), source, nil, nil)

	rec := serve(t, service)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "hmac-key",
		"a symmetric key must not reach the published document")
	// The configured key is still published: one refused row must not empty
	// the set.
	assert.Len(t, decodeKeys(t, rec), 1)
}

// TestASymmetricConfiguredKeyFailsTheRun covers the other path into the set:
// an auth.public_key that holds a shared secret is a broken deployment, and it
// must fail before the listener opens rather than publish the secret.
func TestASymmetricConfiguredKeyFailsTheRun(t *testing.T) {
	secret := []byte("a-32-byte-hmac-secret-goes-here")
	oct, err := jwk.Import(secret)
	require.NoError(t, err)
	encoded, err := json.Marshal(oct)
	require.NoError(t, err)

	cfg := testConfig(t)
	cfg.Auth.PublicKey = base64.RawStdEncoding.EncodeToString(encoded)

	service := NewService(cfg, nil, nil, nil)

	require.ErrorIs(t, service.Err(), ErrSymmetricKey)
}

// TestModuleNameIsReported keeps the composition report readable.
func TestModuleNameIsReported(t *testing.T) {
	assert.Equal(t, "jwks", NewModule(nil).Name())
}

// TestALegacyConfiguredKeyIsRenamedToAJwkKid pins the fallback: a configured
// key pair generated before the `jwk_` convention carries the old
// thumbprint kid, and the parse renames it so the published set is uniform.
// The private half adopts the new kid, so a token's header still names a
// key the set carries.
func TestALegacyConfiguredKeyIsRenamedToAJwkKid(t *testing.T) {
	cfg := testConfig(t)

	// Roll the pair back to the thumbprint kid the old generator wrote.
	public, err := crypto.DecodeJWK(cfg.Auth.PublicKey)
	require.NoError(t, err)
	thumbprint, thumbErr := public.Thumbprint(stdhash.SHA256)
	require.NoError(t, thumbErr)
	legacy := base64.RawURLEncoding.EncodeToString(thumbprint)
	require.NoError(t, public.Set(jwk.KeyIDKey, legacy))
	legacyPublic, err := json.Marshal(public)
	require.NoError(t, err)
	private, err := crypto.DecodeJWK(cfg.Auth.PrivateKey)
	require.NoError(t, err)
	private.Set(jwk.KeyIDKey, legacy)
	legacyPrivate, err := json.Marshal(private)
	require.NoError(t, err)
	encodedPublic := base64.RawStdEncoding.EncodeToString(legacyPublic)
	encodedPrivate := base64.RawStdEncoding.EncodeToString(legacyPrivate)
	cfg.Auth.PublicKey = encodedPublic
	cfg.Auth.PrivateKey = encodedPrivate

	service := NewService(cfg, nil, nil, nil)
	require.NoError(t, service.Err(), "the rename is not a parse failure")

	keys := decodeKeys(t, serve(t, service))
	require.Len(t, keys, 1)
	kid, ok := keys[0]["kid"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(kid, "jwk_"),
		"the published kid %q must carry the jwk_ prefix", kid)
	assert.NotEqual(t, legacy, kid, "the thumbprint kid must not survive")

	// The signing key answers with the renamed kid, so a token minted now
	// names a key the published set carries.
	signKey, err := service.SignKey(context.Background())
	require.NoError(t, err)
	signedKid, ok := signKey.KeyID()
	require.True(t, ok)
	assert.Equal(t, kid, signedKid, "both halves must answer the renamed kid")

	// A current pair (the generator stamps jwk_ itself) keeps its kid: the
	// fallback only rewrites what predates the convention.
	current := testConfig(t)
	currentService := NewService(current, nil, nil, nil)
	require.NoError(t, currentService.Err())
	currentPublic, err := crypto.DecodeJWK(current.Auth.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, mustGeneratedKid(t, currentPublic), mustServicePublishedKid(t, currentService))
}

func mustGeneratedKid(t *testing.T, key jwk.Key) string {
	t.Helper()
	kid, ok := key.KeyID()
	require.True(t, ok)
	return kid
}

func mustServicePublishedKid(t *testing.T, service *Service) string {
	t.Helper()
	keys := decodeKeys(t, serve(t, service))
	require.Len(t, keys, 1)
	kid, ok := keys[0]["kid"].(string)
	require.True(t, ok)
	return kid
}
