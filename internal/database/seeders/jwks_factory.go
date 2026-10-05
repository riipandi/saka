package seeders

import (
	"context"
	"fmt"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/pkg/crypto"
)

// JWKSKeyPairSeederName is the name this seeder reports under.
const JWKSKeyPairSeederName = "JWKSKeyPairSeeder"

// JWKS returns the seeder that provisions the database's first signing key
// pair. The database is the one signing authority — the environment carries
// no key-pair material — and a fresh database starts with none, so the seed
// writes one row rather than leaving sign-in to fail closed.
//
// cipher must be non-nil: a run without the auth secret cannot seal the
// private half, and a plaintext private key must never rest in the table.
// A key that already rests (any active row) skips the provisioning — the
// seeder never rotates or replaces what is there; jwks:generate does.
func JWKS(cipher *crypto.Cipher, algorithm string) Seeder {
	return Seeder{
		Name:  JWKSKeyPairSeederName,
		Apply: applyJWKS(cipher, algorithm),
	}
}

// applyJWKS writes the first signing row when the table holds none. The
// pair itself is minted by the jwks package's shared builder, so the
// seeder, the rotation command, and the auto-invalidation write the same
// row shape.
func applyJWKS(cipher *crypto.Cipher, algorithm string) func(context.Context, datastore.Querier, bool) ([]string, []string, error) {
	return func(ctx context.Context, q datastore.Querier, dryRun bool) (created, skipped []string, err error) {
		created = []string{}

		count, err := countJWKSRows(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		if count > 0 {
			skipped = append(skipped, "existing signing key")
		} else {
			if dryRun {
				created = append(created, "jwk (first key pair)")
			} else {
				pair, err := jwks.GeneratePairWith(algorithm, cipher)
				if err != nil {
					return nil, nil, err
				}
				if err := insertJWKSRow(ctx, q, pair); err != nil {
					return nil, nil, err
				}
				created = append(created, pair.KeyID)
			}
		}

		// The Config OP profile the certification runs asks the discovery
		// document to name RS256 beside the primary algorithm — the RSA
		// pair rides beside the first key when the deployment's own
		// algorithm is not RSA's. A deployment that provisions RS256 as
		// its primary skips the second row.
		if algorithm != jwksAlgorithmRS256 {
			rsRows, err := countJWKSRowsWithAlgorithm(ctx, q, jwksAlgorithmRS256)
			if err != nil {
				return nil, nil, err
			}
			if rsRows == 0 {
				if dryRun {
					created = append(created, "jwk (RS256 pair)")
				} else {
					pair, err := jwks.GeneratePairWith(jwksAlgorithmRS256, cipher)
					if err != nil {
						return nil, nil, err
					}
					if err := insertJWKSRow(ctx, q, pair); err != nil {
						return nil, nil, err
					}
					created = append(created, pair.KeyID)
				}
			} else {
				skipped = append(skipped, "existing RS256 key")
			}
		}
		return created, skipped, nil
	}
}

// jwksAlgorithmRS256 is the RSA signing algorithm the Config OP profile
// requires the discovery document to name.
const jwksAlgorithmRS256 = "RS256"

// countJWKSRowsWithAlgorithm answers how many signing keys of one
// algorithm the table holds — active or retired, the same rule the count
// guard above applies.
func countJWKSRowsWithAlgorithm(ctx context.Context, q datastore.Querier, algorithm string) (int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(jwks.TableJWKS)
	sb.Where(sb.Equal("algorithm", algorithm))

	query, args := sb.Build()
	var total int64
	if err := q.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count jwks rows: %w", err)
	}
	return total, nil
}

// countJWKSRows answers how many signing keys the table holds. Any row —
// active or retired — counts: a retired one means the deployment already
// provisioned, and a second provisioning would mint a key the rotation
// history never named.
func countJWKSRows(ctx context.Context, q datastore.Querier) (int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(jwks.TableJWKS)

	query, args := sb.Build()
	var total int64
	if err := q.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count jwks rows: %w", err)
	}
	return total, nil
}

// insertJWKSRow stores one provisioned pair. The conflict is a safety net,
// not the idempotency: the count guard above decides, and a race that
// inserts twice would mint two first keys — refused rather than resolved.
func insertJWKSRow(ctx context.Context, q datastore.Querier, pair jwks.ProvisionedPair) error {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(jwks.TableJWKS)
	sb.Cols("key_id", "algorithm", "key_type", "public_key", "private_key", "seal_fp", "use_for", "is_active")
	sb.Values(pair.KeyID, pair.Algorithm, pair.KeyType, []byte(pair.PublicKey), []byte(pair.SealedKey), pair.SealFP, jwks.UseSignature, true)

	query, args := sb.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("store signing key pair: %w", err)
	}
	return nil
}

// InsertJWKSRow is the command-facing insert: jwks:generate stages a
// rotation pair through it, so a manual row and a seeded row agree on
// shape and fingerprint.
func InsertJWKSRow(ctx context.Context, q datastore.Querier, pair jwks.ProvisionedPair) error {
	return insertJWKSRow(ctx, q, pair)
}
