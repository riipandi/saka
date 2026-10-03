package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/database"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/envfile"
	"github.com/riipandi/saka/pkg/printext"
	"github.com/riipandi/saka/pkg/testutils"
)

// embeddedFiles loads the migrations compiled into the test binary once. Every
// count and version expectation derives from it, so adding a migration file
// never means editing these tests.
var embeddedFiles = sync.OnceValue(func() []database.EmbeddedMigration {
	files, err := database.EmbeddedMigrations()
	if err != nil {
		panic(err)
	}
	if len(files) == 0 {
		panic("cmd: no migrations are compiled into the test binary")
	}
	return files
})

// migrationTotal is the number of migrations compiled into the binary.
func migrationTotal() int { return len(embeddedFiles()) }

// latestMigration is the last migration in version order.
func latestMigration() database.EmbeddedMigration {
	files := embeddedFiles()
	return files[len(files)-1]
}

// migrationTables lists the table names a migration's SQL creates.
func migrationTables(m database.EmbeddedMigration) []string {
	var tables []string
	for _, match := range createTableRe.FindAllStringSubmatch(m.SQL, -1) {
		tables = append(tables, match[1])
	}
	return tables
}

var createTableRe = regexp.MustCompile(`(?i)CREATE TABLE (?:IF NOT EXISTS )?([a-z_][a-z0-9_]*)`)

// writeEnvFile writes a dotenv file containing DATABASE_URL.
func writeEnvFile(t *testing.T, dsn string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".env.local")
	body := envfile.DatabaseURL + "=" + dsn + "\n" +
		"APP_SECRET_KEY=" + testSecret + "\n" +
		"AUTH_SECRET_KEY=" + testSecret + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// configForDeployment is the config file the deployment-command tests
// resolve against: the database and both secret keys, each left to the
// environment, which writeEnvFile fills. initialize seals the signing key
// pair with the auth secret, so both directives must resolve.
func configForDeployment(t *testing.T) {
	t.Helper()

	useConfig(t, `{
		"database": {"url": "env:DATABASE_URL"},
		"app": {"secret_key": "env:APP_SECRET_KEY"},
		"auth": {"secret_key": "env:AUTH_SECRET_KEY"}
	}`)
}

// runMigrateCmd executes a migration command with args and returns stdout.
//
// Every migration command needs a DSN, so the config file names one and the test
// supplies it through DATABASE_URL. A test that needs a data directory passes it
// to runMigrateCmdIn.
func runMigrateCmd(t *testing.T, cmd *cli.Command, stdin string, args ...string) (string, error) {
	t.Helper()
	return runMigrateCmdIn(t, "", cmd, stdin, args...)
}

// runMigrateCmdIn runs a migration command with an explicit data directory.
func runMigrateCmdIn(
	t *testing.T,
	dataDir string,
	cmd *cli.Command,
	stdin string,
	args ...string,
) (string, error) {
	t.Helper()

	configFor(t, dataDir)

	var out bytes.Buffer
	root := testRoot(&out, stdin, cmd)
	err := root.Run(context.Background(), append([]string{"saka", cmd.Name}, args...))
	return out.String(), err
}

// runMigrateUpCmd executes migrate:up with args and returns stdout.
func runMigrateUpCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	return runMigrateCmd(t, migrateUpCmd, stdin, args...)
}

func TestDatabaseURLPrefersEnvFileOverEnvironment(t *testing.T) {
	t.Setenv(envfile.DatabaseURL, "postgres://from-environment/db")

	path := writeEnvFile(t, "postgres://from-env-file/db")

	resolved, err := resolveDatabaseURL(t, path)
	require.NoError(t, err)
	assert.Equal(t, "postgres://from-env-file/db", resolved)
}

func TestDatabaseURLFallsBackToEnvironment(t *testing.T) {
	t.Setenv(envfile.DatabaseURL, "postgres://from-environment/db")

	resolved, err := resolveDatabaseURL(t, "")
	require.NoError(t, err)
	assert.Equal(t, "postgres://from-environment/db", resolved)
}

func TestDatabaseURLMissing(t *testing.T) {
	// The file references env:DATABASE_URL, so an unset variable leaves the key
	// empty. The command reports the variable by name, which is the message a
	// user needs; the unresolved directive is Validate's to report.
	_, err := resolveDatabaseURL(t, "")
	require.ErrorIs(t, err, ErrDatabaseURLUnset)
	assert.Contains(t, err.Error(), "DATABASE_URL")
}

func TestDatabaseURLKeyOmittedFromTheFile(t *testing.T) {
	// A file that says nothing about database.url leaves the key at its empty
	// default, which is the case ErrDatabaseURLUnset reports.
	useConfig(t, `{"log": {"level": "info"}}`)
	t.Setenv(envfile.DatabaseURL, "")

	_, err := resolveDatabaseURL(t, "")
	require.ErrorIs(t, err, ErrDatabaseURLUnset)
}

