//go:build debug

package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/tango/database/seeders"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/envfile"
	"github.com/riipandi/tango/pkg/testutils"
)

// migratedDatabase returns a DSN for a fresh database with the schema applied,
// so a seed has tables to write to.
func migratedDatabase(t *testing.T) string {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	return envFile
}

// countUsers reads the number of accounts straight from the database, so a test
// cannot pass on what the command printed.
func countUsers(t *testing.T, envFile string) int {
	t.Helper()

	file, err := envfile.Load(envFile)
	require.NoError(t, err)
	dsn, ok := file.Get(envfile.DatabaseURL)
	require.True(t, ok)

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })

	var count int
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM public.users").Scan(&count))
	return count
}

func TestMigrateSeedCreatesTheDefaultUser(t *testing.T) {
	envFile := migratedDatabase(t)

	out, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	assert.Contains(t, out, seeders.UserSeederName+" "+seeders.DefaultUser.Email+" created")
	assert.Contains(t, out, "status: 70 created, 0 skipped")
	assert.Equal(t, 6, countUsers(t, envFile))
}

// Running the command twice must not create a second account.
func TestMigrateSeedIsIdempotent(t *testing.T) {
	envFile := migratedDatabase(t)

	_, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	assert.Contains(t, out, seeders.UserSeederName+" "+seeders.DefaultUser.Email+" skipped")
	assert.Contains(t, out, "0 created, 67 skipped")
	assert.Equal(t, 6, countUsers(t, envFile))
}

// --dry-run must report the work in future tense and write nothing.
func TestMigrateSeedDryRunWritesNothing(t *testing.T) {
	envFile := migratedDatabase(t)

	out, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--dry-run")
	require.NoError(t, err)

	assert.Contains(t, out, seeders.UserSeederName+" "+seeders.DefaultUser.Email+" would create")
	assert.Contains(t, out, "60 to create, 4 to skip")
	assert.Zero(t, countUsers(t, envFile))
}

// A seed against an empty database must say what to do, not report a missing
// relation.
func TestMigrateSeedRequiresSchema(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	out, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d migrations pending", migrationTotal()))
	assert.Contains(t, err.Error(), "run migrate:up first")
	assert.Empty(t, out)
}

// A database that stopped short of the last migration must also be refused. A
// table check would pass here — public.users exists after version 2 — and the
// seed would then run against a schema the migrations have not finished.
func TestMigrateSeedRequiresEveryMigration(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force", "--to=2")
	require.NoError(t, err)

	out, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d migrations pending", migrationTotal()-2))
	assert.Empty(t, out)
	assert.Zero(t, countUsers(t, envFile))
}

// Seeding writes data, so it asks first. A declined prompt must leave the
// database untouched.
func TestMigrateSeedDeclinedLeavesDatabaseEmpty(t *testing.T) {
	envFile := migratedDatabase(t)

	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = isTerminal })

	out, err := runMigrateSeedCmd(t, "n\n", "--env-file="+envFile)
	require.NoError(t, err)

	assert.Contains(t, out, "seed the database? [y/N]")
	assert.Contains(t, out, "nothing seeded")
	assert.Zero(t, countUsers(t, envFile))
}

// An accepted prompt seeds without --force.
func TestMigrateSeedAcceptedPromptSeeds(t *testing.T) {
	envFile := migratedDatabase(t)

	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = isTerminal })

	out, err := runMigrateSeedCmd(t, "y\n", "--env-file="+envFile)
	require.NoError(t, err)

	assert.Contains(t, out, "status: 70 created, 0 skipped")
	assert.Equal(t, 6, countUsers(t, envFile))
}

// A seeder that fails must roll the whole run back, so a partial seed is never
// left behind. The command owns the transaction, so this is exercised where the
// transaction is: in the seeders package.

func runMigrateSeedCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	return runMigrateCmd(t, migrateSeedCmd, stdin, args...)
}
