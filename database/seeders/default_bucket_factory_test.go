package seeders_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/database/seeders"
)

// TestDefaultBucketSeederCreatesTheBucket pins what a fresh database gains:
// one `default` bucket, unlimited and accepting any content type.
func TestDefaultBucketSeederCreatesTheBucket(t *testing.T) {
	pool := newSeededPool(t)

	results, err := seeders.Run(t.Context(), pool, false, seeders.DefaultBucket())
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, []string{seeders.DefaultBucketName}, results[0].Created)
	assert.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM public.storage_buckets`))

	var sizeLimit *int64
	var mimeTypes *[]string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT file_size_limit, allowed_mime_types FROM public.storage_buckets WHERE name = $1`,
		seeders.DefaultBucketName).Scan(&sizeLimit, &mimeTypes))
	assert.Nil(t, sizeLimit)
	assert.Nil(t, mimeTypes)
}

// TestDefaultBucketSeederIsIdempotent runs the seeder twice: the second run
// creates nothing — an existing bucket row is configuration the seeder never
// overwrites.
func TestDefaultBucketSeederIsIdempotent(t *testing.T) {
	pool := newSeededPool(t)

	_, err := seeders.Run(t.Context(), pool, false, seeders.DefaultBucket())
	require.NoError(t, err)

	results, err := seeders.Run(t.Context(), pool, false, seeders.DefaultBucket())
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Empty(t, results[0].Created)
	assert.Equal(t, []string{seeders.DefaultBucketName}, results[0].Skipped)
	assert.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM public.storage_buckets`))
}

// TestDefaultBucketSeedsWithTheRest pins the seam the commands use: All
// carries the seeder for migrate:seed, so a development database is never
// without the bucket the default setting names.
func TestDefaultBucketSeedsWithTheRest(t *testing.T) {
	pool := newSeededPool(t)

	results := runSeeders(t, pool, false)

	names := make([]string, 0, len(results))
	for _, result := range results {
		names = append(names, result.Name)
	}
	assert.Contains(t, names, seeders.DefaultBucketSeederName)
	// The literal pins the seeded name itself: a constant renamed without the
	// row would fail loudly here.
	assert.Equal(t, 1, countRows(t, pool,
		`SELECT count(*) FROM public.storage_buckets WHERE name = 'devbucket'`))
}

// TestDefaultBucketSeederDryRunReportsWithoutWriting pins the dry run: the
// report names the bucket an apply would create, and the table stays empty.
func TestDefaultBucketSeederDryRunReportsWithoutWriting(t *testing.T) {
	pool := newSeededPool(t)

	results, err := seeders.Run(t.Context(), pool, true, seeders.DefaultBucket())
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, []string{seeders.DefaultBucketName}, results[0].Created)
	assert.Zero(t, countRows(t, pool, `SELECT count(*) FROM public.storage_buckets`))

	// Once the bucket rests, the same dry run reports it as skipped.
	_, err = seeders.Run(t.Context(), pool, false, seeders.DefaultBucket())
	require.NoError(t, err)

	results, err = seeders.Run(t.Context(), pool, true, seeders.DefaultBucket())
	require.NoError(t, err)
	assert.Empty(t, results[0].Created)
	assert.Equal(t, []string{seeders.DefaultBucketName}, results[0].Skipped)
}
