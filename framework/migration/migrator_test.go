package migration

import (
	"database/sql"
	"regexp"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

// migrationsFS is the fixture set the engine tests run over: four migrations,
// each creating one table, each with the Down that drops it. The fixture keeps
// the engine test self-contained — no application schema rides in — while the
// shape (versions, Up/Down blocks) is what goose consumes.
var migrationsFS = fstest.MapFS{
	"00001_create_widget_a.sql": &fstest.MapFile{Data: []byte(
		"-- +goose Up\n-- +goose StatementBegin\nCREATE TABLE IF NOT EXISTS widget_a (id int);\n-- +goose StatementEnd\n-- +goose Down\n-- +goose StatementBegin\nDROP TABLE IF EXISTS widget_a;\n-- +goose StatementEnd\n")},
	"00002_create_widget_b.sql": &fstest.MapFile{Data: []byte(
		"-- +goose Up\n-- +goose StatementBegin\nCREATE TABLE IF NOT EXISTS widget_b (id int);\n-- +goose StatementEnd\n-- +goose Down\n-- +goose StatementBegin\nDROP TABLE IF EXISTS widget_b;\n-- +goose StatementEnd\n")},
	"00003_create_widget_c.sql": &fstest.MapFile{Data: []byte(
		"-- +goose Up\n-- +goose StatementBegin\nCREATE TABLE IF NOT EXISTS widget_c (id int);\n-- +goose StatementEnd\n-- +goose Down\n-- +goose StatementBegin\nDROP TABLE IF EXISTS widget_c;\n-- +goose StatementEnd\n")},
	"00004_create_widget_d.sql": &fstest.MapFile{Data: []byte(
		"-- +goose Up\n-- +goose StatementBegin\nCREATE TABLE IF NOT EXISTS widget_d (id int);\n-- +goose StatementEnd\n-- +goose Down\n-- +goose StatementBegin\nDROP TABLE IF EXISTS widget_d;\n-- +goose StatementEnd\n")},
}

// testSet is the fixture set the engine tests run.
func testSet() Set {
	return Set{Name: "fixture", FS: migrationsFS, VersionTable: "fixture_migration"}
}

// embeddedMigrations loads the fixture set once. Every count and version
// expectation derives from it, so growing the fixture never means editing
// these tests.
var embeddedMigrations = sync.OnceValue(func() []EmbeddedMigration {
	files, err := EmbeddedMigrations(migrationsFS)
	if err != nil {
		panic(err)
	}
	if len(files) == 0 {
		panic("migration: no migrations are compiled into the test binary")
	}
	return files
})

// migrationCount is the number of files compiled into the binary.
func migrationCount() int { return len(embeddedMigrations()) }

// highestVersion is the version of the last embedded migration.
func highestVersion() int64 {
	files := embeddedMigrations()
	return files[len(files)-1].Version
}

func newMigrator(t *testing.T) (*Migrator, *sql.DB) {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	db, err := datastore.OpenMigrationDB(t.Context(),
		datastore.PostgresOptions{DSN: container.NewDatabase(t)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	migrator, err := NewMigrator(t.Context(), db, testSet(), MigratorOptions{})
	require.NoError(t, err)
	return migrator, db
}

func TestNewMigratorRequiresHandle(t *testing.T) {
	_, err := NewMigrator(t.Context(), nil, testSet(), MigratorOptions{})
	require.Error(t, err)
}

// Every embedded file must be picked up. goose silently skips a file whose
// numeric prefix is below 1, which would make a migration never run.
func TestMigratorLoadsEveryEmbeddedFile(t *testing.T) {
	migrator, _ := newMigrator(t)

	assert.Equal(t, highestVersion(), migrator.HighestVersion())
}

func TestMigratorAppliesEveryMigration(t *testing.T) {
	migrator, db := newMigrator(t)
	ctx := t.Context()

	applied, err := migrator.Up(ctx)
	require.NoError(t, err)
	require.Len(t, applied, migrationCount())
	for i, migration := range applied {
		assert.Equal(t, int64(i+1), migration.Version, "migrations must apply in version order")
		assert.NotEmpty(t, migration.Name)
		assert.False(t, migration.Empty)
	}

	version, err := migrator.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, highestVersion(), version)

	// The table the first migration creates must exist, proving version 1 ran.
	assert.True(t, tableExists(t, db, "widget_a"), "migration 00001 must have created its table")

	// Second run is a no-op.
	again, err := migrator.Up(ctx)
	require.NoError(t, err)
	assert.Empty(t, again)
}

func TestMigratorRecordsVersionInAppMigration(t *testing.T) {
	migrator, db := newMigrator(t)
	ctx := t.Context()

	_, err := migrator.Up(ctx)
	require.NoError(t, err)

	var table string
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_name = $1",
		"fixture_migration").Scan(&table))
	assert.Equal(t, "fixture_migration", table)

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM fixture_migration").Scan(&count))
	// One row per applied migration plus goose's version 0 sentinel.
	assert.Equal(t, migrationCount()+1, count)
}

