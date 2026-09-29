package crypto

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKeyHexLength(t *testing.T) {
	first, err := GenerateKeyHex()
	require.NoError(t, err)
	assert.Len(t, first, KeyHexLength)

	second, err := GenerateKeyHex()
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "keys must not repeat")
}

func TestGenerateRandomHex(t *testing.T) {
	value, err := GenerateRandomHex(48)
	require.NoError(t, err)
	assert.Len(t, value, 96)

	_, err = GenerateRandomHex(0)
	assert.Error(t, err)
}

func TestParseKeyHex(t *testing.T) {
	encoded, err := GenerateKeyHex()
	require.NoError(t, err)

	key, err := ParseKeyHex(encoded)
	require.NoError(t, err)
	assert.Len(t, key, KeySize)
	assert.Equal(t, encoded, hex.EncodeToString(key))

	_, err = ParseKeyHex("not-hex")
	assert.ErrorIs(t, err, ErrInvalidKeyEncoding)

	_, err = ParseKeyHex(strings.Repeat("ab", KeySize-1))
	assert.ErrorIs(t, err, ErrInvalidKeyEncoding)
}

func TestNewCipherFromHexRoundTrip(t *testing.T) {
	encoded, err := GenerateKeyHex()
	require.NoError(t, err)

	cipher, err := NewCipherFromHex(encoded)
	require.NoError(t, err)

	sealed, err := cipher.Encrypt("secret")
	require.NoError(t, err)

	opened, err := cipher.Decrypt(sealed)
	require.NoError(t, err)
	assert.Equal(t, "secret", opened)
}

func TestTheFingerprintIdentifiesTheSealingKey(t *testing.T) {
	first, err := GenerateKeyHex()
	require.NoError(t, err)
	second, err := GenerateKeyHex()
	require.NoError(t, err)

	a, err := NewCipherFromHex(first)
	require.NoError(t, err)
	aAgain, err := NewCipherFromHex(first)
	require.NoError(t, err)
	b, err := NewCipherFromHex(second)
	require.NoError(t, err)

	assert.Equal(t, a.Fingerprint(), aAgain.Fingerprint())
	assert.NotEqual(t, a.Fingerprint(), b.Fingerprint())
	assert.Len(t, a.Fingerprint(), 16) // 8 bytes hex — short, never the key itself
	assert.NotContains(t, a.Fingerprint(), first[:8])
}

func TestTheAuthCipherSealsAcrossHMACKeyLengths(t *testing.T) {
	for _, size := range []int{32, 48, 64} {
		secret, err := GenerateRandomHex(size)
		require.NoError(t, err)

		cipher, err := NewAuthCipher(secret)
		require.NoError(t, err)

		sealed, err := cipher.Encrypt("totp-secret")
		require.NoError(t, err)

		opened, err := cipher.Decrypt(sealed)
		require.NoError(t, err)
		assert.Equal(t, "totp-secret", opened)
	}
}

func TestAnAuthCipherRefusesANonHexSecret(t *testing.T) {
	_, err := NewAuthCipher("not-hex-at-all")
	require.ErrorIs(t, err, ErrInvalidHMACKey)
}

func TestAuthSealKeyIsDeterministicAndIndependentOfTheRawSecret(t *testing.T) {
	secret, err := GenerateRandomHex(48)
	require.NoError(t, err)
	raw, err := ParseHMACKeyHex(secret)
	require.NoError(t, err)

	first := AuthSealKey(raw)
	second := AuthSealKey(raw)
	assert.Equal(t, first, second)
	assert.Len(t, first, KeySize)
	assert.NotEqual(t, first, raw) // derived, not the secret itself
}
