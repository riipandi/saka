package jwks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/testutils"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// migratedPool opens a fresh database with the migrations applied, so the
// query runs against the real table rather than a hand-built one.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "jwks_test")
}

func TestActiveSigningKeysFiltersTheRows(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	ctx := t.Context()
	now := time.Now()

	// One usable key, plus every reason a row is not published.
	rows := []struct {
		kid     string
		active  bool
		useFor  string
		expires *time.Time
	}{
		{"live", true, UseSignature, nil},
		{"expires-later", true, UseSignature, ptr(now.Add(time.Hour))},
		{"inactive", false, UseSignature, nil},
		{"encryption", true, "enc", nil},
		{"expired", true, UseSignature, ptr(now.Add(-time.Hour))},
	}
	for _, row := range rows {
		_, err := pool.Exec(ctx, `
			INSERT INTO public.jwks (key_id, algorithm, key_type, public_key, use_for, is_active, expires_at)
			VALUES ($1, 'ES256', 'EC', '{"kty":"EC"}', $2, $3, $4)`,
			row.kid, row.useFor, row.active, row.expires)
		require.NoError(t, err)
	}

	keys, err := NewRepository(pool, testCipher(t)).ActiveSigningKeys(ctx)
	require.NoError(t, err)

	got := make([]string, 0, len(keys))
	for _, key := range keys {
		got = append(got, key.KeyID)
	}
	assert.Equal(t, []string{"expires-later", "live"}, got)
}

// TestActiveSigningKeysNeverReadsThePrivateColumn is the security rule of the
// query: the table holds a sealed private key for the provider role, and the
// published set must not be able to reach it.
func TestActiveSigningKeysNeverReadsThePrivateColumn(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.jwks (key_id, algorithm, key_type, public_key, private_key, use_for, is_active)
		VALUES ('with-private', 'ES256', 'EC', '{"kty":"EC"}', 'enc:sealed-value', 'sig', TRUE)`)
	require.NoError(t, err)

	keys, err := NewRepository(pool, testCipher(t)).ActiveSigningKeys(t.Context())
	require.NoError(t, err)

	require.Len(t, keys, 1)
	assert.Equal(t, "with-private", keys[0].KeyID)
	assert.Equal(t, []byte(`{"kty":"EC"}`), keys[0].PublicKey)
}

func ptr[T any](v T) *T { return &v }