// resolveDatabaseURL resolves the DSN the way a command does: through the config
// layer, so the env-file flag wins over the environment.
func resolveDatabaseURL(t *testing.T, envFile string) (string, error) {
	t.Helper()

	configFor(t, "")

	var resolved string
	cmd := &cli.Command{
		Name:   "saka",
		Flags:  []cli.Flag{&cli.StringFlag{Name: config.FlagEnvFile}},
		Before: initConfig,
		Action: func(ctx context.Context, _ *cli.Command) error {
			cfg, err := configFrom(ctx)
			if err != nil {
				return err
			}
			resolved, err = requireDatabaseURL(cfg)
			return err
		},
	}
	err := cmd.Run(t.Context(), []string{"saka", "--env-file=" + envFile})
	return resolved, err
}

func TestMigrateUpWithoutDatabaseURL(t *testing.T) {
	out, err := runMigrateUpCmd(t, "")
	require.ErrorIs(t, err, ErrDatabaseURLUnset)
	assert.Empty(t, out)
}

func TestMigrateUpDryRunListsPendingWithoutApplying(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	out, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "initialize_schema")
	assert.Contains(t, out, fmt.Sprintf("%d migrations pending", len(embeddedFiles())))
	assert.NotContains(t, out, "applied")

	// Nothing may have been written.
	db, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	migrator, err := database.NewMigrator(t.Context(), db, database.MigratorOptions{})
	require.NoError(t, err)

	version, err := migrator.Version(t.Context())
	require.NoError(t, err)
	assert.Zero(t, version, "--dry-run must not apply anything")
}

func TestMigrateUpAppliesAndIsIdempotent(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	out, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	assertMigrationRow(t, out, 1, "applied")
	assert.Contains(t, out, fmt.Sprintf("%d migrations applied", len(embeddedFiles())))

	out, err = runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	assert.Contains(t, out, "no pending migrations")
}

func TestMigrateUpStopsAtToVersion(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	out, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force", "--to=2")
	require.NoError(t, err)
	assert.Contains(t, out, "2 migrations applied")
	assert.NotContains(t, out, "00003_")

	out, err = runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("%d migrations applied", migrationTotal()-2))
}

