package jwks

import (
	"context"
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

// testConfig returns a configuration with a fresh HMAC secret, so no test
// depends on a checked-in value. The signing key pair lives in the database;
// a test that needs one wires pairSource.
func testConfig(t *testing.T) config.Config {
	t.Helper()

	generator, err := crypto.NewKeyGenerator(crypto.DefaultSecretAlgorithm)
	require.NoError(t, err)
	keys, err := generator.Generate()
	require.NoError(t, err)

	cfg := config.Default()
	cfg.Auth.SecretKey = keys[crypto.EnvAuthSecretKey]
	return cfg
}

// pairSource is a PairSource whose answer the test controls: the same rows
// seen through the Source view (public only) and the PairSource view (the
// sealed private half included).
type pairSource struct {
	pairs []SigningKeyPair
	keys  []StoredKey
}

func (s pairSource) ActiveSigningKeys(context.Context) ([]StoredKey, error) {
	return s.keys, nil
}

func (s pairSource) ActiveSigningKeyPairs(context.Context) ([]SigningKeyPair, error) {
	return s.pairs, nil
}

// storedPair generates one signing key pair the tests sign through, returned
// in both source views. The row carries the JWK JSON the table stores — the
// column is the document, not its base64 envelope.
func storedPair(t *testing.T) (SigningKeyPair, StoredKey) {
	t.Helper()

	private, public, err := crypto.GenerateKeyPair(crypto.DefaultSignatureAlgorithm)
	require.NoError(t, err)
	parsed, err := crypto.DecodeJWK(public)
	require.NoError(t, err)
	kid, ok := parsed.KeyID()
	require.True(t, ok)

	document, err := json.Marshal(parsed)
	require.NoError(t, err)
	// The private half is stored sealed; the tests sign through the same
	// unseal path the server runs, so the pair carries the ciphertext. The
	// plaintext is the JWK JSON the table stores, not its base64 envelope.
	privateDoc, err := crypto.DecodeJWK(private)
	require.NoError(t, err)
	privateJSON, err := json.Marshal(privateDoc)
	require.NoError(t, err)
	cipher, err := crypto.NewCipherFromHex(strings.Repeat("ab", 32))
	require.NoError(t, err)
	sealed, err := cipher.Encrypt(string(privateJSON))
	require.NoError(t, err)
	return SigningKeyPair{KeyID: kid, Algorithm: crypto.DefaultSignatureAlgorithm, PrivateKey: []byte(sealed)},
		StoredKey{KeyID: kid, Algorithm: crypto.DefaultSignatureAlgorithm, PublicKey: document}
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

	_, public, err := crypto.GenerateKeyPair(algorithm)
	require.NoError(t, err)

	key, err := crypto.DecodeJWK(public)
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

func TestEndpointPublishesTheStoredKeys(t *testing.T) {
	_, key := storedPair(t)
	service := NewService(testConfig(t), pairSource{keys: []StoredKey{key}}, nil, nil)
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
	_, key := storedPair(t)
	service := NewService(testConfig(t), pairSource{keys: []StoredKey{key}}, nil, nil)
	rec := serve(t, service)

	for _, key := range decodeKeys(t, rec) {
		for _, private := range []string{"d", "p", "q", "dp", "dq", "qi", "k"} {
			assert.NotContains(t, key, private,
				"the published key must carry no private field")
		}
	}
}

func TestEndpointMergesTheStoredKeys(t *testing.T) {
	_, first := storedPair(t)
	_, second := storedPair(t)
	second.KeyID = "stored-key-2"
	second.Algorithm = "ES384"
	source := pairSource{keys: []StoredKey{first, second}}
	service := NewService(testConfig(t), source, nil, nil)

	keys := decodeKeys(t, serve(t, service))

	require.Len(t, keys, 2, "every stored row is published")
	assert.Equal(t, "stored-key-2", keys[1]["kid"])
	assert.Equal(t, "ES384", keys[1]["alg"])
	assert.Equal(t, KeyUsageSignature, keys[1]["use"])
}

// TestADuplicateKidAppearsOnce pins that a set naming one key twice is
// never served: a client cannot index it.
func TestADuplicateKidAppearsOnce(t *testing.T) {
	_, key := storedPair(t)
	source := pairSource{keys: []StoredKey{key, key}}
	service := NewService(testConfig(t), source, nil, nil)

	keys := decodeKeys(t, serve(t, service))
	assert.Len(t, keys, 1, "one key named twice is a set a client cannot index")
}

// TestSourceFailureFailsTheSet pins that the database is the one authority:
// a source that cannot be read cannot be bridged from the configuration,
// because there is no configured key to bridge to. The failure is visible,
// not a partial set a client would read as "no key is valid".
func TestSourceFailureFailsTheSet(t *testing.T) {
	source := stubSource{err: errors.New("connection refused")}
	service := NewService(testConfig(t), source, nil, nil)

	_, err := service.VerifyKeySet(context.Background())
	require.Error(t, err)
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

	require.Len(t, keys, 1, "the usable stored one")
	assert.Equal(t, "good", keys[0]["kid"])
}

// TestAnEmptyTableAnswersAnEmptySet pins the fresh-database shape: no rows
// is not an error at the endpoint, and a client reads it as "no key yet".
func TestAnEmptyTableAnswersAnEmptySet(t *testing.T) {
	service := NewService(testConfig(t), stubSource{}, nil, nil)

	require.NoError(t, service.Err())
	rec := serve(t, service)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, decodeKeys(t, rec))
}

func TestSignKeyRefusesWithoutAKeyPair(t *testing.T) {
	service := NewService(testConfig(t), stubSource{}, nil, nil)
	_, err := service.SignKey(context.Background())

	assert.ErrorIs(t, err, ErrNoStoredKeys)
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
	// One refused row must not empty the set.
	assert.Empty(t, decodeKeys(t, rec), "the only row was the refused one")
}

// TestModuleNameIsReported keeps the composition report readable.
func TestModuleNameIsReported(t *testing.T) {
	assert.Equal(t, "jwks", NewModule(nil).Name())
}
