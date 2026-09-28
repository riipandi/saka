package datastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Pool defaults, applied when the matching option is zero.
const (
	defaultMaxConns             = 10
	defaultMinConns             = 2
	defaultMaxConnLifetime      = time.Hour
	defaultMaxConnIdleTime      = 30 * time.Minute
	defaultHealthCheckPeriod    = time.Minute
	defaultConnectTimeout       = 5 * time.Second
	defaultConnectAttempts      = 1
	defaultConnectRetryInterval = 2 * time.Second
)

// Session defaults, applied to every connection in the pool.
const (
	// PgSearchPath lists the schemas created by migration 00000. A schema that
	// does not exist yet is ignored by Postgres, so a pre-migration connection
	// still succeeds.
	PgSearchPath = "public,internal,reference"
	// PgTimezone keeps every session in UTC so timestamps never depend on the
	// host or container clock.
	PgTimezone = "UTC"
)

// ErrNoRows is returned by QueryRow.Scan when the query matched no rows.
// Repositories compare against this instead of importing pgx themselves.
var ErrNoRows = pgx.ErrNoRows

// ErrMissingDSN is returned when a Postgres handle is opened without a DSN.
var ErrMissingDSN = errors.New("datastore: postgres DSN is required")

// Querier is the query surface shared by the pool and a transaction, so a
// repository can accept either without knowing which one it got.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PostgresOptions configures the application connection pool. Zero fields fall
// back to the defaults above; only DSN is required.
type PostgresOptions struct {
	DSN                  string
	ApplicationName      string
	SearchPath           string
	Timezone             string
	MaxConns             int32
	MinConns             int32
	MaxConnLifetime      time.Duration
	MaxConnIdleTime      time.Duration
	HealthCheckPeriod    time.Duration
	ConnectTimeout       time.Duration
	ConnectAttempts      int
	ConnectRetryInterval time.Duration
	Logger               *slog.Logger
}

// Postgres owns the single connection pool of the process. Nothing else may
// open another pool or a raw driver connection.
type Postgres struct {
	pool *pgxpool.Pool
	cfg  *pgxpool.Config
	opts PostgresOptions
}

// NewPostgres opens the pool and verifies connectivity, so an unreachable
// database fails at startup instead of on the first query.
//
// The probe retries: a database that starts beside the application is not
// necessarily ready when the application is, and a server meant to start
// unattended should wait out that race rather than die on it. ConnectAttempts
// bounds the wait, so an unreachable database still ends the run — with the
// driver's own error — instead of blocking forever.
func NewPostgres(ctx context.Context, opts PostgresOptions) (*Postgres, error) {
	cfg, normalized, err := newPoolConfig(opts)
	if err != nil {
		return nil, err
	}

	pool, err := connect(ctx, cfg, normalized)
	if err != nil {
		return nil, err
	}
	return &Postgres{pool: pool, cfg: cfg, opts: normalized}, nil
}

