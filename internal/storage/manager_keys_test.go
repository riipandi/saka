package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/datastore"
)

func TestKeyComposesFlexibleSegments(t *testing.T) {
	// The store is multi-purpose: the first part names the purpose, the
	// rest is the feature's own naming — the engine only guarantees each
	// part lands as one safe segment.
	key, err := Key("avatar", "usr_123", "128.png")
	require.NoError(t, err)
	assert.Equal(t, "avatar/usr_123/128.png", key)

	key, err = Key("user-files", "usr_123", "Q4 report final.pdf")
	require.NoError(t, err)
	assert.Equal(t, "user-files/usr_123/Q4_report_final.pdf", key,
		"spaces and unsafe characters become underscores, never separators")
}

func TestKeyRefusesTheShapesATraversalIsMadeOf(t *testing.T) {
	_, err := Key("docs", "..", "etc")
	assert.ErrorIs(t, err, ErrInvalidKey)

	_, err = Key("")
	assert.ErrorIs(t, err, ErrInvalidKey)

	_, err = Key("   ")
	assert.ErrorIs(t, err, ErrInvalidKey, "a part the sanitizer empties is an error, not an unnamed file")
}

func TestValidateKeyRejectsTraversalAndHiddenSegments(t *testing.T) {
	for _, key := range []string{
		"../../etc/passwd",
		"/absolute",
		"docs/./report",
		"docs/../report",
		"docs/.hidden",
		"docs//report",
		`docs\report`,
		"",
	} {
		assert.ErrorIs(t, ValidateKey(key), ErrInvalidKey, "key %q must be refused", key)
	}
	for _, key := range []string{"docs/report.txt", "avatar/usr_1/128.png", "single"} {
		assert.NoError(t, ValidateKey(key), "key %q must be accepted", key)
	}
}

func TestValidateBucketNameRejectsTheShapesANamespaceCannotCarry(t *testing.T) {
	for _, name := range []string{
		"../../etc",
		"/absolute",
		"Docs",
		"docs/.hidden",
		"staging",
		"logs",
		"backup",
		"config",
		"files",
		"",
	} {
		assert.Error(t, ValidateBucketName(name), "bucket name %q must be refused", name)
	}
	for _, name := range []string{"default", "user-files", "avatars_2"} {
		assert.NoError(t, ValidateBucketName(name), "bucket name %q must be accepted", name)
	}
}

func TestSplitRefParsesABucketScopedReference(t *testing.T) {
	bucket, key, err := SplitRef("default/avatars/usr_1.png")
	require.NoError(t, err)
	assert.Equal(t, "default", bucket)
	assert.Equal(t, "avatars/usr_1.png", key)

	_, _, err = SplitRef("nokey")
	assert.Error(t, err, "a reference with no key segment is not a reference")
	_, _, err = SplitRef("/leading")
	assert.Error(t, err)
	_, _, err = SplitRef("default/../escape")
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestManagerRefusesAnInvalidKeyBeforeTouchingAnything(t *testing.T) {
	manager, _, _ := newManager(t)
	ctx := t.Context()

	// The staging path is built by joining the bucket and the key; a
	// traversal in either must die at the door, not walk out of the
	// staging directory.
	assert.ErrorIs(t, manager.Stage(ctx, testBucket, "../escape", bytes.NewReader([]byte("x")), nil), ErrInvalidKey)
	assert.ErrorIs(t, manager.Sync(ctx, testBucket+"/../escape"), ErrInvalidKey)
	assert.ErrorIs(t, manager.Delete(ctx, testBucket, "../escape"), ErrInvalidKey)
	assert.ErrorIs(t, manager.UpdateMetadata(ctx, testBucket, "../escape", nil), ErrInvalidKey)
	assert.ErrorIs(t, manager.Stage(ctx, "../escape", "k", bytes.NewReader([]byte("x")), nil), ErrInvalidKey)

	_, err := manager.Open(ctx, testBucket, "../escape")
	assert.ErrorIs(t, err, ErrNotFound, "a read of an invalid key finds nothing, not a traversal")
}

func TestManagerCarriesAndRewritesMetadata(t *testing.T) {
	manager, _, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("meta"), 40)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(data), map[string]any{
		"content_type": "application/pdf",
		"original":     "q4 report.pdf",
		"owner_id":     "usr_123",
	}))
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	// The metadata survives the upload: it was written at Stage time and
	// the manifest commits carried it through.
	manifest, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, "application/pdf", manifest.Metadata["content_type"])
	assert.Equal(t, "q4 report.pdf", manifest.Metadata["original"])

	// A rewrite touches only the metadata; the status and the content hash
	// are exactly as they were.
	require.NoError(t, manager.UpdateMetadata(ctx, testBucket, "k", map[string]any{
		"content_type": "application/pdf",
		"original":     "renamed.pdf",
	}))
	after, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, manifest.ContentHash, after.ContentHash)
	assert.Equal(t, "renamed.pdf", after.Metadata["original"])
	assert.Equal(t, StatusReady, after.Status)
}

