package database_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fwaudit "github.com/riipandi/saka/framework/audit"
	fwqueue "github.com/riipandi/saka/framework/queue"
	fwscheduler "github.com/riipandi/saka/framework/scheduler"
	"github.com/riipandi/saka/internal/database"
	"github.com/riipandi/saka/pkg/testutils"
)

// A data-only dump carries rows and no DDL, and loading it into another
// migrated database reproduces them.
func TestDataOnlyDumpRoundTrips(t *testing.T) {
	source, sourceDSN := migratedPool(t)
	seedRows(t, source)

	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{DataOnly: true})

	assert.Contains(t, content, `COPY "public"."users"("id", "username"`)
	assert.NotContains(t, content, "CREATE TABLE", "a data-only dump must carry no DDL")

	_, targetDSN := migratedPool(t)
	target := dumper(t, targetDSN)

	var seen []database.TableProgress
	stats, err := database.NewRestorer(target, false).
		Restore(t.Context(), strings.NewReader(content),
			func(p database.TableProgress) { seen = append(seen, p) })
	require.NoError(t, err)

	assert.Positive(t, stats.Rows)
	assert.Positive(t, stats.Tables)
	assert.NotEmpty(t, seen, "the restorer must report every table it loads")

	targetPool, err := pgxpool.New(t.Context(), targetDSN)
	require.NoError(t, err)
	t.Cleanup(targetPool.Close)

	var email, displayName string
	err = targetPool.QueryRow(t.Context(),
		`SELECT email, display_name FROM public.users WHERE username = 'alice'`).
		Scan(&email, &displayName)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", email)
	assert.Equal(t, "Alice A", displayName)

	var hash string
	err = targetPool.QueryRow(t.Context(),
		`SELECT password_hash FROM public.user_passwords`).Scan(&hash)
	require.NoError(t, err)
	assert.Equal(t, "$scrypt$test", hash)
}

// A schema dump carries the DDL and can rebuild the schema on an empty
// database, which is what makes it a backup rather than a data transfer.
func TestSchemaOnlyDumpRebuildsTheSchema(t *testing.T) {
	source, sourceDSN := migratedPool(t)
	seedRows(t, source)

	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{SchemaOnly: true})

	for _, expected := range []string{
		"CREATE SCHEMA IF NOT EXISTS",
		`CREATE EXTENSION IF NOT EXISTS "citext";`,
		"CREATE TABLE IF NOT EXISTS",
		"CREATE INDEX IF NOT EXISTS",
		"CREATE OR REPLACE FUNCTION",
		"CREATE OR REPLACE TRIGGER",
		// Neither of these has an IF NOT EXISTS form, so each is guarded by a
		// catalog check instead.
		"IF NOT EXISTS (\n    SELECT 1 FROM pg_type t",
		"IF NOT EXISTS (\n    SELECT 1 FROM pg_constraint con",
	} {
		assert.Contains(t, content, expected)
	}
	assert.NotContains(t, content, "FROM stdin", "a schema-only dump must carry no rows")

	// The DDL must apply to an empty database. This is the check that catches a
	// statement written in the wrong order.
	container := testutils.StartPostgres(t.Context(), t)
	emptyDSN := container.NewDatabase(t)
	empty := dumper(t, emptyDSN)

	_, err := empty.Exec(t.Context(), content)
	require.NoError(t, err, "the dumped DDL must apply cleanly")

	pool, err := pgxpool.New(t.Context(), emptyDSN)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// The restored database must carry exactly the tables the migrated source
	// has, whatever that set is. The dump leaves every set's version table
	// out — the target's migrator owns them — so they are filtered here too.
	restored := applicationTables(t, pool)
	require.NotEmpty(t, restored, "every application table must be recreated")
	expected := slices.DeleteFunc(applicationTables(t, source), func(name string) bool {
		switch name {
		case strings.TrimPrefix(database.VersionTable, "public."),
			strings.TrimPrefix(fwqueue.TableQueueMigrations, "public."),
			strings.TrimPrefix(fwscheduler.TableSchedulerMigrations, "public."),
			strings.TrimPrefix(fwaudit.TableAuditMigrations, "public."):
			return true
		}
		return false
	})
	assert.Equal(t, expected, restored)
}

