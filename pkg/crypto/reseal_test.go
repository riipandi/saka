package crypto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReSealRoundTrip(t *testing.T) {
	oldCipher, err := NewCipherFromHex("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	newCipher, err := NewCipherFromHex("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	require.NoError(t, err)

	sealed, err := oldCipher.Encrypt("the webhook signing secret")
	require.NoError(t, err)

	resealed, err := ReSeal(oldCipher, newCipher, sealed)
	require.NoError(t, err)
	assert.NotEqual(t, sealed, resealed, "a fresh nonce re-seals to a different ciphertext")

	opened, err := newCipher.Decrypt(resealed)
	require.NoError(t, err)
	assert.Equal(t, "the webhook signing secret", opened)

	_, err = oldCipher.Decrypt(resealed)
	assert.Error(t, err, "the old cipher must not open the re-sealed value")
}

func TestReSealWrongOldKeyAborts(t *testing.T) {
	wrong, err := NewCipherFromHex("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	right, err := NewCipherFromHex("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	require.NoError(t, err)
	newCipher, err := NewCipherFromHex("abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789")
	require.NoError(t, err)

	sealed, err := right.Encrypt("a totp secret")
	require.NoError(t, err)

	_, err = ReSeal(wrong, newCipher, sealed)
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "decrypt:"),
		"a wrong old key is the decrypt's failure, not the re-seal's: %s", err)
}
