package crypto

import (
	"crypto"
	_ "crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jetify.com/typeid"
)

func TestNewKeyGeneratorDefaults(t *testing.T) {
	generator, err := NewKeyGenerator("")
	require.NoError(t, err)

	secret := generator.Algorithms()
	assert.Equal(t, DefaultSecretAlgorithm, secret)
}

func TestNewKeyGeneratorSelectsTheSecretAlgorithm(t *testing.T) {
	hs512, err := NewKeyGenerator("HS512")
	require.NoError(t, err)
	assert.Equal(t, "HS512", hs512.Algorithms(), "an HS* algorithm replaces the secret")
}

func TestNewKeyGeneratorRefusesAnAsymmetricName(t *testing.T) {
	// The signing key pair is the database's (tango initialize provisions
	// it, jwks:generate rotates it), so an asymmetric algorithm has no role
	// in the environment's secret keys.
	for _, algorithm := range []string{"ES256", "RS256", "EdDSA"} {
		_, err := NewKeyGenerator(algorithm)
		assert.ErrorIs(t, err, ErrUnsupportedAlgorithm, algorithm)
	}
}

func TestNewKeyGeneratorRejectsUnsupportedAlgorithms(t *testing.T) {
	for _, algorithm := range []string{"HS999", "none"} {
		_, err := NewKeyGenerator(algorithm)
		assert.ErrorIs(t, err, ErrUnsupportedAlgorithm, algorithm)
	}
}

func TestGenerateEmitsBothSecrets(t *testing.T) {
	for _, algorithm := range []string{"", "HS256", "HS384", "HS512"} {
		generator, err := NewKeyGenerator(algorithm)
		require.NoError(t, err)

		keys, err := generator.Generate()
		require.NoError(t, err)

		assert.Equal(t, []string{EnvAppSecretKey, EnvAuthSecretKey},
			keys.Names(), algorithm)
		assert.Len(t, keys[EnvAppSecretKey], KeyHexLength, algorithm)
	}
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
	generator, err := NewKeyGenerator("")
	require.NoError(t, err)

	first, err := generator.Generate()
	require.NoError(t, err)
	second, err := generator.Generate()
	require.NoError(t, err)

	assert.NotEqual(t, first[EnvAppSecretKey], second[EnvAppSecretKey])
	assert.NotEqual(t, first[EnvAuthSecretKey], second[EnvAuthSecretKey])
}

func TestGenerateKeyPairProvisionsForTheDatabase(t *testing.T) {
	private, public, err := GenerateKeyPair("ES256")
	require.NoError(t, err)

	priv := decodeJWK(t, private)
	pub := decodeJWK(t, public)

	require.NoError(t, priv.Validate())
	require.NoError(t, pub.Validate())

	privateKID, ok := priv.KeyID()
	require.True(t, ok)
	publicKID, ok := pub.KeyID()
	require.True(t, ok)
	assert.Equal(t, privateKID, publicKID, "both halves must share the kid")

	alg, ok := pub.Algorithm()
	require.True(t, ok)
	assert.Equal(t, "ES256", alg.String())

	assert.True(t, priv.Has(jwk.ECDSADKey), "private JWK must carry the EC private scalar")
	assert.False(t, pub.Has(jwk.ECDSADKey), "public JWK must not leak the private scalar")
}

func TestGenerateKeyPairRefusesASymmetricAlgorithm(t *testing.T) {
	// A shared secret has no publishable half, so the generator refuses it
	// the same way the row it would feed does.
	_, _, err := GenerateKeyPair("HS256")
	assert.ErrorIs(t, err, ErrUnsupportedAlgorithm)
}

func TestDecodeJWKIsTheOtherHalfOfTheGenerator(t *testing.T) {
	private, public, err := GenerateKeyPair("ES256")
	require.NoError(t, err)

	// The round trip is what the jwks row depends on: the value the
	// generator writes is the value the column stores and DecodeJWK reads.
	pub, err := DecodeJWK(public)
	require.NoError(t, err)
	assert.Equal(t, jwa.EC(), pub.KeyType())
	assert.NotEmpty(t, mustKeyID(t, pub))
	_, isPrivate := pub.(jwk.ECDSAPrivateKey)
	assert.False(t, isPrivate, "the public half must carry no private material")

	priv, err := DecodeJWK(private)
	require.NoError(t, err)
	_, isPrivate = priv.(jwk.ECDSAPrivateKey)
	assert.True(t, isPrivate, "the private half is a private key")

	// Both halves carry the same kid, which is what a token header names.
	assert.Equal(t, mustKeyID(t, pub), mustKeyID(t, priv))
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
	require.True(t, ok, "a generated key carries a kid")
	return kid
}

// TestGeneratedKidIsAJwkTypeID pins the kid convention: every generated key
// names itself `jwk_<id>`, the same prefix the stored rows of the OAuth
// provider use, so a published set is uniform regardless of where the key
// came from.
func TestGeneratedKidIsAJwkTypeID(t *testing.T) {
	_, public, err := GenerateKeyPair("ES256")
	require.NoError(t, err)

	pub := decodeJWK(t, public)
	kid := mustKeyID(t, pub)
	assert.True(t, strings.HasPrefix(kid, "jwk_"), "kid %q must carry the jwk_ prefix", kid)
	_, err = typeid.Parse[jWKID](kid)
	assert.NoError(t, err, "kid %q must be a parseable TypeID", kid)

	// The thumbprint the old generator wrote never appears: a renamed key
	// would make the JWKS answer a kid the stored value does not name.
	thumbprint, err := pub.Thumbprint(crypto.SHA256)
	require.NoError(t, err)
	assert.NotEqual(t, base64.RawURLEncoding.EncodeToString(thumbprint), kid)
}

func TestGeneratedKeysSignAndVerify(t *testing.T) {
	private, public, err := GenerateKeyPair("ES256")
	require.NoError(t, err)

	priv := decodeJWK(t, private)
	signer, err := jwtutils.NewSigner[struct{}](priv, jwa.ES256())
	require.NoError(t, err)

	token, err := signer.Sign(struct{}{}, jwtutils.Standard{Subject: "user_123"})
	require.NoError(t, err)

	pub := decodeJWK(t, public)
	verifier, err := jwtutils.NewVerifier[struct{}](pub, jwa.ES256())
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