// applicationTables lists the relation names the dump must carry over.
func applicationTables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()

	rows, err := pool.Query(t.Context(), `
		SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname IN ('public', 'internal', 'reference')
		ORDER BY c.relname`)
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, rows.Err())
	return names
}

// Two dumps of the same state must be byte-identical apart from the header
// timestamp, so a dump can be diffed in review.
func TestDumpIsStableAcrossRuns(t *testing.T) {
	source, sourceDSN := migratedPool(t)
	seedRows(t, source)
	store := dumper(t, sourceDSN)

	first := dump(t, store, database.DumpOptions{})
	second := dump(t, store, database.DumpOptions{})

	assert.Equal(t, stripHeader(first), stripHeader(second))
}

// stripHeader removes the lines that legitimately differ between two runs.
func stripHeader(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "-- generated:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// Values holding the characters that break a naive dump must survive the round
// trip, including a value that is the COPY terminator.
func TestDumpPreservesAwkwardValues(t *testing.T) {
	source, sourceDSN := migratedPool(t)

	awkward := "tab\there\nnewline\\backslash \\. terminator 'quote' \"double\" ünïcode"
	_, err := source.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, first_name, last_name, display_name)
		VALUES ('01890000-0000-7000-8000-000000000002', 'awkward', 'awkward@example.com',
		        $1, 'x', 'Awkward X')`, awkward)
	require.NoError(t, err)

	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{DataOnly: true})

	_, targetDSN := migratedPool(t)
	_, err = database.NewRestorer(dumper(t, targetDSN), false).
		Restore(t.Context(), strings.NewReader(content), nil)
	require.NoError(t, err)

	pool, err := pgxpool.New(t.Context(), targetDSN)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var firstName string
	err = pool.QueryRow(t.Context(),
		`SELECT first_name FROM public.users WHERE username = 'awkward'`).Scan(&firstName)
	require.NoError(t, err)
	assert.Equal(t, awkward, firstName)
}

// A restore into a populated database collides on the primary key unless it
// truncates first, so --truncate must empty the table.
func TestRestoreTruncatesWhenAsked(t *testing.T) {
	source, sourceDSN := migratedPool(t)
	seedRows(t, source)
	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{DataOnly: true})

	_, targetDSN := migratedPool(t)
	target := dumper(t, targetDSN)

	// A row the dump does not carry. A truncate must remove it.
	_, err := database.NewRestorer(target, true).
		Restore(t.Context(), strings.NewReader(content), nil)
	require.NoError(t, err)

	pool, err := pgxpool.New(t.Context(), targetDSN)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, first_name, last_name, display_name)
		VALUES ('01890000-0000-7000-8000-000000000003', 'bob', 'bob@example.com',
		        'Bob', 'B', 'Bob B')`)
	require.NoError(t, err)

	_, err = database.NewRestorer(target, true).
		Restore(t.Context(), strings.NewReader(content), nil)
	require.NoError(t, err)

	var count int
	err = pool.QueryRow(t.Context(), `SELECT count(*) FROM public.users`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "the row outside the dump must be gone")

	var username string
	err = pool.QueryRow(t.Context(), `SELECT username FROM public.users`).Scan(&username)
	require.NoError(t, err)
	assert.Equal(t, "alice", username)
}

// Without --truncate a second load of the same dump collides on the primary key
// and must roll back, leaving the target as it was.
func TestRestoreWithoutTruncateFailsOnConflict(t *testing.T) {
	source, sourceDSN := migratedPool(t)
	seedRows(t, source)
	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{DataOnly: true})

	_, targetDSN := migratedPool(t)
	target := dumper(t, targetDSN)

	_, err := database.NewRestorer(target, false).
		Restore(t.Context(), strings.NewReader(content), nil)
	require.NoError(t, err)

	_, err = database.NewRestorer(target, false).
		Restore(t.Context(), strings.NewReader(content), nil)
	require.Error(t, err, "a duplicate primary key must fail")

	pool, err := pgxpool.New(t.Context(), targetDSN)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var count int
	err = pool.QueryRow(t.Context(), `SELECT count(*) FROM public.users`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "the failed load must not have added a row")
}

