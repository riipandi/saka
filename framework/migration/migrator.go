// Package migration is the goose-native migration toolkit: a binary composes
// the migration sets it owns and runs each over its own version table.
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Set is one migration sequence a binary composes: a name the reports use,
// the SQL files themselves, and the version table the applied migrations are
// recorded in. The lock key, when non-zero, is the session advisory lock the
// set runs under — sets that share a database but are run by different
// binaries take distinct keys so their runs never block each other.
type Set struct {
	// Name names the set in the composed reports: "app", "queue", …
	Name string
	// FS carries the set's .sql files at its root.
	FS fs.FS
	// VersionTable records the applied migrations. goose defaults to
	// goose_db_version; every set owns its name so the schema reads as ours.
	// The table resolves through the connection's current_schema().
	VersionTable string
	// LockKey is the session advisory lock id the set's runs take. Zero
	// means goose's default lock.
	LockKey int64
}

// Compose puts the sets a binary runs into their run order. A duplicate
// set name, version table, or lock key is a composition error — two sets
// recording into one table would each believe the other's migrations
// applied — so the mistake fails the composition rather than a run.
func Compose(sets ...Set) ([]Set, error) {
	out := make([]Set, 0, len(sets))
	seen := map[string]string{} // value -> the set that claimed it
	for _, set := range sets {
		for _, key := range []string{"name:" + set.Name, "table:" + set.VersionTable} {
			if first, dup := seen[key]; dup {
				return nil, fmt.Errorf("migration: set %q duplicates %s with set %q", set.Name, key, first)
			}
			seen[key] = set.Name
		}
		if set.LockKey != 0 {
			key := fmt.Sprintf("lock:%d", set.LockKey)
			if first, dup := seen[key]; dup {
				return nil, fmt.Errorf("migration: set %q duplicates the advisory lock %d with set %q", set.Name, set.LockKey, first)
			}
			seen[key] = set.Name
		}
		out = append(out, set)
	}
	return out, nil
}

// MigratorOptions configures a Migrator.
type MigratorOptions struct {
	// AllowOutOfOrder applies migrations that are missing below the current
	// database version instead of failing. Without it goose refuses to run,
	// because an older migration may depend on a schema the newer ones changed.
	AllowOutOfOrder bool
	// Progress receives one event per step of a run as it happens. It is called
	// from the goroutine running the migration, so it must not block. A nil
	// Progress disables reporting.
	Progress func(ProgressEvent)
}

// Migrator applies one set's migrations over a single-connection handle.
type Migrator struct {
	// set is the sequence this migrator runs; the version table and the
	// report lines read from it.
	set      Set
	provider *goose.Provider
	// db is the single-connection handle the provider runs on. It is kept for
	// the maintenance statements that must not open a second connection, such as
	// rewinding the version table's identity sequence.
	db *sql.DB
}

// Migration is one migration goose executed.
type Migration struct {
	Version  int64
	Name     string
	Duration time.Duration
	Empty    bool
}

// MigrationStatus is one migration known to the binary and whether the
// database has it.
type MigrationStatus struct {
	Version   int64
	Name      string
	Applied   bool
	AppliedAt time.Time
}

// NewMigrator loads the set's migrations and prepares the provider. db must
// be the single-connection handle from datastore.OpenMigrationDB: goose takes a
// session advisory lock on it, so a pool could hand the lock and the migration
// statements to different backends.
//
// When opts.Progress is set, goose runs in verbose mode and its log records are
// translated into progress events. goose has no progress hook of its own, so a
// custom slog handler is the only way to see a run while it happens.
func NewMigrator(ctx context.Context, db *sql.DB, set Set, opts MigratorOptions) (*Migrator, error) {
	if db == nil {
		return nil, errors.New("migration: migrator requires a database handle")
	}
	if set.FS == nil {
		return nil, fmt.Errorf("migration: set %q carries no migration FS", set.Name)
	}
	if set.VersionTable == "" {
		return nil, fmt.Errorf("migration: set %q names no version table", set.Name)
	}

	var lockerOpt goose.ProviderOption
	if set.LockKey != 0 {
		locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(set.LockKey))
		if err != nil {
			return nil, fmt.Errorf("migration: create migration locker: %w", err)
		}
		lockerOpt = goose.WithSessionLocker(locker)
	}

	providerOptions := []goose.ProviderOption{
		goose.WithTableName(set.VersionTable),
		goose.WithAllowOutofOrder(opts.AllowOutOfOrder),
	}
	if lockerOpt != nil {
		providerOptions = append(providerOptions, lockerOpt)
	}
	if opts.Progress != nil {
		providerOptions = append(providerOptions,
			goose.WithVerbose(true),
			goose.WithSlog(slog.New(&progressHandler{emit: opts.Progress})),
		)
	} else {
		// The command prints the results itself, in the same shape as the rest
		// of the CLI, so goose must not log them a second time.
		providerOptions = append(providerOptions, goose.WithLogger(goose.NopLogger()))
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, set.FS, providerOptions...)
	if err != nil {
		return nil, fmt.Errorf("migration: load migrations of set %q: %w", set.Name, err)
	}
	return &Migrator{set: set, provider: provider, db: db}, nil
}

// Name names the set this migrator runs — the composed reports label the
// rows with it.
func (m *Migrator) Name() string { return m.set.Name }

