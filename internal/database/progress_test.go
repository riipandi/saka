package database_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/database"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

// newProgressMigrator returns a migrator that records every progress event on a
// fresh database.
func newProgressMigrator(t *testing.T) (*database.Migrator, *[]database.ProgressEvent) {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	db, err := datastore.OpenMigrationDB(t.Context(),
		datastore.PostgresOptions{DSN: container.NewDatabase(t)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var events []database.ProgressEvent
	migrator, err := database.NewMigrator(t.Context(), db, database.MigratorOptions{
		Progress: func(event database.ProgressEvent) { events = append(events, event) },
	})
	require.NoError(t, err)
	return migrator, &events
}

// Progress events are what the CLI draws its live lines from, so a run must
// report one finished event per migration.
func TestMigratorReportsProgressPerMigration(t *testing.T) {
	migrator, events := newProgressMigrator(t)

	results, err := migrator.Up(t.Context())
	require.NoError(t, err)
	require.Len(t, results, migrationCount())

	// Every migration reports a start and one finished state, so the count is
	// exactly twice the number of migrations.
	started := make(map[int64]bool)
	finished := make(map[int64]database.ProgressEvent)
	for _, event := range *events {
		if event.State == database.ProgressStarted {
			assert.False(t, started[event.Version], "version %d started twice", event.Version)
			started[event.Version] = true
			continue
		}
		finished[event.Version] = event
	}

	assert.Len(t, started, migrationCount(), "every migration must report a start")
	assert.Len(t, finished, migrationCount(), "every migration must report a finished state")

	for version, event := range finished {
		assert.Equal(t, database.ProgressApplied, event.State, "version %d", version)
		assert.Equal(t, "up", event.Direction)
		assert.NotEmpty(t, event.Name)
		assert.Positive(t, event.Duration)
	}
}

// A rollback must be reported as such, so the report does not claim a migration
// was applied when it was removed.
func TestMigratorReportsRollbackDirection(t *testing.T) {
	migrator, events := newProgressMigrator(t)

	_, err := migrator.Up(t.Context())
	require.NoError(t, err)

	*events = nil
	rolled, err := migrator.Down(t.Context(), 2)
	require.NoError(t, err)
	require.Len(t, rolled, 2)

	var finished []database.ProgressEvent
	for _, event := range *events {
		if event.State != database.ProgressStarted {
			finished = append(finished, event)
		}
	}

	require.Len(t, finished, 2)
	for _, event := range finished {
		assert.Equal(t, database.ProgressRolledBack, event.State)
		assert.Equal(t, "down", event.Direction)
	}
}

// Without a Progress callback the migrator must stay silent: goose's default
// logger would otherwise print its own report next to the command's.
func TestMigratorWithoutProgressStaysSilent(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	db, err := datastore.OpenMigrationDB(t.Context(),
		datastore.PostgresOptions{DSN: container.NewDatabase(t)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	migrator, err := database.NewMigrator(t.Context(), db, database.MigratorOptions{})
	require.NoError(t, err)

	results, err := migrator.Up(t.Context())
	require.NoError(t, err)
	assert.Len(t, results, migrationCount())
}
