package crypto

import (
	"encoding/base64"
	"testing"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewKeyGeneratorDefaults(t *testing.T) {
	generator, err := NewKeyGenerator("")
	require.NoError(t, err)

	keyPair, secret := generator.Algorithms()
	assert.Equal(t, DefaultSignatureAlgorithm, keyPair)
	assert.Equal(t, DefaultSecretAlgorithm, secret)
}

func TestNewKeyGeneratorSelectsRole(t *testing.T) {
	es384, err := NewKeyGenerator("ES384")
	require.NoError(t, err)
	keyPair, secret := es384.Algorithms()
	assert.Equal(t, "ES384", keyPair, "an asymmetric algorithm replaces the key pair")
	assert.Equal(t, DefaultSecretAlgorithm, secret, "the HMAC secret keeps its default")

	hs512, err := NewKeyGenerator("HS512")
	require.NoError(t, err)
	keyPair, secret = hs512.Algorithms()
	assert.Equal(t, DefaultSignatureAlgorithm, keyPair, "the key pair keeps its default")
	assert.Equal(t, "HS512", secret, "an HS* algorithm replaces the secret")
}

func TestNewKeyGeneratorRejectsUnsupportedAlgorithms(t *testing.T) {
	for _, algorithm := range []string{"HS999", "none", "ES999"} {
		_, err := NewKeyGenerator(algorithm)
		assert.ErrorIs(t, err, ErrUnsupportedAlgorithm, algorithm)
	}
}

func TestGenerateEmitsAllFourKeys(t *testing.T) {
	for _, algorithm := range []string{"", "ES256", "ES384", "ES512", "EdDSA", "RS256", "PS256", "HS256", "HS384", "HS512"} {
		generator, err := NewKeyGenerator(algorithm)
		require.NoError(t, err)

		keys, err := generator.Generate()
		require.NoError(t, err)

		assert.Equal(t, []string{EnvAppSecretKey, EnvAuthPrivateKey, EnvAuthPublicKey, EnvAuthSecretKey},
			keys.Names(), algorithm)
		assert.Len(t, keys[EnvAppSecretKey], KeyHexLength, algorithm)
	}
}

func TestGenerateKeyPair(t *testing.T) {
	generator, err := NewKeyGenerator("ES256")
	require.NoError(t, err)

	keys, err := generator.Generate()
	require.NoError(t, err)

	_, err = ParseKeyHex(keys[EnvAppSecretKey])
	require.NoError(t, err)

	private := decodeJWK(t, keys[EnvAuthPrivateKey])
	public := decodeJWK(t, keys[EnvAuthPublicKey])

	require.NoError(t, private.Validate())
	require.NoError(t, public.Validate())

	privateKID, ok := private.KeyID()
	require.True(t, ok)
	publicKID, ok := public.KeyID()
	require.True(t, ok)
	assert.Equal(t, privateKID, publicKID, "both halves must share the kid")

	alg, ok := public.Algorithm()
	require.True(t, ok)
	assert.Equal(t, "ES256", alg.String())

	assert.True(t, private.Has(jwk.ECDSADKey), "private JWK must carry the EC private scalar")
	assert.False(t, public.Has(jwk.ECDSADKey), "public JWK must not leak the private scalar")
}

func TestGenerateHMACSecretSize(t *testing.T) {
	for algorithm, want := range map[string]int{"HS256": 64, "HS384": 96, "HS512": 128} {
		generator, err := NewKeyGenerator(algorithm)
		require.NoError(t, err)

		keys, err := generator.Generate()
		require.NoError(t, err)
		assert.Len(t, keys[EnvAuthSecretKey], want, algorithm)
	}
}

func TestGeneratedKeysAreUnique(t *testing.T) {
	generator, err := NewKeyGenerator("ES256")
	require.NoError(t, err)

	first, err := generator.Generate()
	require.NoError(t, err)
	second, err := generator.Generate()
	require.NoError(t, err)

	assert.NotEqual(t, first[EnvAppSecretKey], second[EnvAppSecretKey])
	assert.NotEqual(t, first[EnvAuthPrivateKey], second[EnvAuthPrivateKey])
	assert.NotEqual(t, first[EnvAuthSecretKey], second[EnvAuthSecretKey])
}

func TestDecodeJWKIsTheOtherHalfOfTheGenerator(t *testing.T) {
	generator, err := NewKeyGenerator("ES256")
	require.NoError(t, err)
	keys, err := generator.Generate()
	require.NoError(t, err)

	// The round trip is what a caller reading AUTH_PUBLIC_KEY depends on: the
	// value the generator writes is the value DecodeJWK reads.
	public, err := DecodeJWK(keys[EnvAuthPublicKey])
	require.NoError(t, err)
	assert.Equal(t, jwa.EC(), public.KeyType())
	assert.NotEmpty(t, mustKeyID(t, public))
	_, isPrivate := public.(jwk.ECDSAPrivateKey)
	assert.False(t, isPrivate, "the public half must carry no private material")

	private, err := DecodeJWK(keys[EnvAuthPrivateKey])
	require.NoError(t, err)
	_, isPrivate = private.(jwk.ECDSAPrivateKey)
	assert.True(t, isPrivate, "the private half is a private key")

	// Both halves carry the same kid, which is what a token header names.
	assert.Equal(t, mustKeyID(t, public), mustKeyID(t, private))
}

func TestDecodeJWKRejectsAMalformedValue(t *testing.T) {
	// A value that is not base64 fails before the JWK parse, and one that is
	// base64 but not a key fails at the parse: both are a broken
	// configuration, reported rather than returned as a nil key.
	_, err := DecodeJWK("not base64!")
	assert.Error(t, err)

	_, err = DecodeJWK(base64.RawStdEncoding.EncodeToString([]byte("not a jwk")))
	assert.Error(t, err)
}

func mustKeyID(t *testing.T, key jwk.Key) string {
	t.Helper()

	kid, ok := key.KeyID()
	require.True(t, ok, "a generated key carries a thumbprint kid")
	return kid
}

func TestGeneratedKeysSignAndVerify(t *testing.T) {
	generator, err := NewKeyGenerator("ES256")
	require.NoError(t, err)
	keys, err := generator.Generate()
	require.NoError(t, err)

	private := decodeJWK(t, keys[EnvAuthPrivateKey])
	signer, err := jwtutils.NewSigner[struct{}](private, jwa.ES256())
	require.NoError(t, err)

	token, err := signer.Sign(struct{}{}, jwtutils.Standard{Subject: "user_123"})
	require.NoError(t, err)

	public := decodeJWK(t, keys[EnvAuthPublicKey])
	verifier, err := jwtutils.NewVerifier[struct{}](public, jwa.ES256())
	require.NoError(t, err)

	verified, err := verifier.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, "user_123", verified.Subject)
}

