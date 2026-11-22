//go:build debug

package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/seeders"
	"github.com/riipandi/saka/pkg/envfile"
	"github.com/riipandi/saka/pkg/testutils"
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

// seedTotals parses the report's status line and answers the two totals it
// carries, so a test asserts the invariants between runs instead of a
// hand-maintained counter that every new seed item would have to bump.
func seedTotals(t *testing.T, out string) (created, skipped int) {
	t.Helper()

	m := regexp.MustCompile(`status: (\d+) created, (\d+) skipped`).FindStringSubmatch(out)
	require.NotNil(t, m, "the seed report carries no status line")
	created, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	skipped, err = strconv.Atoi(m[2])
	require.NoError(t, err)
	return created, skipped
}

// dryRunTotals parses the dry-run report's status line, the future-tense
// twin of seedTotals.
func dryRunTotals(t *testing.T, out string) (toCreate, toSkip int) {
	t.Helper()

	m := regexp.MustCompile(`status: (\d+) to create, (\d+) to skip`).FindStringSubmatch(out)
	require.NotNil(t, m, "the dry run carries no status line")
	toCreate, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	toSkip, err = strconv.Atoi(m[2])
	require.NoError(t, err)
	return toCreate, toSkip
}

func TestMigrateSeedCreatesTheDefaultUser(t *testing.T) {
	envFile := migratedDatabase(t)

	out, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	assert.Contains(t, out, seeders.UserSeederName+" "+seeders.DefaultUser.Email+" created")
	created, _ := seedTotals(t, out)
	assert.Positive(t, created, "a fresh seed creates the records its registry plans")
	assert.Equal(t, 7, countUsers(t, envFile))
}

// Running the command twice must not create a second account. The second
// run's totals are read off its own report — the registry's answer, not a
// counter a new seed item would have to bump.
func TestMigrateSeedIsIdempotent(t *testing.T) {
	envFile := migratedDatabase(t)

	_, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	second, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	assert.Contains(t, second, seeders.UserSeederName+" "+seeders.DefaultUser.Email+" skipped")
	recreated, reskipped := seedTotals(t, second)
	assert.Zero(t, recreated)
	assert.Positive(t, reskipped, "the second run reports the records it found standing")
	assert.Equal(t, 7, countUsers(t, envFile))
}

// --dry-run must report the work in future tense and write nothing.
func TestMigrateSeedDryRunWritesNothing(t *testing.T) {
	envFile := migratedDatabase(t)

	out, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--dry-run")
	require.NoError(t, err)

	assert.Contains(t, out, seeders.UserSeederName+" "+seeders.DefaultUser.Email+" would create")
	toCreate, _ := dryRunTotals(t, out)
	assert.Positive(t, toCreate, "a dry run plans the work its registry names")
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
// table check would pass here — public.users exists after version 4 — and the
// seed would then run against a schema the migrations have not finished. The
// refusal is asserted before any account exists, so the users table itself is
// the probe.
func TestMigrateSeedRequiresEveryMigration(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force", "--to=4")
	require.NoError(t, err)

	out, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d migrations pending", migrationTotal()-4))
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

	created, _ := seedTotals(t, out)
	assert.Positive(t, created, "an accepted prompt seeds the records its registry plans")
	assert.Equal(t, 7, countUsers(t, envFile))
}

// A seeder that fails must roll the whole run back, so a partial seed is never
// left behind. The command owns the transaction, so this is exercised where the
// transaction is: in the seeders package.

func runMigrateSeedCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	return runMigrateCmd(t, migrateSeedCmd, stdin, args...)
}
