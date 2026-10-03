package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signedURLSecret is 32 hex-encoded bytes — the parse floor an HMAC key needs.
const signedURLSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testEngineSigner(t *testing.T) *Signer {
	t.Helper()
	signer, err := NewSigner(signedURLSecret)
	require.NoError(t, err)
	return signer
}

func TestTheSignerVerifiesItsOwnSignatureAndNothingElse(t *testing.T) {
	signer := testEngineSigner(t)
	exp := time.Now().Add(time.Hour)
	sig := signer.Sign("devbucket", "avatars/x.png", exp)

	assert.True(t, signer.Verify("devbucket", "avatars/x.png", exp.Unix(), sig))
	assert.False(t, signer.Verify("devbucket", "avatars/other.png", exp.Unix(), sig), "a different key must not verify")
	assert.False(t, signer.Verify("devbucket", "avatars/x.png", exp.Add(time.Hour).Unix(), sig), "a lifted expiry must not verify")
	assert.False(t, signer.Verify("devbucket", "avatars/x.png", exp.Unix(), sig+"00"), "an extended signature must not verify")
	assert.False(t, signer.Verify("devbucket", "avatars/x.png", 0, sig), "no expiry is no access")
}

func TestASignatureNeverSurvivesASecretRotation(t *testing.T) {
	rotated := testEngineSigner(t)
	other, err := NewSigner("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	require.NoError(t, err)

	exp := time.Now().Add(time.Hour)
	assert.False(t, other.Verify("devbucket", "k", exp.Unix(), rotated.Sign("devbucket", "k", exp)))
}

func TestNewSignerRefusesAWeakSecret(t *testing.T) {
	_, err := NewSigner("616263")
	assert.Error(t, err)
	_, err = NewSigner("")
	assert.Error(t, err)
}

func TestSignedURLComposesTheBucketAndKeyUnderThePrefix(t *testing.T) {
	signer := testEngineSigner(t)
	exp := time.Now().Add(time.Hour)

	url, err := signer.SignedURL("http://x.test/storage", "devbucket", "avatars/x.png", exp)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(url, "http://x.test/storage/devbucket/avatars/x.png?"), url)

	// A base without a path is the assets URL's shape minus its prefix:
	// the engine's own mount is the fallback, not the caller's guess.
	url, err = signer.SignedURL("http://x.test", "devbucket", "k", exp)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(url, "http://x.test/storage/devbucket/k?"), url)
}
