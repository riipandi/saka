package storage

import (
	"bytes"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/pkg/testutils"
)

// s3TestBucket is the bucket the object-store tests run through — the name
// the compose file provisions on the shared Silo and the tests create on
// their own throwaway container.
const s3TestBucket = "devbucket"

// newS3Manager builds the engine over a Silo container and a fresh test
// database, with the physical bucket created and the row seeded. The
// container is shared per test binary; the database and the bucket are the
// test's own.
func newS3Manager(t *testing.T) *Manager {
	t.Helper()

	store4 := testutils.StartMinIO(t.Context(), t)
	pool := migratedPool(t)
	seedBucket(t, pool, s3TestBucket)

	driver, err := NewS3(config.S3{
		AccessKey:      store4.AccessKey,
		SecretKey:      store4.Secret,
		EndpointURL:    store4.Endpoint,
		Region:         "auto",
		ForcePathStyle: true,
	})
	require.NoError(t, err)

	manager := NewManager(driver, pool, t.TempDir(), slog.New(slog.DiscardHandler))
	require.NoError(t, manager.EnsureBucket(t.Context(), s3TestBucket))
	return manager
}

func TestTheS3DriverMirrorsTheLocalRound(t *testing.T) {
	manager := newS3Manager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("silo round"), 40)
	require.NoError(t, manager.Stage(ctx, s3TestBucket, "docs/silo.txt", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, s3TestBucket+"/docs/silo.txt"))

	manifest, err := manager.Manifest(ctx, s3TestBucket, "docs/silo.txt")
	require.NoError(t, err)
	assert.Equal(t, StatusReady, manifest.Status)
	assert.Equal(t, int64(len(data)), manifest.Size)
	assert.Equal(t, hashOf(data), manifest.ContentHash)

	reader, err := manager.Open(ctx, s3TestBucket, "docs/silo.txt")
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, data, got)

	// The replace-whole round and the delete land the same way they do on
	// the local driver: the object store answers the same contract.
	changed := append(bytes.Clone(data), []byte("-second")...)
	require.NoError(t, manager.Stage(ctx, s3TestBucket, "docs/silo.txt", bytes.NewReader(changed), nil))
	require.NoError(t, manager.Sync(ctx, s3TestBucket+"/docs/silo.txt"))

	reader, err = manager.Open(ctx, s3TestBucket, "docs/silo.txt")
	require.NoError(t, err)
	got, err = io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, changed, got)

	require.NoError(t, manager.Delete(ctx, s3TestBucket, "docs/silo.txt"))
	_, err = manager.Open(ctx, s3TestBucket, "docs/silo.txt")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestTheS3DriverSweepsItsOwnBucket(t *testing.T) {
	manager := newS3Manager(t)
	ctx := t.Context()

	data := []byte("kept")
	require.NoError(t, manager.Stage(ctx, s3TestBucket, "k", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, s3TestBucket+"/k"))

	// An orphan in the same physical bucket, and a foreign name the key
	// vocabulary cannot carry: the sweep removes the first and spares the
	// second.
	orphan := []byte("orphaned")
	require.NoError(t, manager.store.Put(ctx, s3TestBucket, "lost", bytes.NewReader(orphan), int64(len(orphan)), "text/plain"))
	foreign := []byte("not the engine's object")
	require.NoError(t, manager.store.Put(ctx, s3TestBucket, "not a key", bytes.NewReader(foreign), int64(len(foreign)), "text/plain"))

	removed, err := manager.CollectGarbage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	_, err = manager.Open(ctx, s3TestBucket, "k")
	require.NoError(t, err)
}

func TestEnsureBucketIsIdempotentAndValidatesTheName(t *testing.T) {
	manager := newS3Manager(t)

	// The container the harness created is already there; a second call is
	// the state the caller asked for.
	assert.NoError(t, manager.EnsureBucket(t.Context(), s3TestBucket))
	assert.ErrorIs(t, manager.EnsureBucket(t.Context(), "../escape"), ErrInvalidKey)
}
