//go:build debug

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/database"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/envfile"
	"github.com/riipandi/saka/pkg/testutils"
)

// migrate:validate must not need a database, so it works before one exists.
func TestMigrateValidateNeedsNoDatabase(t *testing.T) {
	t.Setenv(envfile.DatabaseURL, "")
	t.Setenv("HOME", t.TempDir())

	out, err := runMigrateValidateCmd(t)
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("status: %d migration files valid", migrationTotal()))
}

func TestMigrateValidateReportsNoIssues(t *testing.T) {
	out, err := runMigrateValidateCmd(t)
	require.NoError(t, err)
	assert.NotContains(t, out, "goose will skip")
	assert.Contains(t, out, fmt.Sprintf("status: %d migration files valid", migrationTotal()))
}

// A broken file must fail the command, so `task check` fails with it.
func TestMigrateValidateReportsIssuesAndFails(t *testing.T) {
	migrationCheck = func() database.ValidationReport {
		return database.ValidationReport{
			Checked: 1,
			Issues: []database.ValidationIssue{
				{File: "00001_x.sql", Line: 3, Message: "boom"},
			},
		}
	}
	t.Cleanup(func() { migrationCheck = database.Validate })

	out, err := runMigrateValidateCmd(t)
	require.Error(t, err)
	assert.Contains(t, out, "00001_x.sql:3: boom")
	assert.Contains(t, err.Error(), "1 problem in 1 migration file")
}

func TestMigrateResetRollsBackEverything(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err := runMigrateResetCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	assert.Contains(t, out, "11 migrations rolled back")
	assert.Zero(t, currentVersion(t, dsn))
}

// --up rebuilds the schema in one command.
func TestMigrateResetWithUpReappliesEverything(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err := runMigrateResetCmd(t, "", "--env-file="+envFile, "--force", "--up")
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("%d migrations rolled back", migrationTotal()))
	assert.Contains(t, out, fmt.Sprintf("%d migrations applied", migrationTotal()))
	assert.Equal(t, latestMigration().Version, currentVersion(t, dsn))

	// The two halves must each use their own state column and their own clock.
	// The migrator holds the reporter's progress callback, so a half that
	// replaced the reporter instead of restarting it would keep the other half's
	// width and include the other half's time.
	assertMigrationRow(t, out, latestMigration().Version, "rolled back")
	assertMigrationRow(t, out, 1, "applied")

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "applied ") && strings.Contains(line, "00001_initialize") {
			assert.Contains(t, line, "  00001 applied 2", "the up half must use its own column width: %q", line)
		}
	}
}

func TestMigrateResetDryRunChangesNothing(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force", "--to=3")
	require.NoError(t, err)

	out, err := runMigrateResetCmd(t, "", "--env-file="+envFile, "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "create_multifactor_tables")
	assert.Contains(t, out, "3 migrations to roll back")
	assert.Equal(t, int64(3), currentVersion(t, dsn))

	// With --up the pending half is listed too, still without touching anything.
	out, err = runMigrateResetCmd(t, "", "--env-file="+envFile, "--dry-run", "--up")
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("%d migrations pending", migrationTotal()-3))
	assert.Equal(t, int64(3), currentVersion(t, dsn))
}

func TestMigrateResetWithoutAppliedMigrations(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	out, err := runMigrateResetCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	assert.Contains(t, out, "no applied migrations")
}

// On a fresh database --up alone applies the migrations, so `reset --up` is
// also the way to build a schema from nothing.
func TestMigrateResetWithUpOnFreshDatabaseApplies(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	out, err := runMigrateResetCmd(t, "", "--env-file="+envFile, "--force", "--up")
	require.NoError(t, err)
	assert.NotContains(t, out, "no applied migrations")
	assert.Contains(t, out, "11 migrations applied")
	assert.Equal(t, int64(11), currentVersion(t, dsn))

	// This path applies without rolling back first, so the rows must use the
	// apply column width, not the rollback width the reporter was built with.
	assertMigrationRow(t, out, 1, "applied")
}

