package datastore_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

func newTestPostgres(t *testing.T) *datastore.Postgres {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	pg, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             container.DSN,
		ApplicationName: "saka-test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pg.Shutdown(context.Background()) })
	return pg
}

func TestNewPostgresRequiresDSN(t *testing.T) {
	_, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{})
	require.ErrorIs(t, err, datastore.ErrMissingDSN)
}

func TestNewPostgresRejectsMinAboveMax(t *testing.T) {
	_, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:      "postgres://postgres:postgres@127.0.0.1:1/postgres",
		MinConns: 5,
		MaxConns: 2,
	})
	require.Error(t, err)
}

func TestNewPostgresFailsWhenUnreachable(t *testing.T) {
	// The default is a single attempt: a probe that reports rather than waits.
	// A caller that wants the startup to wait asks for it by name.
	started := time.Now()
	_, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN: "postgres://postgres:postgres@127.0.0.1:1/postgres?sslmode=disable",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "attempts", "one attempt is not a count worth naming")
	assert.Less(t, time.Since(started), 5*time.Second, "the default probe must not wait")
}

func TestNewPostgresWaitsForADatabaseThatArrivesLate(t *testing.T) {
	// The case the retry exists for: a database that is not accepting
	// connections yet. The relay refuses nothing — it starts forwarding only
	// after the first attempt has already timed out — so this can only pass if
	// the probe tried again.
	container := testutils.StartPostgres(t.Context(), t)
	target, err := pgx.ParseConfig(container.DSN)
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go relayAfter(listener, 300*time.Millisecond, target.Host+":"+strconv.Itoa(int(target.Port)))

	pg, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:                  dsnThrough(listener, target),
		ConnectTimeout:       250 * time.Millisecond,
		ConnectAttempts:      5,
		ConnectRetryInterval: 200 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { pg.Shutdown(context.Background()) })

	require.NoError(t, pg.Ping(t.Context()))
}

func TestNewPostgresStopsOnceTheAttemptsRunOut(t *testing.T) {
	started := time.Now()
	_, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:                  "postgres://postgres:postgres@127.0.0.1:1/postgres?sslmode=disable",
		ConnectTimeout:       200 * time.Millisecond,
		ConnectAttempts:      3,
		ConnectRetryInterval: 100 * time.Millisecond,
	})
	require.Error(t, err)

	// The count is named, so a reader can tell an exhausted wait from a refused
	// configuration, and the two waits happened.
	assert.Contains(t, err.Error(), "after 3 attempts")
	assert.GreaterOrEqual(t, time.Since(started), 200*time.Millisecond)
}

func TestNewPostgresDoesNotWaitOnAPermanentVerdict(t *testing.T) {
	// Wrong credentials are the server's answer, not a race: retrying cannot
	// change it, so the run fails with the message that says what is wrong
	// instead of holding the startup for the whole budget.
	container := testutils.StartPostgres(t.Context(), t)
	refused := strings.Replace(container.DSN, ":postgres@", ":expelliarmus@", 1)
	require.NotEqual(t, container.DSN, refused, "the DSN must carry a password to replace")

	started := time.Now()
	_, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:                  refused,
		ConnectAttempts:      5,
		ConnectRetryInterval: 2 * time.Second,
	})
	require.Error(t, err)

	assert.NotContains(t, err.Error(), "attempts")
	assert.Less(t, time.Since(started), time.Second, "a refused credential must not be retried")
}

func TestNewPostgresStopsWaitingWhenTheContextEnds(t *testing.T) {
	// A caller that gave up must not be held for the rest of the budget: the
	// wait is interruptible, and both reasons are reported.
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()

	_, err := datastore.NewPostgres(ctx, datastore.PostgresOptions{
		DSN:                  "postgres://postgres:postgres@127.0.0.1:1/postgres?sslmode=disable",
		ConnectAttempts:      10,
		ConnectRetryInterval: 10 * time.Second,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestOpenMigrationDBRetriesTheSameWay(t *testing.T) {
	// A migration command run from a container's boot sequence races the
	// database exactly as serve does, so the handle it opens waits on the same
	// terms.
	started := time.Now()
	_, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{
		DSN:                  "postgres://postgres:postgres@127.0.0.1:1/postgres?sslmode=disable",
		ConnectTimeout:       200 * time.Millisecond,
		ConnectAttempts:      2,
		ConnectRetryInterval: 100 * time.Millisecond,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after 2 attempts")
	assert.GreaterOrEqual(t, time.Since(started), 100*time.Millisecond)
}

// relayAfter starts forwarding connections to target once the delay has passed.
// Before that it accepts nothing, so a connection attempt reaches the listener
// and waits for a server that does not answer.
func relayAfter(listener net.Listener, delay time.Duration, target string) {
	time.Sleep(delay)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()

			upstream, err := net.Dial("tcp", target)
			if err != nil {
				return
			}
			defer func() { _ = upstream.Close() }()

			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(upstream, conn); done <- struct{}{} }()
			go func() { _, _ = io.Copy(conn, upstream); done <- struct{}{} }()
			<-done
		}()
	}
}

// dsnThrough points the container's DSN at the relay instead of the container,
// so the probe dials a listener that answers only once the relay is running.
func dsnThrough(listener net.Listener, target *pgx.ConnConfig) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		target.User, target.Password, listener.Addr().String(), target.Database)
}