// connect opens the pool and pings it, waiting between attempts while the
// database is unreachable.
func connect(ctx context.Context, cfg *pgxpool.Config, opts PostgresOptions) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	err := retryConnect(ctx, opts, "datastore: ping postgres", func(ctx context.Context) error {
		handle, err := openAndPing(ctx, cfg, opts.ConnectTimeout)
		if err != nil {
			return err
		}
		pool = handle
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pool, nil
}

// retryConnect runs an attempt until it succeeds, the attempts run out, or the
// server gave a verdict a retry cannot change.
//
// A failure the server itself reported — wrong credentials, a database that does
// not exist — ends the wait immediately: a retry cannot change the answer, and
// holding a startup for the full budget would only delay the message the
// operator needs to read. Everything else is treated as transient, which is the
// honest reading of a dial, a TLS handshake, or a timeout: the database may be
// starting, restarting, or briefly saturated.
//
// label names the operation in the final error, so a caller reads which handle
// could not be opened. The wait is a plain interval rather than a backoff,
// because the attempt count already bounds it and a startup wants a predictable
// one.
func retryConnect(
	ctx context.Context,
	opts PostgresOptions,
	label string,
	attempt func(ctx context.Context) error,
) error {
	var lastErr error
	made := 0
	for try := 1; try <= opts.ConnectAttempts; try++ {
		made = try
		err := attempt(ctx)
		if err == nil {
			if try > 1 {
				opts.logger().InfoContext(ctx, "datastore: postgres reachable",
					"attempt", try, "attempts", opts.ConnectAttempts)
			}
			return nil
		}
		lastErr = err

		if try == opts.ConnectAttempts || !retryable(err) {
			break
		}
		opts.logger().WarnContext(ctx, "datastore: postgres unreachable, retrying",
			"attempt", try, "attempts", opts.ConnectAttempts,
			"retry_in", opts.ConnectRetryInterval, "err", err.Error())

		timer := time.NewTimer(opts.ConnectRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(lastErr, ctx.Err())
		case <-timer.C:
		}
	}

	// Only a wait that happened is named. A verdict the server gave on the
	// first try is not a count of attempts, and the driver's own message
	// already says what is wrong; a single attempt is not a fact the reader
	// needs either, so a probe that reports rather than waits keeps the error
	// it always had.
	if made > 1 {
		return fmt.Errorf("%s after %d attempts: %w", label, made, lastErr)
	}
	return fmt.Errorf("%s: %w", label, lastErr)
}

// openAndPing builds one pool and proves it can reach the database. The pool is
// closed on failure, so a retry starts from a fresh handle rather than one
// whose background connection attempts were already abandoned.
func openAndPing(ctx context.Context, cfg *pgxpool.Config, timeout time.Duration) (*pgxpool.Pool, error) {
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// retryable reports whether another attempt could succeed.
//
// The answer comes from the server when the server gave one: an authentication
// failure or an unknown database is a permanent verdict on this DSN. Everything
// else — a refused dial, a TLS failure, a timeout, a server that is still
// starting — is read as transient, because none of them is a statement about the
// configuration.
func retryable(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return true
	}
	switch pgErr.Code {
	case sqlStateInvalidPassword, sqlStateInvalidAuthorization, sqlStateUnknownDatabase:
		return false
	default:
		return true
	}
}

// SQLSTATE codes a retry cannot change: the server read the connection request
// and refused it. Postgres reports them by code, so the match does not depend on
// a message being translated.
const (
	sqlStateInvalidPassword      = "28P01"
	sqlStateInvalidAuthorization = "28000"
	sqlStateUnknownDatabase      = "3D000"
)

// OpenMigrationDB opens the single-connection handle used by goose, without
// building a pool. The caller owns the returned handle and must close it.
//
// It retries on the same terms as the pool: a migration command run from a
// container's boot sequence races the database exactly the way serve does, and
// the wait is bounded by the same attempt count.
func OpenMigrationDB(ctx context.Context, opts PostgresOptions) (*sql.DB, error) {
	cfg, normalized, err := newPoolConfig(opts)
	if err != nil {
		return nil, err
	}

	var db *sql.DB
	err = retryConnect(ctx, normalized, "datastore: open postgres migration connection",
		func(ctx context.Context) error {
			handle, openErr := openMigrationDB(ctx, cfg.ConnConfig, normalized.ConnectTimeout)
			if openErr != nil {
				return openErr
			}
			db = handle
			return nil
		})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// MigrationDB opens the single-connection handle from the running pool
// configuration. It does not retry: the pool it is built from already proved
// the database reachable, so a failure here is a new condition rather than a
// startup race.
func (p *Postgres) MigrationDB(ctx context.Context) (*sql.DB, error) {
	db, err := openMigrationDB(ctx, p.cfg.ConnConfig, p.opts.ConnectTimeout)
	if err != nil {
		return nil, fmt.Errorf("datastore: open postgres migration connection: %w", err)
	}
	return db, nil
}

// Acquire takes a connection out of the pool for work that needs several
// statements on the same backend, such as LISTEN or a session-level advisory
// lock. The caller must release it with conn.Release.
func (p *Postgres) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("datastore: acquire postgres connection: %w", err)
	}
	return conn, nil
}

// Exec runs a statement on the pool.
func (p *Postgres) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}

// Query runs a query on the pool.
func (p *Postgres) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.pool.Query(ctx, sql, args...)
}

// QueryRow runs a single-row query on the pool.
func (p *Postgres) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}

// WithTx runs fn inside a transaction. The transaction is committed when fn
// returns nil, and rolled back otherwise — including on panic. The callback
// receives the shared Querier surface, so it passes the transaction to any
// repository, seeder, or store written against it.
func (p *Postgres) WithTx(ctx context.Context, fn func(ctx context.Context, tx Querier) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("datastore: begin transaction: %w", err)
	}

	// Roll back on a context that survives caller cancellation, so an aborted
	// request still releases the transaction. Rolling back after a successful
	// commit is a no-op (pgx.ErrTxClosed).
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("datastore: commit transaction: %w", err)
	}
	return nil
}

// WithSavepoint runs fn inside a savepoint on the query surface it is given,
// so a statement that fails inside fn leaves the surrounding transaction
// usable.
//
// Postgres aborts a transaction on any statement error until the transaction
// is rolled back (`25P02`): every later statement, and the commit, fails with
// "current transaction is aborted" rather than with the original reason. A
// side effect that is allowed to fail without failing the caller — an audit
// record, a metric row — therefore cannot simply ignore its own error: it
// would take the caller's change down with it. Rolling back to a savepoint
// clears the aborted state, and the caller's transaction commits what it
// meant to.
//
// The surface decides how. A transaction opens a nested transaction, which
// pgx implements as a savepoint. A pool has no surrounding transaction to
// poison — the statement is its own transaction, so a failure is already
// contained — and fn runs on the surface as given. The caller passes whatever
// it holds, so the same code works inside and outside a transaction.
//
// fn's error is returned, not the savepoint's: the caller is deciding what a
// failed side effect means, and the reason it failed is what it needs.
func WithSavepoint(ctx context.Context, q Querier, fn func(ctx context.Context, q Querier) error) error {
	tx, ok := q.(pgx.Tx)
	if !ok {
		return fn(ctx, q)
	}

	sp, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("datastore: begin savepoint: %w", err)
	}

	if err := fn(ctx, sp); err != nil {
		// The rollback runs on a context that survives caller cancellation,
		// the same way WithTx's does: a request that gave up must still
		// clear the aborted state it left behind. Rolling back a savepoint
		// that was already released is an error we ignore.
		_ = sp.Rollback(context.WithoutCancel(ctx))
		return err
	}
	if err := sp.Commit(ctx); err != nil {
		return fmt.Errorf("datastore: release savepoint: %w", err)
	}
	return nil
}