func TestMigratorStatusAndPending(t *testing.T) {
	migrator, _ := newMigrator(t)
	ctx := t.Context()

	statuses, err := migrator.Status(ctx)
	require.NoError(t, err)
	require.Len(t, statuses, migrationCount())
	for _, status := range statuses {
		assert.False(t, status.Applied)
		assert.True(t, status.AppliedAt.IsZero())
	}

	pending, err := migrator.Pending(ctx)
	require.NoError(t, err)
	assert.Len(t, pending, migrationCount())

	_, err = migrator.Up(ctx)
	require.NoError(t, err)

	statuses, err = migrator.Status(ctx)
	require.NoError(t, err)
	for _, status := range statuses {
		assert.True(t, status.Applied)
		assert.False(t, status.AppliedAt.IsZero())
	}

	pending, err = migrator.Pending(ctx)
	require.NoError(t, err)
	assert.Empty(t, pending)
}

func TestMigratorUpToStopsAtVersion(t *testing.T) {
	migrator, db := newMigrator(t)
	ctx := t.Context()

	applied, err := migrator.UpTo(ctx, 3)
	require.NoError(t, err)
	require.Len(t, applied, 3)
	assert.Equal(t, int64(3), applied[len(applied)-1].Version)

	version, err := migrator.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), version)

	// The last migration's table must not exist yet.
	assert.False(t, tableExists(t, db, "widget_d"), "migration 00004 must not have run")

	rest, err := migrator.UpTo(ctx, highestVersion())
	require.NoError(t, err)
	assert.Len(t, rest, migrationCount()-3)
}

func TestMigratorDownRollsBackNewestFirst(t *testing.T) {
	migrator, db := newMigrator(t)
	ctx := t.Context()

	files := embeddedMigrations()
	newest, previous := files[len(files)-1], files[len(files)-2]

	_, err := migrator.Up(ctx)
	require.NoError(t, err)

	rolled, err := migrator.Down(ctx, 2)
	require.NoError(t, err)
	require.Len(t, rolled, 2)
	assert.Equal(t, newest.Version, rolled[0].Version)
	assert.Equal(t, previous.Version, rolled[1].Version)

	version, err := migrator.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, files[len(files)-3].Version, version)

	// Every table the two rolled-back migrations create must be gone, while a
	// table from an older migration survives.
	for _, migration := range []EmbeddedMigration{newest, previous} {
		for _, table := range migrationTables(migration) {
			assert.False(t, tableExists(t, db, table),
				"%s must have rolled back table %s", migration.Name, table)
		}
	}
	assert.True(t, tableExists(t, db, "widget_a"),
		"a table from an older migration must survive the rollback")

	// The applied rows are gone too, so a later up reapplies them.
	pending, err := migrator.Pending(ctx)
	require.NoError(t, err)
	assert.Len(t, pending, 2)
}

// migrationTables lists the table names a migration's SQL creates.
func migrationTables(migration EmbeddedMigration) []string {
	matches := createTableRe.FindAllStringSubmatch(migration.SQL, -1)
	tables := make([]string, 0, len(matches))
	for _, match := range matches {
		tables = append(tables, match[1])
	}
	return tables
}

var createTableRe = regexp.MustCompile(`(?i)CREATE TABLE (?:IF NOT EXISTS )?([a-z_][a-z0-9_]*)`)

// tableExists asks the catalog whether a table is present.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()

	var exists bool
	require.NoError(t, db.QueryRowContext(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)", name).
		Scan(&exists))
	return exists
}

// A count above what is applied rolls back everything and reports only what it
// actually rolled back.
func TestMigratorDownStopsWhenDatabaseIsEmpty(t *testing.T) {
	migrator, _ := newMigrator(t)
	ctx := t.Context()

	_, err := migrator.UpTo(ctx, 2)
	require.NoError(t, err)

	rolled, err := migrator.Down(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, rolled, 2)

	version, err := migrator.Version(ctx)
	require.NoError(t, err)
	assert.Zero(t, version, "the goose sentinel row keeps the version at 0")

	rolled, err = migrator.Down(ctx, 1)
	require.NoError(t, err)
	assert.Empty(t, rolled, "an empty database is not an error")
}

func TestMigratorDownRejectsZeroCount(t *testing.T) {
	migrator, _ := newMigrator(t)

	_, err := migrator.Down(t.Context(), 0)
	require.Error(t, err)
}

