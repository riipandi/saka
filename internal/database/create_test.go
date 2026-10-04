//go:build debug

package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database"
	"github.com/riipandi/saka/pkg/testutils"
)

// migrationDir returns a temp directory holding the given files, named exactly
// as passed so a test can set up the versions it cares about.
func migrationDir(t *testing.T, files ...string) string {
	t.Helper()

	dir := t.TempDir()
	for _, name := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("-- +goose Up\n"), 0o600))
	}
	return dir
}

func TestCreateMigrationNumbersFromHighestVersion(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		want     string
	}{
		{name: "empty directory starts at 1", want: "00001_add_widgets.sql"},
		{name: "continues the sequence", existing: []string{"00001_a.sql", "00002_b.sql"}, want: "00003_add_widgets.sql"},
		// A gap must not be reused: two files would claim different versions
		// that goose orders by version, not by name.
		{name: "counts from the highest, not the count", existing: []string{"00001_a.sql", "00003_c.sql"}, want: "00004_add_widgets.sql"},
		{name: "ignores files goose cannot parse", existing: []string{"00001_a.sql", "helper.sql"}, want: "00002_add_widgets.sql"},
		{name: "pads the version to five digits", existing: []string{"00009_a.sql"}, want: "00010_add_widgets.sql"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := migrationDir(t, tt.existing...)

			created, err := database.CreateMigration(database.CreateOptions{Dir: dir, Name: "add widgets"})
			require.NoError(t, err)

			assert.Equal(t, tt.want, filepath.Base(created.Path))
			assert.FileExists(t, created.Path)
		})
	}
}

func TestCreateMigrationNormalizesName(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "spaces become underscores", raw: "add widgets", want: "add_widgets"},
		{name: "camel case is split on case change", raw: "addWidgets", want: "addwidgets"},
		{name: "dashes and dots collapse", raw: "add.widgets-v2", want: "add_widgets_v2"},
		{name: "runs of separators collapse to one", raw: "add  --  widgets", want: "add_widgets"},
		{name: "surrounding separators are dropped", raw: "__add_widgets__", want: "add_widgets"},
		{name: "existing snake case is kept", raw: "add_widgets", want: "add_widgets"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created, err := database.CreateMigration(
				database.CreateOptions{Dir: migrationDir(t), Name: tt.raw})
			require.NoError(t, err)
			assert.Equal(t, tt.want, created.Name)
			assert.Equal(t, "00001_"+tt.want+".sql", filepath.Base(created.Path))
		})
	}
}

// A name is checked against every name already in use, whatever version it
// carries, so the directory never holds two files describing the same change.
func TestCreateMigrationRejectsTakenName(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		raw      string
	}{
		{name: "same name, older version", existing: []string{"00001_add_widgets.sql"}, raw: "add_widgets"},
		{name: "same name, newer version", existing: []string{"00007_add_widgets.sql"}, raw: "add_widgets"},
		{name: "different separator style", existing: []string{"00001_add_widgets.sql"}, raw: "Add Widgets"},
		{name: "casing differs", existing: []string{"00001_add_widgets.sql"}, raw: "ADD_WIDGETS"},
		{name: "name is used by an unparsable file", existing: []string{"add_widgets.sql"}, raw: "add_widgets"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := migrationDir(t, tt.existing...)

			_, err := database.CreateMigration(database.CreateOptions{Dir: dir, Name: tt.raw})
			require.ErrorIs(t, err, database.ErrMigrationNameTaken)
			assert.Contains(t, err.Error(), "add_widgets")

			// The message must name the file that holds the name, so the
			// caller can find it without searching.
			assert.Contains(t, err.Error(), tt.existing[0])

			entries, readErr := os.ReadDir(dir)
			require.NoError(t, readErr)
			assert.Len(t, entries, len(tt.existing), "a rejected create must write nothing")
		})
	}
}

func TestCreateMigrationRejectsEmptyName(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "spaces only", raw: "   "},
		{name: "separators only", raw: "---___..."},
		{name: "punctuation only", raw: "!!!"},
		{name: "non-ascii only", raw: "日本語"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := migrationDir(t)

			_, err := database.CreateMigration(database.CreateOptions{Dir: dir, Name: tt.raw})
			require.ErrorIs(t, err, database.ErrInvalidMigrationName)

			entries, readErr := os.ReadDir(dir)
			require.NoError(t, readErr)
			assert.Empty(t, entries)
		})
	}
}

func TestCreateMigrationRequiresExistingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")

	_, err := database.CreateMigration(database.CreateOptions{Dir: missing, Name: "add_widgets"})
	require.ErrorIs(t, err, database.ErrMigrationsDirMissing)
	assert.Contains(t, err.Error(), missing)
	assert.NoDirExists(t, missing, "the command must not create directories")
}