func TestManagerManifestFollowsTheUpload(t *testing.T) {
	manager, _, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("progress"), 40)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(data), nil))

	// While the file still waits in staging, the row Stage created reports
	// pending — the status reader's before picture.
	before, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, before.Status)
	assert.Equal(t, int64(len(data)), before.Size)

	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	after, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, StatusReady, after.Status)
	assert.Equal(t, int64(len(data)), after.Size)
}

func TestManagerRetryReusesTheCheckpointedHash(t *testing.T) {
	// The checkpoint is what makes a retry of a large file cheap: a
	// pending manifest whose staging fingerprint matches is trusted for
	// its content hash, and the sync goes straight to the PUT.
	manager, _, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("checkpoint"), 40)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(data), nil))

	// The sync "dies" after the checkpoint but before its upload: the
	// manifest is pending, the backend has nothing, the staging file sits.
	checkpoint := File{
		BucketID:     manager.mustBucketID(ctx, testBucket),
		Bucket:       testBucket,
		Key:          "k",
		ContentHash:  hashOf(data),
		Status:       StatusPending,
		StagingSize:  int64(len(data)),
		StagingMtime: mtimeOf(manager, testBucket, "k"),
	}
	require.NoError(t, manager.db.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		return manager.manifests.Save(ctx, tx, checkpoint)
	}))

	// The retry finds the fingerprint unchanged: the stored hash is
	// reused and the file lands ready.
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	manifest, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, StatusReady, manifest.Status)
	assert.Equal(t, hashOf(data), manifest.ContentHash)

	reader, err := manager.Open(ctx, testBucket, "k")
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, data, got)
}

func TestStorageObjectsRefuseAnUnknownStatus(t *testing.T) {
	// The CHECK constraint is the database's own word for the status
	// vocabulary; this test keeps it from drifting away in a migration.
	pool := migratedPool(t)
	seedBucket(t, pool, testBucket)
	ctx := t.Context()

	_, err := pool.Exec(ctx,
		`INSERT INTO storage_objects (bucket_id, key, content_hash, status)
		 VALUES ((SELECT id FROM storage_buckets WHERE name = 'default'), 'k', '', 'archived')`)
	require.Error(t, err, "a status outside the CHECK list must be refused")
	assert.Contains(t, err.Error(), "chk_storage_objects_status")
}

func TestStagingMtimeRoundTrips(t *testing.T) {
	// The fingerprint reuse compares what the database stored against what
	// the file system reports, so a round trip through Postgres must not
	// lose the mtime's precision.
	manager, _, _ := newManager(t)
	ctx := t.Context()

	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader([]byte("x")), nil))
	manifest, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.False(t, manifest.StagingMtime.IsZero())
	assert.WithinDuration(t, time.Now(), manifest.StagingMtime, time.Minute)
}

func TestStagingMtimeIsTruncatedToTheColumnPrecision(t *testing.T) {
	// `timestamptz` keeps microseconds, and the file system may report
	// nanoseconds: the fingerprint is truncated on the way in, so the value
	// a retry compares against is the value the database can hold back.
	manager, _, _ := newManager(t)
	ctx := t.Context()

	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader([]byte("x")), nil))
	manifest, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, manifest.StagingMtime.Truncate(time.Microsecond), manifest.StagingMtime,
		"the stored fingerprint carries no sub-microsecond residue")
}

func TestRestageClearsTheStoredContentHash(t *testing.T) {
	// The re-stage's fingerprint is the new file's, so the row's hash — the
	// previous version's — must not survive it: a sync that found a matching
	// fingerprint would otherwise skip hashing and store the new bytes under
	// the old digest.
	manager, _, _ := newManager(t)
	ctx := t.Context()

	first := bytes.Repeat([]byte("first"), 40)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(first), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	second := bytes.Repeat([]byte("second"), 40)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(second), nil))

	staged, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, staged.Status)
	assert.Empty(t, staged.ContentHash,
		"a re-stage must not leave the previous version's hash beside the new file")

	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))
	final, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, hashOf(second), final.ContentHash)
	assert.NotEqual(t, hashOf(first), final.ContentHash)

	reader, err := manager.Open(ctx, testBucket, "k")
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, second, got)
}

// mtimeOf reads the staging fingerprint a checkpoint stores.
func mtimeOf(m *Manager, bucket, key string) time.Time {
	info, err := os.Stat(m.stagingPath(bucket, key))
	if err != nil {
		panic(err)
	}
	return info.ModTime()
}
