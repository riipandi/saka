package jwks

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/jwtutils"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

const cacheTestIssuer = "https://saka.example"

// readCountingSource counts the reads that reach the database, the cost the
// cache exists to remove.
type readCountingSource struct {
	pairSource
	pairReads atomic.Int64
	keyReads  atomic.Int64
}

func (c *readCountingSource) ActiveSigningKeys(ctx context.Context) ([]StoredKey, error) {
	c.keyReads.Add(1)
	return c.pairSource.ActiveSigningKeys(ctx)
}

func (c *readCountingSource) ActiveSigningKeyPairs(ctx context.Context) ([]SigningKeyPair, error) {
	c.pairReads.Add(1)
	return c.pairSource.ActiveSigningKeyPairs(ctx)
}

func signAccessToken(t *testing.T, key jwk.Key, service *Service) string {
	t.Helper()
	algorithm, err := service.SigningAlgorithm(t.Context())
	require.NoError(t, err)
	signer, err := jwtutils.NewSigner[jwtutils.AccessClaims](key, algorithm)
	require.NoError(t, err)
	token, err := signer.WithIssuer(cacheTestIssuer).WithTTL(time.Hour).Sign(
		jwtutils.AccessClaims{Username: "hermione"},
		jwtutils.Standard{Subject: "usr_cache", IssuedAt: time.Now(), NotBefore: time.Now()})
	require.NoError(t, err)
	return token
}

// TestTheSigningMaterialIsReadOncePerWindow pins the cost the audit named:
// after the first request, verifying a token reads nothing from the database
// and opens no private key, until the window passes or a rotation drops it.
func TestTheSigningMaterialIsReadOncePerWindow(t *testing.T) {
	pair, key := storedPair(t)
	source := &readCountingSource{pairSource: pairSource{pairs: []SigningKeyPair{pair}, keys: []StoredKey{key}}}
	service := NewService(testConfig(t), source, testCipher(t), nil)
	now := time.Now()
	service.now = func() time.Time { return now }
	verifier := jwtutils.NewAccessVerifier(service, cacheTestIssuer)

	signKey, err := service.SignKey(t.Context())
	require.NoError(t, err)
	token := signAccessToken(t, signKey, service)
	_, err = verifier.Verify(t.Context(), token)
	require.NoError(t, err)

	pairReads, keyReads := source.pairReads.Load(), source.keyReads.Load()
	for range 50 {
		_, err = verifier.Verify(t.Context(), token)
		require.NoError(t, err)
		_, err = service.SignKey(t.Context())
		require.NoError(t, err)
	}
	assert.Equal(t, pairReads, source.pairReads.Load(), "no pair read inside the window")
	assert.Equal(t, keyReads, source.keyReads.Load(), "no key read inside the window")

	// The window passes: one fresh read each, then quiet again.
	now = now.Add(SigningCacheTTL + time.Second)
	_, err = verifier.Verify(t.Context(), token)
	require.NoError(t, err)
	_, err = service.SignKey(t.Context())
	require.NoError(t, err)
	assert.Greater(t, source.pairReads.Load(), pairReads)
	assert.Greater(t, source.keyReads.Load(), keyReads)

	// A rotation drops everything at once.
	keyReads = source.keyReads.Load()
	service.dropSigningCaches()
	_, err = verifier.Verify(t.Context(), token)
	require.NoError(t, err)
	assert.Greater(t, source.keyReads.Load(), keyReads)
}

