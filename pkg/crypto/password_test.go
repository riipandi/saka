package crypto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"testing/cryptotest"
)

func TestPasswordHasherDefaultsToScrypt(t *testing.T) {
	hasher := NewPasswordHasher()

	hash, err := hasher.Hash("expecto-patronum-horcrux")
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(hash, "$scrypt$N=65536,r=8,p=1$"), hash)
}

func TestPasswordHasherVerifyScrypt(t *testing.T) {
	hasher := NewPasswordHasher()

	hash, err := hasher.Hash("expecto-patronum-horcrux")
	require.NoError(t, err)

	ok, err := hasher.Verify("expecto-patronum-horcrux", hash)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = hasher.Verify("expelliarmus-wrong", hash)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestPasswordHasherVerifyArgon2id(t *testing.T) {
	hasher := NewPasswordHasher().
		WithAlgorithm(AlgorithmArgon2id).
		WithArgon2Cost(8192, 2, 2)

	hash, err := hasher.Hash("expecto-patronum-horcrux")
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$m=8192,t=2,p=2$"), hash)

	ok, err := hasher.Verify("expecto-patronum-horcrux", hash)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = hasher.Verify("expelliarmus-wrong", hash)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestPasswordHasherVerifyAcrossAlgorithms(t *testing.T) {
	scryptHash, err := NewPasswordHasher().WithScryptCost(1024, 8, 1).Hash("legacy")
	require.NoError(t, err)

	argon2Hash, err := NewPasswordHasher().
		WithAlgorithm(AlgorithmArgon2id).
		WithArgon2Cost(8192, 2, 2).
		Hash("legacy")
	require.NoError(t, err)

	defaults := NewPasswordHasher()
	for _, hash := range []string{scryptHash, argon2Hash} {
		ok, err := defaults.Verify("legacy", hash)
		require.NoError(t, err)
		assert.True(t, ok, hash)
	}
}

func TestPasswordHasherSaltsAreUnique(t *testing.T) {
	hasher := NewPasswordHasher().WithScryptCost(1024, 8, 1)

	first, err := hasher.Hash("same password")
	require.NoError(t, err)
	second, err := hasher.Hash("same password")
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "salt must never repeat")
}

func TestPasswordHasherDeterministicWithSeededRandom(t *testing.T) {
	hasher := NewPasswordHasher().WithScryptCost(1024, 8, 1)

	cryptotest.SetGlobalRandom(t, 42)
	first, err := hasher.Hash("deterministic pipeline")
	require.NoError(t, err)

	cryptotest.SetGlobalRandom(t, 42)
	second, err := hasher.Hash("deterministic pipeline")
	require.NoError(t, err)

	assert.Equal(t, first, second)
}

func TestPasswordHasherRejectsMalformedHashes(t *testing.T) {
	hasher := NewPasswordHasher()

	tests := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"no dollar", "scrypt"},
		{"missing fields", "$scrypt$N=1024,r=8,p=1$AAAA"},
		{"bad base64 salt", "$scrypt$N=1024,r=8,p=1$!!$AAAA"},
		{"unknown algorithm", "$bcrypt$r=12$AAAA$AAAA"},
		{"bad cost", "$scrypt$N=abc,r=8,p=1$AAAA$AAAA"},
		{"argon2 missing version", "$argon2id$m=8192,t=2,p=2$AAAA$AAAA"},
		{"argon2 wrong version", "$argon2id$v=16$m=8192,t=2,p=2$AAAA$AAAA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := hasher.Verify("password", tt.encoded)
			assert.Error(t, err)
		})
	}
}

func TestPasswordHasherBuilderIsImmutable(t *testing.T) {
	base := NewPasswordHasher()

	derived := base.WithAlgorithm(AlgorithmArgon2id)

	hash, err := base.Hash("x")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$scrypt$"), hash)

	hash, err = derived.Hash("x")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$"), hash)
}