func TestMigratorAppliedIsNewestFirst(t *testing.T) {
	migrator, _ := newMigrator(t)
	ctx := t.Context()

	applied, err := migrator.Applied(ctx)
	require.NoError(t, err)
	assert.Empty(t, applied)

	_, err = migrator.UpTo(ctx, 3)
	require.NoError(t, err)

	applied, err = migrator.Applied(ctx)
	require.NoError(t, err)
	require.Len(t, applied, 3)
	assert.Equal(t, int64(3), applied[0].Version)
	assert.Equal(t, int64(1), applied[2].Version)
	for _, status := range applied {
		assert.True(t, status.Applied)
	}
}

// Migrations run on one pinned connection: a multi-statement file would break if
// goose held the advisory lock on a different backend than the statements.
func TestMigratorUsesSingleConnection(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	db, err := datastore.OpenMigrationDB(t.Context(),
		datastore.PostgresOptions{DSN: container.NewDatabase(t)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	assert.Equal(t, 1, db.Stats().MaxOpenConnections)

	migrator, err := NewMigrator(t.Context(), db, testSet(), MigratorOptions{})
	require.NoError(t, err)

	applied, err := migrator.Up(t.Context())
	require.NoError(t, err)
	assert.Len(t, applied, migrationCount())
}

func TestMigratorAllowsOutOfOrderWhenEnabled(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	db, err := datastore.OpenMigrationDB(t.Context(),
		datastore.PostgresOptions{DSN: container.NewDatabase(t)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	migrator, err := NewMigrator(t.Context(), db, testSet(),
		MigratorOptions{AllowOutOfOrder: true})
	require.NoError(t, err)

	applied, err := migrator.Up(t.Context())
	require.NoError(t, err)
	assert.Len(t, applied, migrationCount())
}

// versionTableIDs reads the recorded ids of the version table, lowest first.
func versionTableIDs(t *testing.T, db *sql.DB) []int64 {
	t.Helper()

	rows, err := db.QueryContext(t.Context(),
		"SELECT id FROM fixture_migration ORDER BY id")
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()

	var ids []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

// ResetIdentity must rewind the sequence so the next recorded migration continues
// after the highest id left in the table. goose never moves a sequence
// backwards, so without this the ids grow by one cycle each time and stop
// meaning anything.
func TestMigratorResetIdentityRewindsAfterRollback(t *testing.T) {
	migrator, db := newMigrator(t)

	_, err := migrator.Up(t.Context())
	require.NoError(t, err)

	// A full rollback leaves only the sentinel goose requires.
	_, err = migrator.Down(t.Context(), migrationCount())
	require.NoError(t, err)
	assert.Equal(t, []int64{1}, versionTableIDs(t, db), "only the sentinel row must remain")

	require.NoError(t, migrator.ResetIdentity(t.Context()))

	_, err = migrator.Up(t.Context())
	require.NoError(t, err)

	ids := versionTableIDs(t, db)
	require.Len(t, ids, migrationCount()+1)
	assert.Equal(t, int64(1), ids[0], "the sentinel keeps id 1")
	assert.Equal(t, int64(migrationCount()+1), ids[len(ids)-1],
		"the ids must be dense again, not pushed past the previous cycle")
}

// The sentinel row must survive: goose refuses every command without it.
func TestMigratorResetIdentityKeepsTheZeroVersionRow(t *testing.T) {
	migrator, db := newMigrator(t)

	_, err := migrator.Up(t.Context())
	require.NoError(t, err)
	_, err = migrator.Down(t.Context(), migrationCount())
	require.NoError(t, err)

	require.NoError(t, migrator.ResetIdentity(t.Context()))

	var sentinel int64
	require.NoError(t, db.QueryRowContext(t.Context(),
		"SELECT count(*) FROM fixture_migration WHERE version_id = 0").Scan(&sentinel))
	assert.Equal(t, int64(1), sentinel, "goose requires a row for version 0")

	// goose must still be usable, which is what the sentinel buys.
	_, err = migrator.Status(t.Context())
	require.NoError(t, err)
}

// A rollback that stops part way must leave the sequence at the highest id that
// is still recorded, so the next apply continues from there.
func TestMigratorResetIdentityAfterPartialRollback(t *testing.T) {
	migrator, db := newMigrator(t)

	_, err := migrator.Up(t.Context())
	require.NoError(t, err)
	_, err = migrator.Down(t.Context(), 2)
	require.NoError(t, err)

	require.NoError(t, migrator.ResetIdentity(t.Context()))

	_, err = migrator.Up(t.Context())
	require.NoError(t, err)

	ids := versionTableIDs(t, db)
	require.Len(t, ids, migrationCount()+1)
	assert.Equal(t, int64(migrationCount()+1), ids[len(ids)-1],
		"the ids must stay dense after a partial rollback too")
}