// TestAnAsymmetricDeploymentRefusesATokenWithoutAKnownKid keeps the algorithm
// pinned now that the verifier reads the published set: an HMAC token, and an
// asymmetric one whose kid the set does not hold, both fail.
func TestAnAsymmetricDeploymentRefusesATokenWithoutAKnownKid(t *testing.T) {
	pair, key := storedPair(t)
	service := NewService(testConfig(t), pairSource{pairs: []SigningKeyPair{pair}, keys: []StoredKey{key}}, testCipher(t), nil)
	verifier := jwtutils.NewAccessVerifier(service, cacheTestIssuer)

	hmacKey, err := service.HMACKey(t.Context())
	require.NoError(t, err)
	hmacAlgorithm, err := service.HMACAlgorithm()
	require.NoError(t, err)
	signer, err := jwtutils.NewSigner[jwtutils.AccessClaims](hmacKey, hmacAlgorithm)
	require.NoError(t, err)
	hmacToken, err := signer.WithIssuer(cacheTestIssuer).WithTTL(time.Hour).Sign(
		jwtutils.AccessClaims{}, jwtutils.Standard{Subject: "usr_hmac", IssuedAt: time.Now(), NotBefore: time.Now()})
	require.NoError(t, err)
	_, err = verifier.Verify(t.Context(), hmacToken)
	assert.Error(t, err, "the secret is not an asymmetric deployment's verification key")

	otherPair, _ := storedPair(t)
	other := NewService(testConfig(t), pairSource{pairs: []SigningKeyPair{otherPair}}, testCipher(t), nil)
	otherKey, err := other.SignKey(t.Context())
	require.NoError(t, err)
	_, err = verifier.Verify(t.Context(), signAccessToken(t, otherKey, other))
	assert.Error(t, err, "a key outside the published set verifies nothing")
}

// TestTwoActiveKeysBothVerifyAndTheNewestSigns is the rotation the staged key
// is for: with two active rows the newer one signs and either one's tokens
// verify, so a token in flight survives the staging and the retirement is the
// only moment a key stops being accepted.
func TestTwoActiveKeysBothVerifyAndTheNewestSigns(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	cipher := testCipher(t)
	service := NewService(testConfig(t), NewRepository(pool, cipher), cipher, nil)
	older, err := service.GeneratePair(crypto.DefaultSignatureAlgorithm)
	require.NoError(t, err)
	newer, err := service.GeneratePair(crypto.DefaultSignatureAlgorithm)
	require.NoError(t, err)
	require.NoError(t, insertProvisionedPair(t.Context(), pool, older))
	require.NoError(t, insertProvisionedPair(t.Context(), pool, newer))

	// The older key sorts last by creation but could sort first by name — the
	// regression the old ordering had.
	_, err = pool.Exec(t.Context(), `UPDATE public.jwks SET created_at = now() - interval '1 hour' WHERE key_id = $1`, older.KeyID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `UPDATE public.jwks SET created_at = now() WHERE key_id = $1`, newer.KeyID)
	require.NoError(t, err)

	signKey, err := service.SignKey(t.Context())
	require.NoError(t, err)
	kid, ok := signKey.KeyID()
	require.True(t, ok)
	assert.Equal(t, newer.KeyID, kid, "the newest active key signs")

	opened, err := service.OIDCSigningKeys(t.Context())
	require.NoError(t, err)
	require.Len(t, opened, 2)
	verifier := jwtutils.NewAccessVerifier(service, cacheTestIssuer)
	for _, key := range opened {
		_, err = verifier.Verify(t.Context(), signAccessToken(t, key, service))
		require.NoError(t, err, "every active key's tokens verify")
	}
}

// TestAnHMACTokenNamingARealKidIsRefused is the algorithm-confusion probe: a
// token whose header names a published kid but whose algorithm is HS256, signed
// with the public key's own bytes as the secret. The key set pins each key to
// the `alg` its row declares, so the token is refused.
func TestAnHMACTokenNamingARealKidIsRefused(t *testing.T) {
	pair, stored := storedPair(t)
	service := NewService(testConfig(t), pairSource{pairs: []SigningKeyPair{pair}, keys: []StoredKey{stored}}, testCipher(t), nil)
	verifier := jwtutils.NewAccessVerifier(service, cacheTestIssuer)

	secret, err := jwk.Import(append([]byte("saka-confusion-"), stored.PublicKey...)[:48])
	require.NoError(t, err)
	headers := jws.NewHeaders()
	require.NoError(t, headers.Set(jws.KeyIDKey, stored.KeyID))
	payload := fmt.Sprintf(`{"iss":%q,"sub":"usr_forged","exp":%d,"iat":%d,"nbf":%d}`,
		cacheTestIssuer, time.Now().Add(time.Hour).Unix(), time.Now().Unix(), time.Now().Unix())
	forged, err := jws.Sign([]byte(payload), jws.WithKey(jwa.HS384(), secret, jws.WithProtectedHeaders(headers)))
	require.NoError(t, err)

	_, err = verifier.Verify(t.Context(), string(forged))
	assert.Error(t, err)
}
