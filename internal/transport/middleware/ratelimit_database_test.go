package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	fw "github.com/riipandi/saka/framework/middleware"
	"github.com/riipandi/saka/pkg/testutils"
)

// migratedPool answers a pool over a fresh, fully migrated database, the idiom
// the queue tests use: the limiter's check function is an app migration's, so
// the tests need the schema it ships in.
// migratedPool applies the migrations to a fresh test database and returns
// the pool the limiter runs on. The same idiom the queue tests use.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := openTestMigrators(t, migrationDB)
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "ratelimit_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })
	return pool
}

func TestDatabaseLimiterCountsTheWindow(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	limiter := NewDatabaseLimiter(migratedPool(t))
	policy := fw.Policy{Limit: 2, Window: time.Minute}

	first, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", policy)
	require.NoError(t, err)
	assert.False(t, first.Limited)
	assert.Equal(t, 2, first.Limit)
	assert.Equal(t, 1, first.Remaining)

	second, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", policy)
	require.NoError(t, err)
	assert.Equal(t, 0, second.Remaining)

	third, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", policy)
	require.NoError(t, err)
	assert.True(t, third.Limited)
	assert.Equal(t, 0, third.Remaining)
	assert.Greater(t, third.RetryAfter, time.Duration(0), "the function's own retry hint")
}

func TestDatabaseLimiterBucketsAreIndependent(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	limiter := NewDatabaseLimiter(migratedPool(t))

	// The same address in two buckets spends two budgets: the credential
	// attempts a client makes must not starve the rest of its traffic.
	_, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", fw.Policy{Limit: 1, Window: time.Minute})
	require.NoError(t, err)
	_, err = limiter.Allow(t.Context(), "auth:ip_192_0_2_7", fw.Policy{Limit: 1, Window: time.Minute})
	require.NoError(t, err)
	spent, err := limiter.Allow(t.Context(), "auth:ip_192_0_2_7", fw.Policy{Limit: 1, Window: time.Minute})
	require.NoError(t, err)
	assert.True(t, spent.Limited, "the credential bucket is spent")

	other, err := limiter.Allow(t.Context(), "default:ip_192_0_2_7", fw.Policy{Limit: 1, Window: time.Minute})
	require.NoError(t, err)
	assert.False(t, other.Limited, "the default bucket is the client's own budget")
}

func TestDatabaseLimiterKeysAreIndependent(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	limiter := NewDatabaseLimiter(migratedPool(t))
	policy := fw.Policy{Limit: 1, Window: time.Minute}

	_, err := limiter.Allow(t.Context(), "ip_192_0_2_7", policy)
	require.NoError(t, err)

	other, err := limiter.Allow(t.Context(), "ip_192_0_2_8", policy)
	require.NoError(t, err)
	assert.False(t, other.Limited, "a second client starts its own window")
}
