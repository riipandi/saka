package testutils

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// MigratedPostgres answers a pool over a fresh, fully migrated database:
// NewDatabase's container-per-suite isolation plus the embedded migrations
// run to head. Every feature's integration tests start here — the tests that
// query application tables need the schema, and deriving the count or shape
// from `database.EmbeddedMigrations()` keeps a new migration from passing
// silently.
//
// appName names the pool's `application_name` so the server's logs and
// `pg_stat_activity` tell a test's connections from a serving process's.
func MigratedPostgres(t testing.TB, appName string) *datastore.Postgres {
	t.Helper()

	dsn := conttest.StartPostgres(t.Context(), t).NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := database.NewMigrator(t.Context(), migrationDB, database.MigratorOptions{})
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: appName,
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.WithoutCancel(t.Context())) })
	return pool
}
