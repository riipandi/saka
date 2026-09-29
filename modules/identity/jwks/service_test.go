package jwks

import (
	"context"
	"strings"
	"testing"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/jwtutils"
)

// hmacSecret returns a hex HMAC secret of the algorithm's minimum size, the
// form key:generate writes for AUTH_SECRET_KEY.
func hmacSecret(t *testing.T, size int) string {
	t.Helper()

	secret, err := crypto.GenerateRandomHex(size)
	require.NoError(t, err)
	return secret
}

// TestTheAlgorithmListMatchesTheJWS library pins the two lists together: the
// config package lists what it accepts without importing the library, so a
// library that grows an algorithm must fail here rather than leave a value
// config rejects but the signer supports.
func TestTheAlgorithmListMatchesTheJWSLibrary(t *testing.T) {
	registered := make(map[string]bool)
	for _, alg := range jwa.SignatureAlgorithms() {
		registered[alg.String()] = true
	}

	for _, alg := range config.JWTAlgorithms {
		assert.True(t, registered[alg],
			"auth.jwt_algorithm offers %q, which the JWS library does not know", alg)
	}
	// Every registered algorithm must be nameable, except the none family:
	// "none" is an unsigned token, which a signature algorithm setting must
	// never select.
	for name := range registered {
		if name == "none" {
			continue
		}
		assert.Contains(t, config.JWTAlgorithms, name,
			"the JWS library knows %q, which auth.jwt_algorithm cannot name", name)
	}
}

// TestBothStacksSignAndVerify is the dual-stack contract: the database's
// key pair and the configured HMAC secret each sign a token the application
// verifies.
func TestBothStacksSignAndVerify(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.SecretKey = hmacSecret(t, 32)
	pair, key := storedPair(t)
	service := NewService(cfg, pairSource{pairs: []SigningKeyPair{pair}, keys: []StoredKey{key}}, testCipher(t), nil)
	require.NoError(t, service.Err())

	ctx := context.Background()

	// The asymmetric half, verified against the published set.
	signKey, err := service.SignKey(ctx)
	require.NoError(t, err)
	asymmetricAlg, err := service.SigningAlgorithm()
	require.NoError(t, err)
	assert.False(t, asymmetricAlg.IsSymmetric())

	signer, err := jwtutils.NewSigner[struct{}](signKey, asymmetricAlg)
	require.NoError(t, err)
	token, err := signer.Sign(struct{}{}, jwtutils.Standard{Subject: "usr_pair"})
	require.NoError(t, err)

	set, err := service.VerifyKeySet(ctx)
	require.NoError(t, err)
	verifier, err := jwtutils.NewVerifier[struct{}](nil, asymmetricAlg)
	require.NoError(t, err)
	verified, err := verifier.WithKeySet(set).Verify(token)
	require.NoError(t, err)
	assert.Equal(t, "usr_pair", verified.Subject)

	// The symmetric half, verified with the secret the process holds.
	hmacKey, err := service.HMACKey(ctx)
	require.NoError(t, err)
	hmacAlg, err := service.HMACAlgorithm()
	require.NoError(t, err)
	assert.True(t, hmacAlg.IsSymmetric())

	hmacSigner, err := jwtutils.NewSigner[struct{}](hmacKey, hmacAlg)
	require.NoError(t, err)
	hmacToken, err := hmacSigner.Sign(struct{}{}, jwtutils.Standard{Subject: "usr_hmac"})
	require.NoError(t, err)

	hmacVerifier, err := jwtutils.NewVerifier[struct{}](hmacKey, hmacAlg)
	require.NoError(t, err)
	hmacVerified, err := hmacVerifier.Verify(hmacToken)
	require.NoError(t, err)
	assert.Equal(t, "usr_hmac", hmacVerified.Subject)
}

// TestTheHMACSecretNeverReachesTheKeySet is the boundary the dual stack rests
// on: a published set holds public keys only, so an HS* token is verified
// locally and never from the endpoint.
func TestTheHMACSecretNeverReachesTheKeySet(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.SecretKey = hmacSecret(t, 32)
	service := NewService(cfg, nil, nil, nil)
	require.NoError(t, service.Err())

	set, err := service.VerifyKeySet(context.Background())
	require.NoError(t, err)

	for i := range set.Len() {
		key, ok := set.Key(i)
		require.True(t, ok)
		assert.NotEqual(t, jwa.OctetSeq(), key.KeyType(),
			"a symmetric key must never be published")
	}
}

// TestTheHMACAlgorithmFollowsTheSecretLength pins the derivation: the secret
// carries no algorithm, so its size is the only signal, and it is the same
// rule key:generate follows when it sizes the value.
func TestTheHMACAlgorithmFollowsTheSecretLength(t *testing.T) {
	cases := []struct {
		size int
		want jwa.SignatureAlgorithm
	}{
		{32, jwa.HS256()},
		{48, jwa.HS384()},
		{64, jwa.HS512()},
	}
	for _, tc := range cases {
		cfg := testConfig(t)
		cfg.Auth.SecretKey = hmacSecret(t, tc.size)
		service := NewService(cfg, nil, nil, nil)
		require.NoError(t, service.Err())

		alg, err := service.HMACAlgorithm()
		require.NoError(t, err)
		assert.Equal(t, tc.want, alg, "a %d-byte secret is %s", tc.size, tc.want)
	}
}

