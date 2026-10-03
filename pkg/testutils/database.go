package testutils

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// NewDatabase creates a fresh database inside the shared Postgres container and
// returns its DSN. Tests that mutate schema state must use this instead of the
// container DSN: StartPostgres hands out one database for the whole test binary,
// so migrations applied by one test would leak into the next.
//
// A test may call this more than once, and each call gets its own database: a
// test that compares a source against a target needs two.
//
// The database is dropped when the test finishes.
func (p *Postgres) NewDatabase(t testing.TB) string {
	t.Helper()

	name := testDatabaseName(t)

	admin, err := sql.Open("pgx", p.DSN)
	require.NoError(t, err, "open admin connection")
	defer func() { require.NoError(t, admin.Close()) }()

	ctx := t.Context()
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+quoteIdent(name))
	require.NoError(t, err, "create test database")

	t.Cleanup(func() {
		cleanup, err := sql.Open("pgx", p.DSN)
		require.NoError(t, err, "open cleanup connection")
		defer func() { require.NoError(t, cleanup.Close()) }()

		// The test context is already canceled by the time cleanup runs.
		_, err = cleanup.ExecContext(context.WithoutCancel(t.Context()),
			"DROP DATABASE IF EXISTS "+quoteIdent(name)+" WITH (FORCE)")
		require.NoError(t, err, "drop test database")
	})

	return replaceDatabase(p.DSN, name)
}

// databaseCounters tracks how many databases a test has asked for, so the second
// call does not collide with the first. A bare test name is used for the first
// database, which keeps the name readable while debugging.
var (
	databaseCountersMu sync.Mutex
	databaseCounters   = map[string]int{}
)

// testDatabaseName derives a valid, unique database name from the test name.
func testDatabaseName(t testing.TB) string {
	databaseCountersMu.Lock()
	databaseCounters[t.Name()]++
	ordinal := databaseCounters[t.Name()]
	databaseCountersMu.Unlock()

	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, t.Name())

	if ordinal > 1 {
		sanitized = fmt.Sprintf("%s_%d", sanitized, ordinal)
	}

	// Postgres identifiers are limited to 63 bytes. The prefix and the ordinal
	// both have to fit, so the test name is trimmed last.
	const maxName = 63 - len("saka_test_")
	if len(sanitized) > maxName {
		sanitized = sanitized[:maxName]
	}
	return "saka_test_" + sanitized
}

// replaceDatabase points dsn at another database, keeping credentials and
// options.
func replaceDatabase(dsn, database string) string {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	parsed.Path = "/" + database
	return parsed.String()
}

// quoteIdent wraps an identifier in double quotes for the DDL statements that
// cannot take a bind parameter.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
