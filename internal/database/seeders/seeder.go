// Package seeders creates the default records a fresh database needs. Every
// seeder is idempotent, so migrate:seed is safe to run repeatedly.
package seeders

import (
	"context"
	"fmt"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/pkg/crypto"
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
// the account exists, and the grant names a role row. The settings seeder
// depends on nothing but the migration, so it closes the list; the
// conformance-suite seeder rides before it — its rows are rehearsal
// fixtures, not settings — and names no record but its own.
//
// All returns every seeder in the order they must run: a seeder may depend on a
// record an earlier one created. The authorization seeder runs first — the
// user seeder grants its default account the administrator role the moment
// the account exists, and the grant names a role row. The settings seeder
// depends on nothing but the migration, so it closes the list.
//
// The JWKS seeder is not in the list: it needs a cipher to seal the private
// half, and a development seed resolves that from the environment. Callers
// that can seal append it through SeedJWKS.
func All() []Seeder {
	return []Seeder{Authorization(), User(), UserGroup(), APIKey(), Notification(), DefaultBucket(), ConformanceSuite(), Settings()}
}

// SeedJWKS returns All plus the JWKS provisioning seeder. It answers All
// unchanged when there is no application secret to seal with — the caller
// then seeds without provisioning, and a later initialize provisions.
func SeedJWKS(cipher *crypto.Cipher, signingAlgorithm string) []Seeder {
	if cipher == nil {
		return All()
	}
	return append(All(), JWKS(cipher, signingAlgorithm))
}

// System is the seed a production deployment needs: the data the
// application's own surfaces depend on, with no sample content beside it.
// `initialize` applies it. The signing key pair is part of that — a
// deployment that ran initialize can sign in. The algorithm is the
// deployment's default signature choice (crypto.DefaultSignatureAlgorithm
// when empty).
func System(cipher *crypto.Cipher, signingAlgorithm string) []Seeder {
	return []Seeder{Authorization(), JWKS(cipher, signingAlgorithm), Settings()}
}

// System is the seed a production deployment needs: the data the
// application's own surfaces depend on, with no sample content beside it.
// `initialize` applies it; the development fixtures travel with `All` and
// `migrate:seed` alone.
//
// The JWKS seeder needs a cipher to seal the private half, so All does not
// include it: the development seed's caller resolves the environment's
// APP_SECRET_KEY and appends it through SeedJWKS.

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