// scratchTable creates a uniquely named table and drops it on cleanup. A temp
// table is not usable here: the pool may serve the next statement on another
// connection, which would not see it.
func scratchTable(t *testing.T, pg *datastore.Postgres, columns string) string {
	t.Helper()

	name := fmt.Sprintf("scratch_%d", rand.Int64())
	_, err := pg.Exec(t.Context(), fmt.Sprintf("CREATE TABLE %s (%s)", name, columns))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := pg.Exec(context.WithoutCancel(t.Context()), "DROP TABLE IF EXISTS "+name)
		assert.NoError(t, err)
	})
	return name
}

func TestPostgresQueryAndTransaction(t *testing.T) {
	pg := newTestPostgres(t)
	ctx := t.Context()

	require.NoError(t, pg.Ping(ctx))

	table := scratchTable(t, pg, "id int PRIMARY KEY, name text NOT NULL")

	// Committed work is visible outside the transaction.
	err := pg.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		_, execErr := tx.Exec(ctx, "INSERT INTO "+table+" (id, name) VALUES ($1, $2)", 1, "kept")
		return execErr
	})
	require.NoError(t, err)

	var name string
	require.NoError(t, pg.QueryRow(ctx, "SELECT name FROM "+table+" WHERE id = $1", 1).Scan(&name))
	assert.Equal(t, "kept", name)

	// A failing callback rolls the whole transaction back.
	sentinel := errors.New("boom")
	err = pg.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		_, execErr := tx.Exec(ctx, "INSERT INTO "+table+" (id, name) VALUES ($1, $2)", 2, "dropped")
		if execErr != nil {
			return execErr
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	var count int
	require.NoError(t, pg.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count))
	assert.Equal(t, 1, count)

	// A panicking callback must not leak the transaction.
	assert.Panics(t, func() {
		_ = pg.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
			_, _ = tx.Exec(ctx, "INSERT INTO "+table+" (id, name) VALUES ($1, $2)", 3, "panic")
			panic("boom")
		})
	})

	require.NoError(t, pg.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count))
	assert.Equal(t, 1, count, "the panicking transaction must be rolled back")
}

func TestWithSavepointContainsAFailedStatement(t *testing.T) {
	// A failed statement poisons its transaction until rollback: every later
	// statement, and the commit, fail with `25P02`. The savepoint is what
	// lets a caller ignore a side effect's failure and still commit its own
	// work.
	pg := newTestPostgres(t)
	ctx := t.Context()

	table := scratchTable(t, pg, "id int PRIMARY KEY, name text NOT NULL")

	sentinel := errors.New("the side effect failed")
	err := pg.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		// A duplicate key, the shape a failed insert takes.
		_, firstErr := tx.Exec(ctx, "INSERT INTO "+table+" (id, name) VALUES ($1, $2)", 1, "kept")
		require.NoError(t, firstErr)

		sideEffect := datastore.WithSavepoint(ctx, tx, func(ctx context.Context, q datastore.Querier) error {
			if _, dupErr := q.Exec(ctx, "INSERT INTO "+table+" (id, name) VALUES ($1, $2)", 1, "duplicate"); dupErr != nil {
				return dupErr
			}
			return sentinel
		})
		require.Error(t, sideEffect, "the side effect's failure must reach the caller")

		// The transaction is still usable, which is the whole point.
		_, secondErr := tx.Exec(ctx, "INSERT INTO "+table+" (id, name) VALUES ($1, $2)", 2, "also-kept")
		return secondErr
	})
	require.NoError(t, err, "the caller's transaction must still commit")

	var count int
	require.NoError(t, pg.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count))
	assert.Equal(t, 2, count, "both caller rows must survive the failed side effect")
}

