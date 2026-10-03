package database_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/database"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

// Shared helpers for the dump tests. They build real databases, because the
// thing under test is the contract with Postgres: which objects a dump finds
// and whether the bytes it writes load back.

// migratedPoolDSN returns only the DSN of a fresh migrated database, for a test
// that does not need the pool itself.
func migratedPoolDSN(t *testing.T) string {
	t.Helper()
	_, dsn := migratedPool(t)
	return dsn
}

// writeZipWithTwoEntries writes a zip holding two files, which a dump archive
// must never be.
func writeZipWithTwoEntries(t *testing.T, path string) {
	t.Helper()

	file, err := os.Create(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	writer := zip.NewWriter(file)
	for _, name := range []string{"one.sql", "two.sql"} {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte("-- " + name + "\n"))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
}

// assertZipPayloadEqual compares the single entry of two zip archives by
// content. The entry carries a modification time, so the archives are not
// byte-identical even when the payload is.
func assertZipPayloadEqual(t *testing.T, a, b []byte) {
	t.Helper()

	read := func(raw []byte) []byte {
		reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		require.NoError(t, err)
		require.Len(t, reader.File, 1)

		entry, err := reader.File[0].Open()
		require.NoError(t, err)
		defer func() { _ = entry.Close() }()

		content, err := io.ReadAll(entry)
		require.NoError(t, err)
		return content
	}

	assert.Equal(t, read(a), read(b), "the payloads must match")
}

// migratedPool returns a pool over a fresh database with every migration
// applied, which is the state an export starts from and a data-only import
// expects.
func migratedPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(),
		datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)

	migrator, err := database.NewMigrator(t.Context(), migrationDB, database.MigratorOptions{})
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := pgxpool.New(t.Context(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool, dsn
}

// dumper builds the datastore handle the exporter and restorer take.
func dumper(t *testing.T, dsn string) *datastore.Postgres {
	t.Helper()
	store, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { store.Shutdown(context.Background()) })
	return store
}

// seedRows inserts one user and one password row, so a dump has something to
// carry across the three schemas.
func seedRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, first_name, last_name, display_name)
		VALUES ('01890000-0000-7000-8000-000000000001', 'alice', 'alice@example.com',
		        'Alice', 'A', 'Alice A')`)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.user_passwords (user_id, password_hash)
		VALUES ('01890000-0000-7000-8000-000000000001', '$scrypt$test')`)
	require.NoError(t, err)
}

// dump runs an export and returns the file contents.
func dump(t *testing.T, store *datastore.Postgres, opts database.DumpOptions) string {
	t.Helper()
	var out bytes.Buffer
	_, err := database.NewExporter(store, opts).Dump(t.Context(), &out, nil)
	require.NoError(t, err)
	return out.String()
}