// A fresh database has nothing to roll back, so only the up half is asked
// about. The question matches migrate:up, because the work is the same.
func TestMigrateResetWithUpOnFreshDatabasePromptsToApply(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = isTerminal })

	out, err := runMigrateResetCmd(t, "n\n", "--env-file="+envFile, "--up")
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("apply all %d pending migrations?", migrationTotal()))
	assert.NotContains(t, out, "roll back")
	assert.Contains(t, out, fmt.Sprintf("%d pending migrations left unapplied", migrationTotal()))
	assert.Zero(t, currentVersion(t, dsn))
}

// --dry-run on a fresh database lists the up half and changes nothing.
func TestMigrateResetDryRunOnFreshDatabase(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	out, err := runMigrateResetCmd(t, "", "--env-file="+envFile, "--dry-run", "--up")
	require.NoError(t, err)
	assert.NotContains(t, out, "to roll back")
	assert.Contains(t, out, "11 migrations pending")
	assert.Zero(t, currentVersion(t, dsn))

	// Without --up there is nothing to report at all.
	out, err = runMigrateResetCmd(t, "", "--env-file="+envFile, "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "no applied migrations")
	assert.Zero(t, currentVersion(t, dsn))
}

func TestMigrateResetDeclinedLeavesDatabase(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = isTerminal })

	out, err := runMigrateResetCmd(t, "n\n", "--env-file="+envFile)
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("roll back all %d migrations?", migrationTotal()))
	assert.Contains(t, out, fmt.Sprintf("%d migrations left applied", migrationTotal()))
	assert.Equal(t, latestMigration().Version, currentVersion(t, dsn))
}

func runMigrateResetCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	return runMigrateCmd(t, migrateResetCmd, stdin, args...)
}

// --seed writes rows onto the schema the re-apply builds, so the pair without
// --up is refused rather than half-served.
func TestMigrateResetSeedNeedsUp(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	_, err = runMigrateResetCmd(t, "", "--env-file="+envFile, "--force", "--seed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--seed")
	assert.Contains(t, err.Error(), "--up")
}

// --up --seed rebuilds the schema and fills it in one run, through the same
// seeding body migrate:seed serves.
func TestMigrateResetWithUpAndSeedReappliesAndSeeds(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err := runMigrateResetCmd(t, "", "--env-file="+envFile, "--force", "--up", "--seed")
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("%d migrations rolled back", migrationTotal()))
	assert.Contains(t, out, fmt.Sprintf("%d migrations applied", migrationTotal()))
	assert.Contains(t, out, "created", "the seed half must report the records it wrote")
	assert.Greater(t, seededAccounts(t, dsn), int64(0),
		"the seed half must leave the default account behind")
}

// The dry run lists the seed half without running it.
func TestMigrateResetDryRunWithSeedReportsTheSeedHalf(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err := runMigrateResetCmd(t, "", "--env-file="+envFile, "--dry-run", "--up", "--seed")
	require.NoError(t, err)
	assert.Contains(t, out, "would seed the database")
	assert.Zero(t, seededAccounts(t, dsn), "a dry run must not write any rows")
}

// On a fresh database the seed half follows the plain apply.
func TestMigrateResetWithUpAndSeedOnFreshDatabase(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	out, err := runMigrateResetCmd(t, "", "--env-file="+envFile, "--force", "--up", "--seed")
	require.NoError(t, err)
	assert.NotContains(t, out, "no applied migrations")
	assert.Contains(t, out, "created")
	assert.Greater(t, seededAccounts(t, dsn), int64(0))
}

// A declined apply leaves the schema unbuilt, so the seed half must not run.
func TestMigrateResetWithSeedDeclinedAppliesNothing(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = isTerminal })

	out, err := runMigrateResetCmd(t, "n\n", "--env-file="+envFile, "--up", "--seed")
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("%d pending migrations left unapplied", migrationTotal()))
	assert.NotContains(t, out, "created")
	assert.False(t, tableExists(t, dsn, "users"),
		"a declined apply must not leave a schema the seed half could write into")
}

