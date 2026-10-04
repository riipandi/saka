package migration

import (
	"context"
	"database/sql"
)

// Runner runs a composed sequence of sets as one migration surface: Up and
// Down walk the sets in composition order (reverse for Down), so a composed
// run answers with one report and one version space per set. The single-set
// semantics are each Migrator's own; the Runner only carries the order.
type Runner struct {
	migrators []*Migrator
}

// NewRunner composes the sets and prepares one migrator per set. The
// composition rules (Compose) apply: duplicate names, version tables, and
// lock keys fail here.
func NewRunner(ctx context.Context, db *sql.DB, sets []Set, opts MigratorOptions) (*Runner, error) {
	composed, err := Compose(sets...)
	if err != nil {
		return nil, err
	}
	migrators := make([]*Migrator, 0, len(composed))
	for _, set := range composed {
		migrator, err := NewMigrator(ctx, db, set, opts)
		if err != nil {
			return nil, err
		}
		migrators = append(migrators, migrator)
	}
	return &Runner{migrators: migrators}, nil
}

// Name names the set at index i in the run order.
func (r *Runner) Name(i int) string { return r.migrators[i].Name() }

// Len is the number of sets in the composition.
func (r *Runner) Len() int { return len(r.migrators) }

// Migrator answers the set migrator at index i, for a caller that reports
// per set.
func (r *Runner) Migrator(i int) *Migrator { return r.migrators[i] }

// Up applies every pending migration of every set, in composition order.
func (r *Runner) Up(ctx context.Context) ([]Migration, error) {
	var all []Migration
	for _, migrator := range r.migrators {
		applied, err := migrator.Up(ctx)
		if err != nil {
			return all, err
		}
		all = append(all, applied...)
	}
	return all, nil
}

// Down rolls back at most count migrations across the sets, consuming the
// composition in reverse: a set whose tables reference another set's rows
// lets go first. The count is a global budget, not a per-set one.
func (r *Runner) Down(ctx context.Context, count int) ([]Migration, error) {
	var all []Migration
	for i := len(r.migrators) - 1; i >= 0 && count > 0; i-- {
		rolled, err := r.migrators[i].Down(ctx, count)
		all = append(all, rolled...)
		if err != nil {
			return all, err
		}
		count -= len(rolled)
	}
	return all, nil
}

// Pending lists every set's pending migrations in composition order.
func (r *Runner) Pending(ctx context.Context) ([]MigrationStatus, error) {
	return r.gatherStatus(ctx, func(m *Migrator) ([]MigrationStatus, error) { return m.Pending(ctx) })
}

// Status lists every set's migrations and their applied state.
func (r *Runner) Status(ctx context.Context) ([]MigrationStatus, error) {
	return r.gatherStatus(ctx, func(m *Migrator) ([]MigrationStatus, error) { return m.Status(ctx) })
}

// Applied lists every set's applied migrations, newest first per set.
func (r *Runner) Applied(ctx context.Context) ([]MigrationStatus, error) {
	return r.gatherStatus(ctx, func(m *Migrator) ([]MigrationStatus, error) { return m.Applied(ctx) })
}

func (r *Runner) gatherStatus(ctx context.Context, fn func(*Migrator) ([]MigrationStatus, error)) ([]MigrationStatus, error) {
	var all []MigrationStatus
	for _, migrator := range r.migrators {
		statuses, err := fn(migrator)
		if err != nil {
			return all, err
		}
		all = append(all, statuses...)
	}
	return all, nil
}

// Version answers the first set's recorded version — the app set's number is
// the one scripts and reports read.
func (r *Runner) Version(ctx context.Context) (int64, error) {
	return r.migrators[0].Version(ctx)
}

// ResetIdentity rewinds every set's version-table identity sequence.
func (r *Runner) ResetIdentity(ctx context.Context) error {
	for _, migrator := range r.migrators {
		if err := migrator.ResetIdentity(ctx); err != nil {
			return err
		}
	}
	return nil
}