// VersionTable names the table the set records into.
func (m *Migrator) VersionTable() string { return m.set.VersionTable }

// Up applies every pending migration. An already up-to-date database returns an
// empty slice.
func (m *Migrator) Up(ctx context.Context) ([]Migration, error) {
	results, err := m.provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("migration: migrate up: %w", err)
	}
	return convert(results), nil
}

// UpTo applies pending migrations up to and including version.
func (m *Migrator) UpTo(ctx context.Context, version int64) ([]Migration, error) {
	results, err := m.provider.UpTo(ctx, version)
	if err != nil {
		return nil, fmt.Errorf("migration: migrate up to %d: %w", version, err)
	}
	return convert(results), nil
}

// Down rolls back up to count migrations, newest first. It stops early when the
// database runs out of applied migrations, which is not an error.
//
// Each migration is rolled back on its own, so goose picks the next one in the
// order it recorded. That is exact even for out-of-order migrations, which a
// version comparison cannot express. A failure returns the migrations that did
// roll back alongside the error.
func (m *Migrator) Down(ctx context.Context, count int) ([]Migration, error) {
	if count <= 0 {
		return nil, fmt.Errorf("migration: rollback count must be greater than zero, got %d", count)
	}

	results := make([]Migration, 0, count)
	for range count {
		result, err := m.provider.Down(ctx)
		if errors.Is(err, goose.ErrNoNextVersion) {
			return results, nil
		}
		if err != nil {
			return results, fmt.Errorf("migration: migrate down: %w", err)
		}
		results = append(results, convert([]*goose.MigrationResult{result})...)
	}
	return results, nil
}

// Status lists every migration of the set with its applied state, in version order.
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	statuses, err := m.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("migration: read migration status: %w", err)
	}

	out := make([]MigrationStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, MigrationStatus{
			Version:   status.Source.Version,
			Name:      filepath.Base(status.Source.Path),
			Applied:   status.State == goose.StateApplied,
			AppliedAt: status.AppliedAt,
		})
	}
	return out, nil
}

// Version returns the highest version recorded in the database, or 0 when the
// database has never been migrated.
func (m *Migrator) Version(ctx context.Context) (int64, error) {
	version, err := m.provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("migration: read database version: %w", err)
	}
	return version, nil
}

// Pending lists the migrations the database has not applied yet, in version order.
func (m *Migrator) Pending(ctx context.Context) ([]MigrationStatus, error) {
	statuses, err := m.Status(ctx)
	if err != nil {
		return nil, err
	}

	pending := make([]MigrationStatus, 0, len(statuses))
	for _, status := range statuses {
		if !status.Applied {
			pending = append(pending, status)
		}
	}
	return pending, nil
}

// Applied lists the migrations the database has applied, newest first, which is
// the order a rollback consumes them. Migrations applied out of order are
// rolled back in the order goose recorded them, so this list is an
// approximation of that order when --allow-out-of-order is in use.
func (m *Migrator) Applied(ctx context.Context) ([]MigrationStatus, error) {
	statuses, err := m.Status(ctx)
	if err != nil {
		return nil, err
	}

	applied := make([]MigrationStatus, 0, len(statuses))
	for _, status := range statuses {
		if status.Applied {
			applied = append(applied, status)
		}
	}
	slices.Reverse(applied)
	return applied, nil
}

// HighestVersion returns the version of the set's last migration, which is
// the version the database reaches after a full up.
func (m *Migrator) HighestVersion() int64 {
	sources := m.provider.ListSources()
	if len(sources) == 0 {
		return 0
	}
	return sources[len(sources)-1].Version
}

// ResetIdentity rewinds the identity sequence of the version table to the
// highest id it still holds, so the next recorded migration continues from
// there.
//
// goose creates the id column as `integer PRIMARY KEY GENERATED BY DEFAULT AS
// IDENTITY`, and a sequence never moves backwards. A rollback deletes rows but
// leaves the sequence where it was, so after rolling everything back the
// sentinel keeps id 1 while the next apply starts at 10, 19, and so on — the ids
// stop meaning anything.
//
// The sentinel row is deliberately kept. goose requires a row for version 0 to
// exist and refuses every command with "missing zero version migration" without
// it, so the table is never emptied; only the sequence moves.
//
// setval to max(id) with is_called left true makes the next value max(id)+1,
// which is exactly "continue after the last row". The statement runs on the
// migrator's own connection, so it cannot interleave with a goose run.
func (m *Migrator) ResetIdentity(ctx context.Context) error {
	// The table name is the set's own constant, composed by the binary — not
	// request input — and goose has no placeholder syntax for identifiers, so
	// the statement is formatted with it directly.
	query := fmt.Sprintf( //nolint:gosec // G201: the version table is a composed constant, not input
		"SELECT setval(pg_get_serial_sequence('%s', 'id'), max(id)) FROM %s",
		m.set.VersionTable, m.set.VersionTable)
	if _, err := m.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("migration: rewind %s identity: %w", m.set.VersionTable, err)
	}
	return nil
}

func convert(results []*goose.MigrationResult) []Migration {
	out := make([]Migration, 0, len(results))
	for _, result := range results {
		out = append(out, Migration{
			Version:  result.Source.Version,
			Name:     filepath.Base(result.Source.Path),
			Duration: result.Duration,
			Empty:    result.Empty,
		})
	}
	return out
}
