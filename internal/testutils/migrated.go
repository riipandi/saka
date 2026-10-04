package testutils

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/migration"
	fwqueue "github.com/riipandi/saka/framework/queue"
	fwscheduler "github.com/riipandi/saka/framework/scheduler"
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
	// The sets composed here are the ones the binary composes: the app set
	// and every framework package's own — a feature's test sees the schema
	// the serving process runs.
	sets, err := migration.Compose(database.Schema(), fwqueue.Schema(), fwscheduler.Schema(), fwaudit.Schema())
	require.NoError(t, err)
	for _, set := range sets {
		setMigrator, migrateErr := migration.NewMigrator(t.Context(), migrationDB, set, migration.MigratorOptions{})
		require.NoError(t, migrateErr)
		_, migrateErr = setMigrator.Up(t.Context())
		require.NoError(t, migrateErr)
	}
	require.NoError(t, err)
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

// TestMigrator answers a runner over the composed sets — the ones the binary
// runs — for a test that migrates a database handle it manages itself.
func TestMigrator(t testing.TB, db *sql.DB) *migration.Runner {
	t.Helper()
	runner, err := migration.NewRunner(t.Context(), db, ComposedSets(), migration.MigratorOptions{})
	require.NoError(t, err)
	return runner
}

// ComposedSets is the set list the binary runs: the app set and every
// framework package's own.
func ComposedSets() []migration.Set {
	return []migration.Set{database.Schema(), fwqueue.Schema(), fwscheduler.Schema(), fwaudit.Schema()}
}