// seededAccounts counts the rows the seeders wrote into the accounts table.
func seededAccounts(t *testing.T, dsn string) int64 {
	t.Helper()

	db, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var count int64
	require.NoError(t, db.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public.users").Scan(&count))
	return count
}

// tableExists reports whether the schema holds the named table, so a test can
// assert the schema was never built rather than that it is empty.
func tableExists(t *testing.T, dsn, table string) bool {
	t.Helper()

	db, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var exists bool
	require.NoError(t, db.QueryRowContext(t.Context(),
		"SELECT exists (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)",
		table).Scan(&exists))
	return exists
}

// migrate:create must need no database, so a schema can be started before
// Postgres exists.
func TestMigrateCreateNeedsNoDatabase(t *testing.T) {
	t.Setenv(envfile.DatabaseURL, "")
	t.Setenv("HOME", t.TempDir())

	out, err := runMigrateCreateCmd(t, t.TempDir(), "add widgets")
	require.NoError(t, err)
	assert.Contains(t, out, "00001_add_widgets.sql created (version 00001)")
}

func TestMigrateCreateReportsPathAndVersion(t *testing.T) {
	dir := t.TempDir()

	out, err := runMigrateCreateCmd(t, dir, "add widgets")
	require.NoError(t, err)

	path := filepath.Join(dir, "00001_add_widgets.sql")
	assert.Contains(t, out, path+" created (version 00001)")
	assert.FileExists(t, path)
}

// A second create must take the next version, not reuse the first.
func TestMigrateCreateContinuesSequence(t *testing.T) {
	dir := t.TempDir()

	_, err := runMigrateCreateCmd(t, dir, "add widgets")
	require.NoError(t, err)
	out, err := runMigrateCreateCmd(t, dir, "drop widgets")
	require.NoError(t, err)

	assert.Contains(t, out, "00002_drop_widgets.sql created (version 00002)")
}

// A name conflict must fail the command, so a scripted create cannot silently
// leave a second file describing the same change.
func TestMigrateCreateFailsOnNameConflict(t *testing.T) {
	dir := t.TempDir()

	_, err := runMigrateCreateCmd(t, dir, "add widgets")
	require.NoError(t, err)

	out, err := runMigrateCreateCmd(t, dir, "Add Widgets")
	require.ErrorIs(t, err, database.ErrMigrationNameTaken)
	assert.Empty(t, out, "a failed create prints nothing to stdout")

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Len(t, entries, 1)
}

func TestMigrateCreateRejectsUnusableName(t *testing.T) {
	dir := t.TempDir()

	out, err := runMigrateCreateCmd(t, dir, "...")
	require.ErrorIs(t, err, database.ErrInvalidMigrationName)
	assert.Empty(t, out)

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestMigrateCreateFailsWhenDirectoryMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")

	out, err := runMigrateCreateCmd(t, missing, "add widgets")
	require.ErrorIs(t, err, database.ErrMigrationsDirMissing)
	assert.Empty(t, out)
	assert.NoDirExists(t, missing)
}

// The command writes into database/migrations unless --dir says otherwise,
// which is the directory the binary embeds.
func TestMigrateCreateDefaultsToEmbeddedDirectory(t *testing.T) {
	assert.Equal(t, database.MigrationsPath, migrateCreateCmd.Flags[0].(*cli.StringFlag).Value)
}

func runMigrateCreateCmd(t *testing.T, dir, name string) (string, error) {
	t.Helper()
	return runMigrateCmd(t, migrateCreateCmd, "", "--dir="+dir, name)
}

func runMigrateValidateCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runMigrateCmd(t, migrateValidateCmd, "", args...)
}

// migrate:reset --up rolls back and re-applies in one run, so its ids must stay
// dense across the two halves.
func TestMigrateResetWithUpKeepsIDsDense(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	afterUp := maxMigrationID(t, dsn)

	_, err = runMigrateResetCmd(t, "", "--env-file="+envFile, "--force", "--up")
	require.NoError(t, err)
	assert.Equal(t, afterUp, maxMigrationID(t, dsn),
		"the re-apply must reuse the ids, not continue past them")
	assert.Equal(t, latestMigration().Version, currentVersion(t, dsn))
}