func TestWithSavepointOnThePoolRunsTheFunctionDirectly(t *testing.T) {
	// A pool has no surrounding transaction to poison, so there is nothing to
	// contain: the function runs on the surface as given, and its failure is
	// returned unchanged.
	pg := newTestPostgres(t)
	table := scratchTable(t, pg, "id int PRIMARY KEY")

	sentinel := errors.New("boom")
	err := datastore.WithSavepoint(t.Context(), pg, func(ctx context.Context, q datastore.Querier) error {
		_, execErr := q.Exec(ctx, "INSERT INTO "+table+" (id) VALUES ($1)", 1)
		require.NoError(t, execErr)
		return sentinel
	})
	require.ErrorIs(t, err, sentinel, "the function's error is what the caller sees")

	var count int
	require.NoError(t, pg.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&count))
	assert.Equal(t, 1, count, "the insert ran and was not rolled back by the helper")
}

func TestPostgresStatsExposePoolCounters(t *testing.T) {
	pg := newTestPostgres(t)

	require.NoError(t, pg.Ping(t.Context()))
	assert.Equal(t, int32(10), pg.Stats().MaxConns())
}

func TestPostgresAppliesSessionDefaults(t *testing.T) {
	pg := newTestPostgres(t)
	ctx := t.Context()

	var searchPath, timezone, applicationName string
	err := pg.QueryRow(ctx,
		"SELECT current_setting('search_path'), current_setting('timezone'), current_setting('application_name')",
	).Scan(&searchPath, &timezone, &applicationName)
	require.NoError(t, err)

	assert.Equal(t, datastore.PgSearchPath, searchPath)
	assert.Equal(t, datastore.PgTimezone, timezone)
	assert.Equal(t, "saka-test", applicationName)
}

// Session parameters must survive connection reuse. pgxpool resets a recycled
// connection from RuntimeParams; a startup query would be lost here.
func TestPostgresKeepsSessionDefaultsOnReusedConnection(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	pg, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:      container.DSN,
		MaxConns: 1,
		MinConns: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { pg.Shutdown(context.Background()) })
	ctx := t.Context()

	var timezone string
	for range 3 {
		require.NoError(t, pg.QueryRow(ctx, "SELECT current_setting('timezone')").Scan(&timezone))
		assert.Equal(t, datastore.PgTimezone, timezone)
	}
}

func TestErrNoRowsMatchesPgx(t *testing.T) {
	pg := newTestPostgres(t)
	table := scratchTable(t, pg, "id int")

	err := pg.QueryRow(t.Context(), "SELECT id FROM "+table).Scan(new(int))
	require.ErrorIs(t, err, datastore.ErrNoRows)
}

func TestAcquireReturnsReusableConnection(t *testing.T) {
	pg := newTestPostgres(t)
	ctx := t.Context()

	conn, err := pg.Acquire(ctx)
	require.NoError(t, err)

	// A session-level setting made through the acquired connection is visible
	// on that connection only, which is what the callers need it for.
	_, err = conn.Exec(ctx, "SET application_name = 'acquired'")
	require.NoError(t, err)

	var name string
	require.NoError(t, conn.QueryRow(ctx, "SELECT current_setting('application_name')").Scan(&name))
	assert.Equal(t, "acquired", name)

	conn.Release()
	assert.Equal(t, int32(0), pg.Stats().AcquiredConns(), "the connection must return to the pool")
}

func TestMigrationDBUsesSingleConnection(t *testing.T) {
	pg := newTestPostgres(t)
	ctx := t.Context()

	db, err := pg.MigrationDB(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	stats := db.Stats()
	assert.Equal(t, 1, stats.MaxOpenConnections, "migrations must not run on a pool")

	// The migration handle is independent of the pool: closing it must not
	// affect the application pool.
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, pg.Ping(ctx))
}

func TestOpenMigrationDBWithoutPool(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)

	db, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: container.DSN})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	assert.Equal(t, 1, db.Stats().MaxOpenConnections)

	var one int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT 1").Scan(&one))
	assert.Equal(t, 1, one)
}

func TestOpenMigrationDBRequiresDSN(t *testing.T) {
	_, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{})
	require.ErrorIs(t, err, datastore.ErrMissingDSN)
}
