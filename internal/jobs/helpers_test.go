package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/queue"
)

// migratedClient applies the migrations to a fresh test database and returns
// the pool and a client on it, without starting the dispatcher.
func migratedClient(t *testing.T, dsn string) (*datastore.Postgres, *queue.Client) {
	t.Helper()

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := openTestMigrators(t, migrationDB)
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "queue_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })

	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		NumWorkers:   2,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)
	return pool, client
}