// A non-interactive run (stdin is not a terminal) must not block on the
// confirmation prompt; task db:migrate and CI depend on it.
func TestMigrateUpDoesNotPromptWithoutTerminal(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	out, err := runMigrateUpCmd(t, "", "--env-file="+envFile)
	require.NoError(t, err)
	assert.NotContains(t, out, "[y/N]")
	assert.Contains(t, out, fmt.Sprintf("%d migrations applied", migrationTotal()))
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		name        string
		stdin       string
		args        []string
		interactive bool
		want        bool
	}{
		{name: "force skips prompt", interactive: true, args: []string{"--force"}, want: true},
		{name: "non-interactive applies", interactive: false, want: true},
		{name: "interactive yes", interactive: true, stdin: "y\n", want: true},
		{name: "interactive yes word", interactive: true, stdin: "yes\n", want: true},
		{name: "interactive no", interactive: true, stdin: "n\n", want: false},
		{name: "interactive empty", interactive: true, stdin: "\n", want: false},
		{name: "interactive eof", interactive: true, stdin: "", want: false},
		{name: "interactive other word", interactive: true, stdin: "maybe\n", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			cmd := &cli.Command{
				Writer: &out,
				Reader: strings.NewReader(tt.stdin),
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "force"}},
			}

			var got bool
			var promptErr error
			cmd.Action = func(_ context.Context, cmd *cli.Command) error {
				got, promptErr = confirm(printext.NewPalette(cmd.Root().Writer), cmd,
					tt.interactive, "apply 3 pending migrations?")
				return nil
			}
			require.NoError(t, cmd.Run(t.Context(), append([]string{"saka"}, tt.args...)))
			require.NoError(t, promptErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMigrateDownRollsBackNewestFirst(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err := runMigrateDownCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	assertMigrationRow(t, out, latestMigration().Version, "rolled back")
	assert.Contains(t, out, "1 migration rolled back")

	// Every table the newest migration creates must be gone, while tables from
	// older migrations stay.
	db, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	for _, table := range migrationTables(latestMigration()) {
		var exists bool
		require.NoError(t, db.QueryRowContext(t.Context(),
			"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)", table).
			Scan(&exists))
		assert.False(t, exists, "%s must have been rolled back", table)
	}

	var exists bool
	require.NoError(t, db.QueryRowContext(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'rate_limits')").Scan(&exists))
	assert.True(t, exists, "a table from an older migration must survive")
}

func TestMigrateDownCountAndDryRun(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err := runMigrateDownCmd(t, "", "--env-file="+envFile, "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, migrationLabel(latestMigration().Name))
	assert.Contains(t, out, "1 migration to roll back")

	// --dry-run must not have rolled anything back.
	version := currentVersion(t, dsn)
	assert.Equal(t, latestMigration().Version, version)

	files := embeddedFiles()
	out, err = runMigrateDownCmd(t, "", "--env-file="+envFile, "--force", "--count=3")
	require.NoError(t, err)
	assertMigrationRow(t, out, files[len(files)-3].Version, "rolled back")
	assert.Contains(t, out, "3 migrations rolled back")
	assert.Equal(t, files[len(files)-4].Version, currentVersion(t, dsn))
}

// A count above what the database has rolls back everything instead of failing.
func TestMigrateDownCountAboveApplied(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err := runMigrateDownCmd(t, "", "--env-file="+envFile, "--force", "--count=50")
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("%d migrations rolled back", migrationTotal()))
	assert.Zero(t, currentVersion(t, dsn))
}

func TestMigrateDownRejectsZeroCount(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	out, err := runMigrateDownCmd(t, "", "--env-file="+envFile, "--count=0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "greater than zero")
	assert.Empty(t, out)
}

func TestMigrateDownWithoutAppliedMigrations(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	out, err := runMigrateDownCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	assert.Contains(t, out, "no applied migrations")
}

// A declined rollback must leave the database untouched. The prompt is
// unreachable from a test without a terminal, so the check is stubbed.
func TestMigrateDownDeclinedLeavesDatabase(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = isTerminal })

	out, err := runMigrateDownCmd(t, "n\n", "--env-file="+envFile)
	require.NoError(t, err)
	assert.Contains(t, out, "roll back 1 migration? [y/N]")
	assert.Contains(t, out, "1 migration left applied")
	// The declined rollback leaves the database at its full version: the
	// count the answer refused is one, and the version is the last
	// migration's.
	assert.Equal(t, int64(migrationTotal()), currentVersion(t, dsn))
}

// An accepted rollback applies, proving the prompt gate is not the only path.
func TestMigrateDownAcceptedPrompt(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = isTerminal })

	out, err := runMigrateDownCmd(t, "y\n", "--env-file="+envFile)
	require.NoError(t, err)
	assertMigrationRow(t, out, latestMigration().Version, "rolled back")
	files := embeddedFiles()
	assert.Equal(t, files[len(files)-2].Version, currentVersion(t, dsn))
}

// assertMigrationRow asserts that a migration appears as a full row of the
// shared report shape: version, state, timestamp, name, and duration. Asserting
// the whole row, not just the file name, is what pins the format the three
// migrate:* commands must agree on.
func assertMigrationRow(t *testing.T, out string, version int64, state string) {
	t.Helper()

	prefix := fmt.Sprintf("  %05d %s ", version, state)
	line := rowStartingWith(out, prefix)
	require.NotEmpty(t, line, "no %q row in:\n%s", prefix, out)

	rest := strings.TrimPrefix(line, prefix)
	require.GreaterOrEqual(t, len(rest), len(migrationTimestamp)+1,
		"row must carry a timestamp after the state: %q", line)

	// The timestamp itself holds a space, so it is cut by width rather than by
	// splitting on the separator.
	stamp, tail := rest[:len(migrationTimestamp)], rest[len(migrationTimestamp)+1:]
	_, err := time.Parse(migrationTimestamp, stamp)
	require.NoError(t, err, "the timestamp column must hold a timestamp, got %q", stamp)

	assert.Regexp(t, `^[a-z0-9_]+ \(\d+(\.\d+)? (µs|ms|s)\)$`, tail,
		"the row must end with the short name and a humanized duration: %q", line)
}

// rowStartingWith returns the row that begins with prefix, up to the end of its
// line, or "" when there is none.
//
// The search is not anchored to the start of a line: a confirmed run writes its
// prompt without a trailing newline and relies on the terminal to echo the
// Enter, which a test's stub reader does not do.
func rowStartingWith(out, prefix string) string {
	start := strings.Index(out, prefix)
	if start < 0 {
		return ""
	}
	row := out[start:]
	if end := strings.IndexByte(row, '\n'); end >= 0 {
		row = row[:end]
	}
	return strings.TrimRight(row, "\r")
}