// The skeleton must satisfy the same checks migrate:validate runs, otherwise a
// freshly created migration would fail `task check` before anything is added.
func TestCreateMigrationTemplateIsValid(t *testing.T) {
	created, err := database.CreateMigration(
		database.CreateOptions{Dir: migrationDir(t), Name: "add_widgets"})
	require.NoError(t, err)

	report := database.ValidateFS(os.DirFS(filepath.Dir(created.Path)))
	require.True(t, report.OK(), "generated migration must validate: %v", report.Issues)
	assert.Equal(t, 1, report.Checked)
}

// The skeleton must be accepted by goose itself, which is stricter than the
// static checks: it rejects a missing Down block and a half-open statement.
func TestCreateMigrationTemplateIsAcceptedByGoose(t *testing.T) {
	dir := migrationDir(t)
	created, err := database.CreateMigration(database.CreateOptions{Dir: dir, Name: "add_widgets"})
	require.NoError(t, err)

	provider, err := goose.NewProvider(goose.DialectPostgres, &sql.DB{}, os.DirFS(dir))
	require.NoError(t, err)

	sources := provider.ListSources()
	require.Len(t, sources, 1)
	assert.Equal(t, created.Version, sources[0].Version)
	assert.Equal(t, filepath.Base(created.Path), filepath.Base(sources[0].Path))
}

// A generated migration must run against a real database, in both directions.
// Empty blocks are reported as "empty" rather than "applied", which is what
// migrate:up prints until statements are added.
func TestCreateMigrationRunsOnPostgres(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	db, err := datastore.OpenMigrationDB(t.Context(),
		datastore.PostgresOptions{DSN: container.NewDatabase(t)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	dir := migrationDir(t)
	created, err := database.CreateMigration(database.CreateOptions{Dir: dir, Name: "add_widgets"})
	require.NoError(t, err)

	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir))
	require.NoError(t, err)

	ctx := context.Background()
	up, err := provider.Up(ctx)
	require.NoError(t, err)
	require.Len(t, up, 1)
	assert.True(t, up[0].Empty, "an unfilled skeleton has no statements")
	assert.Equal(t, filepath.Base(created.Path), filepath.Base(up[0].Source.Path))

	version, err := provider.GetDBVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, created.Version, version)

	down, err := provider.Down(ctx)
	require.NoError(t, err)
	require.NotNil(t, down)
	assert.Equal(t, filepath.Base(created.Path), filepath.Base(down.Source.Path))

	version, err = provider.GetDBVersion(ctx)
	require.NoError(t, err)
	assert.Zero(t, version)
}

// repoPath resolves a module-root-relative path from a test's working
// directory, which is the package directory.
func repoPath(t *testing.T, rel string) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel)
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "go.mod not found above %s", rel)
		dir = parent
	}
}

// The default target must be the directory the binary embeds, otherwise a
// created file would never be compiled in.
func TestCreateMigrationDefaultDirectory(t *testing.T) {
	assert.Equal(t, "database/migrations", database.MigrationsPath)

	info, err := os.Stat(repoPath(t, database.MigrationsPath))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// The embedded set must stay in sync with the files on disk: a migration
// created after a build is invisible to migrate:up until the binary is rebuilt,
// which is the failure the docs warn about.
func TestEmbeddedMigrationsMatchDisk(t *testing.T) {
	entries, err := os.ReadDir(repoPath(t, database.MigrationsPath))
	require.NoError(t, err)

	report := database.Validate()
	require.True(t, report.OK(), "embedded migrations must validate: %v", report.Issues)
	assert.Equal(t, len(entries), report.Checked)
}

// A directory with no migrations yet still gets version 1, not a skipped slot.
func TestCreateMigrationInEmptyDirectoryUsesFirstVersion(t *testing.T) {
	dir := t.TempDir()

	created, err := database.CreateMigration(database.CreateOptions{Dir: dir, Name: "bootstrap"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), created.Version)
	assert.Equal(t, "00001_bootstrap.sql", filepath.Base(created.Path))
}

// The generated file is written with the repository's mode, and never
// executable, so a diff does not record a mode change.
func TestCreateMigrationWritesPlainFile(t *testing.T) {
	created, err := database.CreateMigration(
		database.CreateOptions{Dir: migrationDir(t), Name: "add_widgets"})
	require.NoError(t, err)

	info, err := os.Stat(created.Path)
	require.NoError(t, err)
	assert.False(t, info.Mode().Perm()&0o111 != 0, "migration must not be executable")

	content, err := os.ReadFile(created.Path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "-- +goose Up")
	assert.Contains(t, string(content), "-- +goose Down")
}

// ValidateFS is the check the template must survive, so prove it fails on a
// skeleton missing its Down block. Without this, the template test could pass
// for the wrong reason.
func TestValidateFSRejectsMissingDownBlock(t *testing.T) {
	report := database.ValidateFS(fstest.MapFS{
		"00001_broken.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n")},
	})
	require.False(t, report.OK())
	assert.Contains(t, report.Issues[0].Message, "missing '-- +goose Down'")
}