// Ping reports whether the database is reachable.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Stats exposes pool counters for health reporting.
func (p *Postgres) Stats() *pgxpool.Stat {
	return p.pool.Stat()
}

// Shutdown drains and closes the pool, and is what the container calls when
// the run ends.
//
// It is named Shutdown rather than Close because that is the interface
// samber/do looks for: the container calls `Shutdown` on every service that
// implements it and ignores `Close` entirely, so a pool exposing only Close
// was never closed by a run that shut down through the container. There is no
// error to report — pgxpool.Close does not fail — so this is the
// context-only form of the interface.
func (p *Postgres) Shutdown(context.Context) {
	// A handle that was never opened is not a failure to report: the
	// container calls this on whatever it holds, and a test that registers a
	// stub registers a zero value.
	if p == nil || p.pool == nil {
		return
	}
	p.pool.Close()
}

func newPoolConfig(opts PostgresOptions) (*pgxpool.Config, PostgresOptions, error) {
	if opts.DSN == "" {
		return nil, opts, ErrMissingDSN
	}

	cfg, err := pgxpool.ParseConfig(opts.DSN)
	if err != nil {
		return nil, opts, fmt.Errorf("datastore: parse postgres DSN: %w", err)
	}

	opts = opts.normalize()
	if opts.MinConns < 0 {
		return nil, opts, fmt.Errorf("datastore: postgres min conns must not be negative: %d", opts.MinConns)
	}
	if opts.MinConns > opts.MaxConns {
		return nil, opts, fmt.Errorf(
			"datastore: postgres min conns (%d) exceeds max conns (%d)", opts.MinConns, opts.MaxConns)
	}

	cfg.MaxConns = opts.MaxConns
	cfg.MinConns = opts.MinConns
	cfg.MaxConnLifetime = opts.MaxConnLifetime
	cfg.MaxConnIdleTime = opts.MaxConnIdleTime
	cfg.HealthCheckPeriod = opts.HealthCheckPeriod
	cfg.ConnConfig.ConnectTimeout = opts.ConnectTimeout

	// Session parameters must live in RuntimeParams, not in a startup query:
	// pgxpool resets a reused connection with these, so the pool never hands
	// out a connection on the wrong search_path or timezone.
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string, 3)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = opts.SearchPath
	cfg.ConnConfig.RuntimeParams["timezone"] = opts.Timezone
	if opts.ApplicationName != "" {
		cfg.ConnConfig.RuntimeParams["application_name"] = opts.ApplicationName
	}
	return cfg, opts, nil
}

func (o PostgresOptions) normalize() PostgresOptions {
	if o.MaxConns <= 0 {
		o.MaxConns = defaultMaxConns
	}
	if o.MinConns == 0 {
		o.MinConns = min(defaultMinConns, o.MaxConns)
	}
	if o.MaxConnLifetime <= 0 {
		o.MaxConnLifetime = defaultMaxConnLifetime
	}
	if o.MaxConnIdleTime <= 0 {
		o.MaxConnIdleTime = defaultMaxConnIdleTime
	}
	if o.HealthCheckPeriod <= 0 {
		o.HealthCheckPeriod = defaultHealthCheckPeriod
	}
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = defaultConnectTimeout
	}
	if o.ConnectAttempts <= 0 {
		o.ConnectAttempts = defaultConnectAttempts
	}
	if o.ConnectRetryInterval <= 0 {
		o.ConnectRetryInterval = defaultConnectRetryInterval
	}
	if o.SearchPath == "" {
		o.SearchPath = PgSearchPath
	}
	if o.Timezone == "" {
		o.Timezone = PgTimezone
	}
	return o
}

// logger is the logger the probe writes through, never nil: a caller that asked
// for no logging still reaches a discard handler, so the retry loop has one
// path.
func (o PostgresOptions) logger() *slog.Logger {
	if o.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return o.Logger
}

// openMigrationDB builds a database/sql handle pinned to one connection.
// Migrations must not run on the pool: goose takes a session advisory lock and
// executes multi-statement files, and both require every statement of a run to
// land on the same backend. Idle and lifetime reaping are disabled so that the
// single connection stays open for the whole run.
func openMigrationDB(ctx context.Context, connConfig *pgx.ConnConfig, connectTimeout time.Duration) (*sql.DB, error) {
	db := stdlib.OpenDB(*connConfig)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