// decodeJWK decodes a base64-encoded JWK value.
func decodeJWK(t *testing.T, encoded string) jwk.Key {
	t.Helper()
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	require.NoError(t, err)

	key, err := jwk.ParseKey(raw)
	require.NoError(t, err)
	return key
}

// TestTheSecretLengthRoundTripsThroughTheAlgorithm pins the single source of
// the length-to-algorithm rule: a secret key:generate sized for an algorithm
// reads back as that same algorithm, so the generator and the jwks service
// cannot drift apart.
func TestTheSecretLengthRoundTripsThroughTheAlgorithm(t *testing.T) {
	for _, algorithm := range []string{"HS256", "HS384", "HS512"} {
		generator, err := NewKeyGenerator(algorithm)
		require.NoError(t, err)

		keys, err := generator.Generate()
		require.NoError(t, err)

		secret, err := ParseHMACKeyHex(keys[EnvAuthSecretKey])
		require.NoError(t, err)

		derived, ok := HMACAlgorithmForSecret(len(secret))
		require.True(t, ok, "a %s-sized secret must name an algorithm", algorithm)
		assert.Equal(t, algorithm, derived)
	}

	// Below the table's floor nothing is named: the caller refuses rather
	// than defaulting to HS256.
	_, ok := HMACAlgorithmForSecret(16)
	assert.False(t, ok)
}