// TestAConfiguredAlgorithmWinsOverTheDerivedOne covers the deployment that
// configures both stacks: auth.jwt_algorithm is how it says which one signs.
func TestAConfiguredAlgorithmWinsOverTheDerivedOne(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.SecretKey = hmacSecret(t, 32)
	cfg.Auth.JWTAlgorithm = "HS256"

	service := NewService(cfg, nil, nil, nil)
	require.NoError(t, service.Err())

	alg, err := service.SigningAlgorithm()
	require.NoError(t, err)
	assert.Equal(t, jwa.HS256(), alg, "the configured algorithm decides, not the key pair")
}

// TestTheDerivedAlgorithmComesFromTheRow covers the common deployment: no
// algorithm override, so the active signing row's own alg is the answer.
func TestTheDerivedAlgorithmComesFromTheRow(t *testing.T) {
	private, public, err := crypto.GenerateKeyPair("ES384")
	require.NoError(t, err)
	parsed, err := crypto.DecodeJWK(public)
	require.NoError(t, err)
	kid, _ := parsed.KeyID()

	cfg := config.Default()
	cfg.Auth.SecretKey = ""
	cfg.Auth.JWTAlgorithm = ""
	source := pairSource{
		pairs: []SigningKeyPair{{KeyID: kid, Algorithm: "ES384", PrivateKey: []byte(private)}},
		keys:  []StoredKey{{KeyID: kid, Algorithm: "ES384", PublicKey: []byte(public)}},
	}

	service := NewService(cfg, source, nil, nil)
	require.NoError(t, service.Err())

	alg, err := service.SigningAlgorithm()
	require.NoError(t, err)
	assert.Equal(t, jwa.ES384(), alg, "the row's own alg is the answer")
}

// TestAConfiguredAlgorithmWithoutItsMaterialIsRefused keeps the mismatch from
// reaching signing time.
func TestAConfiguredAlgorithmWithoutItsMaterialIsRefused(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.JWTAlgorithm = "HS256" // no secret is configured

	service := NewService(cfg, nil, nil, nil)

	require.Error(t, service.Err())
	assert.Contains(t, service.Err().Error(), "auth.secret_key")
}

// TestSigningRefusesWhenNothingIsConfigured covers the empty configuration.
func TestSigningRefusesWhenNothingIsConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.SecretKey = ""

	service := NewService(cfg, stubSource{}, nil, nil)
	require.NoError(t, service.Err())

	_, err := service.SigningAlgorithm()
	assert.ErrorIs(t, err, ErrNoSigningKey)
}

// TestAShorterHMACSecretThanHS256IsRefused covers the floor: a secret below
// the HS256 minimum is not a key, whatever the deployment intended.
func TestAShorterHMACSecretThanHS256IsRefused(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.SecretKey = "aabbccdd" // 4 bytes

	service := NewService(cfg, nil, nil, nil)

	require.Error(t, service.Err())
	assert.Contains(t, service.Err().Error(), "auth.secret_key")
}

// TestHS384AndHS512SecretsAreAccepted covers the bug this found: the reader
// for the AES key insists on exactly 32 bytes, so a 48- or 64-byte HMAC
// secret would have been rejected.
func TestHS384AndHS512SecretsAreAccepted(t *testing.T) {
	for _, size := range []int{32, 48, 64} {
		cfg := testConfig(t)
		cfg.Auth.SecretKey = hmacSecret(t, size)

		service := NewService(cfg, nil, nil, nil)
		require.NoError(t, service.Err(), "a %d-byte secret must be accepted", size)

		key, err := service.HMACKey(context.Background())
		require.NoError(t, err)
		assert.Equal(t, jwa.OctetSeq(), key.KeyType())
	}
}

// TestAnHMACKeySignsAHeaderTheVerifierMatches pins that the signing key the
// service hands out carries what a token header needs.
func TestAnHMACKeySignsAHeaderTheVerifierMatches(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.SecretKey = hmacSecret(t, 32)
	service := NewService(cfg, nil, nil, nil)
	require.NoError(t, service.Err())

	key, err := service.HMACKey(context.Background())
	require.NoError(t, err)

	symmetric, ok := key.(jwk.SymmetricKey)
	require.True(t, ok)
	octets, ok := symmetric.Octets()
	require.True(t, ok)
	require.Len(t, octets, 32)

	signer, err := jwtutils.NewSigner[struct{}](key, jwa.HS256())
	require.NoError(t, err)
	token, err := signer.Sign(struct{}{}, jwtutils.Standard{Subject: "usr_x"})
	require.NoError(t, err)

	// A header is present and names the algorithm the signer used.
	message, err := jws.Parse([]byte(token))
	require.NoError(t, err)
	header := message.Signatures()[0].ProtectedHeaders()
	alg, ok := header.Algorithm()
	require.True(t, ok)
	assert.Equal(t, "HS256", alg.String())
}

// testCipher builds the cipher the seal-and-unseal round trips share.
func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()

	cipher, err := crypto.NewCipherFromHex(strings.Repeat("ab", 32))
	require.NoError(t, err)
	return cipher
}