// A file with no COPY block is not a dump this tool can restore, and saying so
// beats reporting a successful load of nothing.
func TestRestoreRejectsFileWithoutCopyBlocks(t *testing.T) {
	_, targetDSN := migratedPool(t)

	_, err := database.NewRestorer(dumper(t, targetDSN), false).
		Restore(t.Context(), strings.NewReader("-- nothing here\n"), nil)
	require.ErrorIs(t, err, database.ErrNoCopyBlocks)
}

// A dump that stops before the terminator must fail instead of importing a
// partial table and reporting success.
func TestRestoreRejectsTruncatedBlock(t *testing.T) {
	source, sourceDSN := migratedPool(t)
	seedRows(t, source)
	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{DataOnly: true})

	cut := strings.Index(content, "\\.")
	require.Positive(t, cut, "the dump must contain a terminator")

	_, targetDSN := migratedPool(t)
	_, err := database.NewRestorer(dumper(t, targetDSN), false).
		Restore(t.Context(), strings.NewReader(content[:cut]), nil)
	require.Error(t, err, "a block without its terminator must not be accepted")
}

// A restore ignores the DDL in a full dump, because the schema comes from the
// migrations and a second source would only disagree with them.
func TestRestoreIgnoresDDL(t *testing.T) {
	source, sourceDSN := migratedPool(t)
	seedRows(t, source)
	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{})

	_, targetDSN := migratedPool(t)
	_, err := database.NewRestorer(dumper(t, targetDSN), true).
		Restore(t.Context(), strings.NewReader(content), nil)
	require.NoError(t, err)

	pool, err := pgxpool.New(t.Context(), targetDSN)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var count int
	err = pool.QueryRow(t.Context(), `SELECT count(*) FROM public.users`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// An empty database still produces a usable dump rather than an error.
func TestDumpOfEmptyDatabase(t *testing.T) {
	_, sourceDSN := migratedPool(t)

	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{})

	assert.Contains(t, content, "CREATE TABLE")
	assert.Contains(t, content, "-- saka database dump")

	_, targetDSN := migratedPool(t)
	_, err := database.NewRestorer(dumper(t, targetDSN), true).
		Restore(t.Context(), strings.NewReader(content), nil)
	require.NoError(t, err)
}

// The header names the server and the database and stops there. A dump is a
// file that gets copied around, so it must never carry the DSN.
func TestDumpHeaderCarriesNoCredentials(t *testing.T) {
	_, sourceDSN := migratedPool(t)
	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{SchemaOnly: true})

	header, _, _ := strings.Cut(content, "\n\n")
	lines := strings.Split(header, "\n")

	require.Len(t, lines, 4)
	assert.True(t, strings.HasPrefix(lines[0], "-- saka database dump"))
	assert.True(t, strings.HasPrefix(lines[1], "-- server: PostgreSQL"))
	assert.True(t, strings.HasPrefix(lines[2], "-- database: "))
	assert.True(t, strings.HasPrefix(lines[3], "-- generated: "))

	assert.NotContains(t, header, "postgresql://")
	assert.NotContains(t, header, "sslmode")
}

// A schema dump must survive being applied twice. Postgres has no
// CREATE TYPE IF NOT EXISTS and no ADD CONSTRAINT IF NOT EXISTS, so those two
// are guarded by a catalog check; everything else uses the native clause. This
// is the test that catches a statement added without a guard.
func TestSchemaDumpAppliesTwice(t *testing.T) {
	_, sourceDSN := migratedPool(t)
	content := dump(t, dumper(t, sourceDSN), database.DumpOptions{SchemaOnly: true})

	container := testutils.StartPostgres(t.Context(), t)
	target := dumper(t, container.NewDatabase(t))

	_, err := target.Exec(t.Context(), content)
	require.NoError(t, err, "the first apply must succeed")

	_, err = target.Exec(t.Context(), content)
	require.NoError(t, err, "the second apply must succeed, so every statement is idempotent")
}
