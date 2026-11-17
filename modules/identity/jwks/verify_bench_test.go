package jwks

import (
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/jwtutils"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// BenchmarkAccessVerify measures what every authenticated request pays: the
// verifier over a database-backed key service. It uses only calls whose shape
// did not change with the cache, so the same file measures both sides.
func BenchmarkAccessVerify(b *testing.B) {
	conttest.SkipWithoutDocker(b)

	pool := migratedPool(b)
	cipher := testCipher(b)
	service := NewService(testConfig(b), NewRepository(pool, cipher), cipher, nil)
	pair, err := service.GeneratePair(crypto.DefaultSignatureAlgorithm)
	require.NoError(b, err)
	require.NoError(b, insertProvisionedPair(b.Context(), pool, pair))

	algorithm, ok := jwa.LookupSignatureAlgorithm(crypto.DefaultSignatureAlgorithm)
	require.True(b, ok)
	key, err := service.SignKey(b.Context())
	require.NoError(b, err)
	signer, err := jwtutils.NewSigner[jwtutils.AccessClaims](key, algorithm)
	require.NoError(b, err)
	token, err := signer.WithIssuer("https://saka.example").WithTTL(time.Hour).Sign(
		jwtutils.AccessClaims{Username: "hermione"},
		jwtutils.Standard{Subject: "usr_bench", IssuedAt: time.Now(), NotBefore: time.Now()})
	require.NoError(b, err)

	verifier := jwtutils.NewAccessVerifier(service, "https://saka.example")
	b.ResetTimer()
	for range b.N {
		if _, err := verifier.Verify(b.Context(), token); err != nil {
			b.Fatal(err)
		}
	}
}
