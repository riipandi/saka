// Package seeders creates the default records a fresh database needs. Every
// seeder is idempotent, so migrate:seed is safe to run repeatedly.
package seeders

import (
	"context"
	"fmt"

	"github.com/riipandi/tango/internal/datastore"
)

// Seeder creates the default records of one kind.
type Seeder struct {
	// Name identifies the seeder in the report, e.g. "UserSeeder". The suffix
	// matters: a bare entity name would read as the record rather than the code
	// that creates it, and the report shows both on one line.
	Name string
	// Apply creates the records and reports the natural key of each record it
	// created and each one that already existed. With dryRun set it reports
	// what it would create and writes nothing.
	Apply func(ctx context.Context, q datastore.Querier, dryRun bool) (created, skipped []string, err error)
}

// Result is what one seeder did, or would do under --dry-run.
type Result struct {
	Name    string
	Created []string
	Skipped []string
}

// All returns every seeder in the order they must run: a seeder may depend on a
// record an earlier one created. The authorization seeder runs first — the
// user seeder grants its default account the administrator role the moment
// the account exists, and the grant names a role row.
func All() []Seeder {
	return []Seeder{Authorization(), User(), UserGroup(), APIKey(), Notification()}
}

// System is the seed a production deployment needs: the data the
// application's own surfaces depend on, with no sample content beside it.
// `initialize` applies it; the development fixtures travel with `All` and
// `migrate:seed` alone.
func System() []Seeder {
	return []Seeder{Authorization()}
}

// Run applies each seeder in order over the same querier.
//
// The caller owns the transaction. That keeps a failed seeder from leaving a
// partial seed behind, and it lets a dry run pass the pool directly because it
// has nothing to roll back.
func Run(ctx context.Context, q datastore.Querier, dryRun bool, list ...Seeder) ([]Result, error) {
	results := make([]Result, 0, len(list))
	for _, seeder := range list {
		created, skipped, err := seeder.Apply(ctx, q, dryRun)
		if err != nil {
			return nil, fmt.Errorf("seeders: %s: %w", seeder.Name, err)
		}
		results = append(results, Result{Name: seeder.Name, Created: created, Skipped: skipped})
	}
	return results, nil
}