func TestMigrateStatus(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	out, err := runMigrateStatusCmd(t, "--env-file="+envFile)
	require.NoError(t, err)
	assert.Contains(t, out, "00001 pending -                   initialize_schema")
	assert.Contains(t, out, fmt.Sprintf("version 00000; 0 of %d applied", migrationTotal()))
	assert.Contains(t, out, "no migrations applied yet")

	_, err = runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err = runMigrateStatusCmd(t, "--env-file="+envFile)
	require.NoError(t, err)
	assert.Contains(t, out, "00001 applied")
	assert.Contains(t, out, "initialize_schema")
	assert.Contains(t, out, fmt.Sprintf("version %0*d; %d of %d applied",
		database.MigrationPrefixWidth, latestMigration().Version, migrationTotal(), migrationTotal()))
	assert.Contains(t, out, "last run ")
	assert.Contains(t, out, " UTC ("+latestMigration().Name+")")
}

// After a rollback the last run is the migration that ran before it, not the
// one that was just removed.
func TestMigrateStatusReportsLastRunAfterRollback(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	_, err = runMigrateDownCmd(t, "", "--env-file="+envFile, "--force", "--count=2")
	require.NoError(t, err)

	out, err := runMigrateStatusCmd(t, "--env-file="+envFile)
	require.NoError(t, err)
	files := embeddedFiles()
	assert.Contains(t, out, "last run ")
	assert.Contains(t, out, "("+files[len(files)-3].Name+")")
	assert.NotContains(t, out, "("+files[len(files)-2].Name+")")
	assert.NotContains(t, out, "("+files[len(files)-1].Name+")")
}

// The applied time must be a real timestamp, not a zero value.
func TestMigrateStatusShowsAppliedTime(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)

	out, err := runMigrateStatusCmd(t, "--env-file="+envFile)
	require.NoError(t, err)

	// 00001 applied 2026-09-21 04:31:07 initialize_schema
	// The list is indented under the database line, so the marker is not at the
	// start of the line.
	line := ""
	for candidate := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(candidate), "00001 applied ") {
			line = strings.TrimSpace(candidate)
			break
		}
	}
	require.NotEmpty(t, line, "status must list 00001 as applied")

	// The stamp has a space in it, so the date and the time are two fields.
	fields := strings.Fields(line)
	require.GreaterOrEqual(t, len(fields), 4)
	stamp := fields[2] + " " + fields[3]
	_, err = time.ParseInLocation(migrationTimestamp, stamp, time.UTC)
	require.NoError(t, err, "applied time %q must be a timestamp", stamp)
}

func TestMigrateVersion(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := writeEnvFile(t, container.NewDatabase(t))

	out, err := runMigrateVersionCmd(t, "--env-file="+envFile)
	require.NoError(t, err)
	assert.Equal(t, "0\n", out)

	_, err = runMigrateUpCmd(t, "", "--env-file="+envFile, "--force", "--to=4")
	require.NoError(t, err)

	out, err = runMigrateVersionCmd(t, "--env-file="+envFile)
	require.NoError(t, err)
	assert.Equal(t, "4\n", out)
}

// The version command prints a bare number so scripts can consume it.
func TestMigrateVersionWithoutDatabaseURL(t *testing.T) {
	out, err := runMigrateVersionCmd(t, "")
	require.ErrorIs(t, err, ErrDatabaseURLUnset)
	assert.Empty(t, out)
}

func runMigrateDownCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	return runMigrateCmd(t, migrateDownCmd, stdin, args...)
}

func runMigrateStatusCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runMigrateCmd(t, migrateStatusCmd, "", args...)
}

func runMigrateVersionCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runMigrateCmd(t, migrateVersionCmd, "", args...)
}

// currentVersion reads the version through the migrator rather than the raw
// table, so the test cannot pass on a stale row.
func currentVersion(t *testing.T, dsn string) int64 {
	t.Helper()

	db, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	migrator, err := database.NewMigrator(t.Context(), db, database.MigratorOptions{})
	require.NoError(t, err)

	version, err := migrator.Version(t.Context())
	require.NoError(t, err)
	return version
}

// maxMigrationID reads the highest id recorded in the version table.
func maxMigrationID(t *testing.T, dsn string) int64 {
	t.Helper()

	db, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var id int64
	require.NoError(t, db.QueryRowContext(t.Context(),
		"SELECT coalesce(max(id), 0) FROM app_migration").Scan(&id))
	return id
}

// A full rollback must rewind the version table's identity sequence, so applying
// again records the same ids instead of starting past the previous cycle.
func TestMigrateDownRewindsTheVersionTableIdentity(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)

	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	afterUp := maxMigrationID(t, dsn)

	_, err = runMigrateDownCmd(t, "", "--env-file="+envFile, "--force", "--count="+fmt.Sprint(migrationTotal()))
	require.NoError(t, err)
	assert.Equal(t, int64(1), maxMigrationID(t, dsn), "only the sentinel must remain")

	_, err = runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	assert.Equal(t, afterUp, maxMigrationID(t, dsn),
		"a second full apply must record the same ids as the first")
}
