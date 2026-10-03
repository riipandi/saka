package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/database"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

// testBucket is the bucket every manager test stages into; newManager seeds
// its row, so the manifest's bucket foreign key is satisfied.
const testBucket = "default"

// migratedPool applies the migrations to a fresh test database and returns
// the pool the manager's manifest reads and writes go through.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := database.NewMigrator(t.Context(), migrationDB, database.MigratorOptions{})
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "storage_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })
	return pool
}

// seedBucket writes a bucket row a test's manifests can scope to.
func seedBucket(t *testing.T, pool *datastore.Postgres, name string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO storage_buckets (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`, name)
	require.NoError(t, err)
}

// newManager builds the engine over a throwaway data directory and a
// throwaway staging directory.
func newManager(t *testing.T) (*Manager, *FS, string) {
	t.Helper()

	pool := migratedPool(t)
	seedBucket(t, pool, testBucket)
	store := NewFS(t.TempDir())
	staging := t.TempDir()
	manager := NewManager(store, pool, staging, slog.New(slog.DiscardHandler))
	return manager, store, staging
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestManagerSyncStoresTheManifestAndDropsTheStagingFile(t *testing.T) {
	manager, store, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("grimoire"), 100)
	require.NoError(t, manager.Stage(ctx, testBucket, "docs/report.txt", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/docs/report.txt"))

	// The staging file has served its purpose: the bytes are stored whole
	// under the key and the manifest is committed.
	_, err := os.Stat(manager.stagingPath(testBucket, "docs/report.txt"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "the staging file must be gone after a sync")

	manifest, err := manager.manifests.Load(ctx, manager.db, manager.mustBucketID(ctx, testBucket), "docs/report.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(len(data)), manifest.Size)
	assert.Equal(t, StatusReady, manifest.Status)
	assert.Equal(t, hashOf(data), manifest.ContentHash)
	assert.Equal(t, testBucket, manifest.Bucket)

	// The backend holds the file whole under the bucket-scoped path, never
	// under a chunk-shaped name.
	reader, err := manager.Open(ctx, testBucket, "docs/report.txt")
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, data, got)

	paths, err := listedKeys(ctx, store)
	require.NoError(t, err)
	assert.Equal(t, []string{testBucket + "/docs/report.txt"}, paths)
}

func TestManagerSyncOfTheSameBytesIsAShortCircuit(t *testing.T) {
	manager, store, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("stable"), 50)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	// The second sync reads the same bytes: the content hash matches, so
	// the answer is still a stored file — nothing needed re-uploading.
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	reader, err := manager.Open(ctx, testBucket, "k")
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, data, got)
	paths, err := listedKeys(ctx, store)
	require.NoError(t, err)
	assert.Equal(t, []string{testBucket + "/k"}, paths)
}

func TestManagerReuploadReplacesTheObjectWhole(t *testing.T) {
	manager, store, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("A"), 96)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	changed := bytes.Clone(data)
	changed[40] = 'B'
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(changed), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	// A change is a whole replacement: the backend names no old version,
	// and the read answers with the newest bytes.
	manifest, err := manager.manifests.Load(ctx, manager.db, manager.mustBucketID(ctx, testBucket), "k")
	require.NoError(t, err)
	assert.Equal(t, hashOf(changed), manifest.ContentHash)

	reader, err := manager.Open(ctx, testBucket, "k")
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, changed, got)
	assert.False(t, bytes.Equal(got, data))

	// Exactly one object answers the key tree — the old version did not
	// survive under a second name.
	paths, err := listedKeys(ctx, store)
	require.NoError(t, err)
	assert.Equal(t, []string{testBucket + "/k"}, paths)
}

func TestManagerDeleteRemovesTheObjectAndTheRow(t *testing.T) {
	manager, store, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("vanish"), 32)
	require.NoError(t, manager.Stage(ctx, testBucket, "left", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/left"))

	require.NoError(t, manager.Delete(ctx, testBucket, "left"))

	_, err := manager.Open(ctx, testBucket, "left")
	assert.ErrorIs(t, err, ErrNotFound)

	paths, err := listedKeys(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, paths)

	_, err = manager.manifests.Load(ctx, manager.db, manager.mustBucketID(ctx, testBucket), "left")
	assert.ErrorIs(t, err, ErrNoManifest)
}

func TestManagerRefusesAStageIntoAnUnknownBucket(t *testing.T) {
	manager, _, _ := newManager(t)

	err := manager.Stage(t.Context(), "nowhere", "k", bytes.NewReader([]byte("x")), nil)
	assert.ErrorIs(t, err, ErrNotFound, "an unknown bucket is refused, never created on the fly")
}

func TestManagerCollectGarbageRemovesOnlyUnreferencedObjects(t *testing.T) {
	manager, store, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("gced"), 32)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	// A delete that finished its manifest rows but not its object removal:
	// the bytes are in the backend, no manifest names them.
	orphan := []byte("orphaned object bytes")
	require.NoError(t, store.Put(ctx, "orphans/lost", bytes.NewReader(orphan), int64(len(orphan)), "image/png"))

	// A foreign object the engine's key vocabulary cannot name: a bucket
	// shared with another tenant, or the deployment's own stray, is not
	// the garbage collection's to sweep.
	foreign := []byte("not the engine's object")
	require.NoError(t, store.Put(ctx, "Not a key/with spaces", bytes.NewReader(foreign), int64(len(foreign)), "image/png"))

	removed, err := manager.CollectGarbage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	paths, err := listedKeys(ctx, store)
	require.NoError(t, err)
	assert.Equal(t, []string{"Not a key/with spaces", testBucket + "/k"}, paths)
}

func TestManagerSyncWithoutAStagingFileIsQuiet(t *testing.T) {
	manager, _, _ := newManager(t)

	assert.NoError(t, manager.Sync(t.Context(), testBucket+"/never-staged"))
}

func TestManagerSyncStopsHashingWhenTheContextEnds(t *testing.T) {
	// The hash pass is a plain `io.Copy` over the whole file, so the
	// context is the only thing that can stop it: a cancelled sync must
	// fail before the checkpoint, leaving the manifest as the stage wrote
	// it and the staging file in place for the retry.
	manager, _, _ := newManager(t)

	data := bytes.Repeat([]byte("deadline"), 200)
	require.NoError(t, manager.Stage(t.Context(), testBucket, "k", bytes.NewReader(data), nil))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := manager.Sync(ctx, testBucket+"/k")
	assert.ErrorIs(t, err, context.Canceled)

	manifest, err := manager.Manifest(t.Context(), testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, manifest.Status)
	assert.Empty(t, manifest.ContentHash, "the hash never finished, so nothing was recorded")
	assert.FileExists(t, manager.stagingPath(testBucket, "k"))
}

// mustBucketID resolves a bucket name the test fixtures need; a missing
// bucket is a broken fixture, so it panics.
func (m *Manager) mustBucketID(ctx context.Context, name string) string {
	id, err := m.resolveBucket(ctx, name)
	if err != nil {
		panic(err)
	}
	return id
}

// listedKeys reads the backend's own listing, the view the garbage
// collection walks.
func listedKeys(ctx context.Context, store *FS) ([]string, error) {
	var keys []string
	err := store.List(ctx, func(key string) error {
		keys = append(keys, key)
		return nil
	})
	return keys, err
}
