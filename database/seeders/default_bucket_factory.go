package seeders

import (
	"context"
	"errors"
	"fmt"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
)

// DefaultBucketSeederName is the name this seeder reports under.
const DefaultBucketSeederName = "DefaultBucketSeeder"

// DefaultBucketName is the bucket every storage feature writes into until an
// administrator moves the default selection: the avatar and OIDC-logo keys
// the engine carries today live under it.
const DefaultBucketName = "devbucket"

// DefaultBucket returns the seeder that creates the default storage bucket.
// It runs with the rest of the seed so a fresh database answers the
// `storage.default_bucket` setting with a bucket that exists.
func DefaultBucket() Seeder {
	return Seeder{
		Name:  DefaultBucketSeederName,
		Apply: applyDefaultBucket,
	}
}

// applyDefaultBucket inserts the default bucket row, unlimited and
// accepting any content type. The conflict is the idempotency: an existing
// row is an administrator's configuration the seeder never overwrites.
func applyDefaultBucket(ctx context.Context, q datastore.Querier, dryRun bool) (created, skipped []string, err error) {
	created, skipped = []string{}, []string{}

	if dryRun {
		exists, existsErr := bucketRests(ctx, q, DefaultBucketName)
		if existsErr != nil {
			return nil, nil, existsErr
		}
		if exists {
			skipped = append(skipped, DefaultBucketName)
		} else {
			created = append(created, DefaultBucketName)
		}
		return created, skipped, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableStorageBuckets)
	ib.Cols("name")
	ib.Values(DefaultBucketName)
	ib.SQL("ON CONFLICT (name) DO NOTHING RETURNING name")

	query, args := ib.Build()
	var name string
	scanErr := q.QueryRow(ctx, query, args...).Scan(&name)
	switch {
	case scanErr == nil:
		created = append(created, name)
	case errors.Is(scanErr, pgx.ErrNoRows):
		skipped = append(skipped, DefaultBucketName)
	default:
		return nil, nil, fmt.Errorf("seed default bucket: %w", scanErr)
	}
	return created, skipped, nil
}

// bucketRests answers whether the table holds a row for the name — the dry
// run's stand-in for the conflict the live insert resolves.
func bucketRests(ctx context.Context, q datastore.Querier, name string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1").From(entity.TableStorageBuckets).Where(sb.Equal("name", name))

	query, args := sb.Build()
	var one int
	err := q.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("seed default bucket: %s: %w", name, err)
	}
	return true, nil
}
