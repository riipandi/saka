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

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jetify.com/typeid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

// testBucket is the bucket every manager test stages into; newManager seeds
// its row, so the manifest's bucket foreign key is satisfied.
const testBucket = "devbucket"

// migratedPool stands a fresh database up over the manifest schema the
// engine's tests touch, and returns the pool the manager reads and writes
// through. The DDL is the fixture below, not the application's migration:
// a framework test constructs what it needs alone (decision 16), and the
// fixture's agreement with the real schema is pinned by the app-side
// round-trip tests, which run over the migrations themselves.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	_, err = migrationDB.Exec(manifestSchema)
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

// manifestSchema is the manifest store's shape: the two tables the engine's
// reads and writes name, the indexes the manifest scan walks, and the
// updated-at triggers the rows carry.
const manifestSchema = `
CREATE TABLE IF NOT EXISTS public.storage_buckets (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL,
    file_size_limit BIGINT,
    allowed_mime_types TEXT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (name),
    CONSTRAINT chk_storage_buckets_file_size_limit CHECK (file_size_limit IS NULL OR file_size_limit >= 0)
);

CREATE TABLE IF NOT EXISTS public.storage_objects (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    bucket_id UUID NOT NULL REFERENCES public.storage_buckets (id) ON DELETE RESTRICT,
    key TEXT NOT NULL,
    size BIGINT NOT NULL DEFAULT 0,
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    is_private BOOLEAN NOT NULL DEFAULT false,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    staging_size BIGINT NOT NULL DEFAULT 0,
    staging_mtime TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (bucket_id, key),
    CONSTRAINT chk_storage_objects_status CHECK (status IN ('pending', 'ready', 'failed')),
    CONSTRAINT chk_storage_objects_size CHECK (size >= 0)
);

CREATE INDEX IF NOT EXISTS idx_storage_objects_bucket_id ON public.storage_objects (bucket_id);
CREATE INDEX IF NOT EXISTS idx_storage_objects_status ON public.storage_objects (status);
CREATE INDEX IF NOT EXISTS idx_storage_objects_content_hash ON public.storage_objects (content_hash);

CREATE OR REPLACE FUNCTION fn_update_storage_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = clock_timestamp(); RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_storage_buckets_updated_at
    BEFORE UPDATE ON public.storage_buckets
    FOR EACH ROW EXECUTE FUNCTION fn_update_storage_updated_at();

CREATE TRIGGER trg_storage_objects_updated_at
    BEFORE UPDATE ON public.storage_objects
    FOR EACH ROW EXECUTE FUNCTION fn_update_storage_updated_at();
`

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

	keys, err := listedKeys(ctx, store, testBucket)
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/report.txt"}, keys)
}

// TestTheManifestAnswersTheObjectIdInItsWireForm pins the identifier's wire
// form: the manifest's id carries the file TypeID's prefix, and parsing it
// back answers the UUID the column stores.
func TestTheManifestAnswersTheObjectIdInItsWireForm(t *testing.T) {
	manager, _, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("grimoire"), 100)
	require.NoError(t, manager.Stage(ctx, testBucket, "docs/typed.txt", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/docs/typed.txt"))

	manifest, err := manager.Manifest(ctx, testBucket, "docs/typed.txt")
	require.NoError(t, err)
	require.Regexp(t, `^file_[a-z0-9]{26}$`, manifest.ID,
		"the manifest's id carries the object TypeID's prefix")

	typed, err := typeid.Parse[FileID](manifest.ID)
	require.NoError(t, err)

	var rawID string
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(objectsTable)
	sb.Where(sb.Equal("bucket_id", manager.mustBucketID(ctx, testBucket)), sb.Equal("key", "docs/typed.txt"))
	query, args := sb.Build()
	require.NoError(t, manager.db.QueryRow(ctx, query, args...).Scan(&rawID))
	assert.Equal(t, rawID, typed.UUID(), "the typed id carries the column's bytes")
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
	keys, err := listedKeys(ctx, store, testBucket)
	require.NoError(t, err)
	assert.Equal(t, []string{"k"}, keys)
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
	keys, err := listedKeys(ctx, store, testBucket)
	require.NoError(t, err)
	assert.Equal(t, []string{"k"}, keys)
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

	keys, err := listedKeys(ctx, store, testBucket)
	require.NoError(t, err)
	assert.Empty(t, keys)

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
	// the bytes are in the bucket's container, no manifest names them.
	orphan := []byte("orphaned object bytes")
	require.NoError(t, store.Put(ctx, testBucket, "lost", bytes.NewReader(orphan), int64(len(orphan)), "image/png"))

	// A name the engine's key vocabulary cannot name — the deployment's own
	// stray inside the bucket — is not the garbage collection's to sweep.
	foreign := []byte("not the engine's object")
	require.NoError(t, store.Put(ctx, testBucket, "not a key", bytes.NewReader(foreign), int64(len(foreign)), "image/png"))

	// A container the buckets table does not name is never walked: an
	// object left in a retired bucket is not the sweep's to find.
	ghost := []byte("no bucket row names this container")
	require.NoError(t, store.Put(ctx, "ghost", "lost", bytes.NewReader(ghost), int64(len(ghost)), "image/png"))

	removed, err := manager.CollectGarbage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	keys, err := listedKeys(ctx, store, testBucket)
	require.NoError(t, err)
	assert.Equal(t, []string{"k", "not a key"}, keys)
	keys, err = listedKeys(ctx, store, "ghost")
	require.NoError(t, err)
	assert.Equal(t, []string{"lost"}, keys)
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

// listedKeys reads one bucket's own listing, the view the garbage
// collection walks.
func listedKeys(ctx context.Context, store *FS, bucket string) ([]string, error) {
	var keys []string
	err := store.List(ctx, bucket, func(key string) error {
		keys = append(keys, key)
		return nil
	})
	return keys, err
}
