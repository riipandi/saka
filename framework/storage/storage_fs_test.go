package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fsTestBucket is the bucket the local-backend tests write into; the key is
// the file's own naming inside it.
const fsTestBucket = "pictures"

// newFSStore builds a local backend over a throwaway directory and returns
// a key with known bytes, the shape every store test runs through.
func newFSStore(t *testing.T) (*FS, string, []byte) {
	t.Helper()

	root := t.TempDir()
	data := []byte("a whole file, stored once under the key its feature composed")
	return NewFS(root), "usr_1.png", data
}

func TestFSStoreRoundTripsAFile(t *testing.T) {
	store, key, data := newFSStore(t)
	ctx := t.Context()

	keys, err := listedKeys(ctx, store, fsTestBucket)
	require.NoError(t, err)
	assert.Empty(t, keys)

	require.NoError(t, store.Put(ctx, fsTestBucket, key, bytes.NewReader(data), int64(len(data)), "image/png"))

	reader, err := store.Get(ctx, fsTestBucket, key)
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, data, got)

	keys, err = listedKeys(ctx, store, fsTestBucket)
	require.NoError(t, err)
	assert.Equal(t, []string{key}, keys)

	require.NoError(t, store.Delete(ctx, fsTestBucket, key))
	keys, err = listedKeys(ctx, store, fsTestBucket)
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestFSStorePutReplacesWhole(t *testing.T) {
	store, key, data := newFSStore(t)
	ctx := t.Context()

	require.NoError(t, store.Put(ctx, fsTestBucket, key, bytes.NewReader(data), int64(len(data)), "image/png"))
	changed := append(bytes.Clone(data), []byte("-changed")...)
	require.NoError(t, store.Put(ctx, fsTestBucket, key, bytes.NewReader(changed), int64(len(changed)), "image/png"))

	reader, err := store.Get(ctx, fsTestBucket, key)
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, changed, got)
}

func TestFSStoreGetMissingFileIsNotFound(t *testing.T) {
	store, key, _ := newFSStore(t)

	_, err := store.Get(t.Context(), fsTestBucket, key)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestFSStoreDeleteMissingFileIsQuiet(t *testing.T) {
	store, key, _ := newFSStore(t)

	assert.NoError(t, store.Delete(t.Context(), fsTestBucket, key))
}

func TestFSStoreListSkipsTempFilesAndStaysInTheBucket(t *testing.T) {
	store, key, data := newFSStore(t)
	ctx := t.Context()

	require.NoError(t, store.Put(ctx, fsTestBucket, key, bytes.NewReader(data), int64(len(data)), "image/png"))
	// A temp file shares the file's directory: a crash's leftover, which
	// the scan must not name, because only real files may be deleted. The
	// backend writes one under the key's own basename, so the fixture does
	// too.
	require.NoError(t, os.WriteFile(
		filepath.Join(store.root, uploadsDir, fsTestBucket, ".usr_1.png.tmp"),
		[]byte("x"), 0o600))

	// The engine's own subtrees live beside the uploads container; the
	// listing walks one bucket's subtree only, so nothing inside them is
	// ever named — the garbage collection could not sweep a staging file
	// or a log even by accident.
	for _, dir := range []string{"staging", "logs", "backup", "config"} {
		require.NoError(t, os.MkdirAll(filepath.Join(store.root, dir, fsTestBucket), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(store.root, dir, fsTestBucket, "private.bin"), []byte("x"), 0o600))
	}

	keys, err := listedKeys(ctx, store, fsTestBucket)
	require.NoError(t, err)
	assert.Equal(t, []string{key}, keys)

	// A bucket whose subtree does not exist lists as empty, not an error.
	keys, err = listedKeys(ctx, store, "nowhere")
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestFSStoreDeleteKeepsTheBucketDirectory(t *testing.T) {
	store, key, data := newFSStore(t)
	ctx := t.Context()

	require.NoError(t, store.Put(ctx, fsTestBucket, key, bytes.NewReader(data), int64(len(data)), "image/png"))
	require.NoError(t, store.Delete(ctx, fsTestBucket, key))

	// The emptied file subtree goes, but the bucket's own directory stays:
	// an empty bucket is still a bucket.
	assert.NoDirExists(t, filepath.Join(store.root, uploadsDir, fsTestBucket, "usr_1"))
	assert.DirExists(t, filepath.Join(store.root, uploadsDir, fsTestBucket))
}

func TestFSStoreRoundTripsANestedKey(t *testing.T) {
	store, _, data := newFSStore(t)
	ctx := t.Context()
	key := "sub/nested/usr_2.png"

	require.NoError(t, store.Put(ctx, fsTestBucket, key, bytes.NewReader(data), int64(len(data)), "image/png"))

	reader, err := store.Get(ctx, fsTestBucket, key)
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, data, got)

	// The listing names the nested key relative to the bucket, the shape
	// the rest of the contract speaks.
	keys, err := listedKeys(ctx, store, fsTestBucket)
	require.NoError(t, err)
	assert.Equal(t, []string{key}, keys)

	// The delete empties the nested subtree's skeleton up to the bucket
	// root, which stays — an empty bucket is still a bucket.
	require.NoError(t, store.Delete(ctx, fsTestBucket, key))
	assert.NoDirExists(t, filepath.Join(store.root, uploadsDir, fsTestBucket, "sub"))
	assert.DirExists(t, filepath.Join(store.root, uploadsDir, fsTestBucket))
}

// TestFSStoreTheTraversalKeyCannotEscape pins the rooted fence: a key that
// spells a path out of the bucket is refused by the FS operations themselves
// — the fence below the manager's validation contract — and nothing lands
// outside the bucket whatever the caller handed over.
func TestFSStoreTheTraversalKeyCannotEscape(t *testing.T) {
	store, _, data := newFSStore(t)
	ctx := t.Context()

	// One round trip first, so the bucket directory exists and the delete
	// exercises the rooted Remove rather than the absent-bucket answer,
	// plus a real nested file: the traversal refusal is what a name whose
	// prefix components exist earns — a first component that is absent
	// answers not-found before the walk ever reaches the escape, which is
	// the delete's own quiet state and still touches nothing outside.
	require.NoError(t, store.Put(ctx, fsTestBucket, "usr_1.png", bytes.NewReader(data), int64(len(data)), "image/png"))
	require.NoError(t, store.Put(ctx, fsTestBucket, "sub/keep.png", bytes.NewReader(data), int64(len(data)), "image/png"))

	for _, key := range []string{"../escape.png", "sub/../../escape.png", ".."} {
		_, err := store.Get(ctx, fsTestBucket, key)
		assert.Error(t, err, "Get "+key)
		assert.Error(t, store.Put(ctx, fsTestBucket, key, bytes.NewReader(data), int64(len(data)), "image/png"), "Put "+key)
		assert.Error(t, store.Delete(ctx, fsTestBucket, key), "Delete "+key)
	}

	// The escape targets — the uploads container itself and the data
	// directory above it — hold nothing the traversal wrote, and the
	// bucket's own content is untouched.
	assert.NoFileExists(t, filepath.Join(store.root, uploadsDir, "escape.png"))
	assert.NoFileExists(t, filepath.Join(store.root, "escape.png"))

	keys, err := listedKeys(ctx, store, fsTestBucket)
	require.NoError(t, err)
	assert.Equal(t, []string{"sub/keep.png", "usr_1.png"}, keys)
}

func TestFSStorePutStopsWhenTheContextEnds(t *testing.T) {
	store, key, data := newFSStore(t)

	// The local write is the one leg of an upload with no network call to
	// notice a cancelled request, so the reader is what makes the caller's
	// deadline real: a copy on a dead context stops at its first read.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := store.Put(ctx, fsTestBucket, key, bytes.NewReader(data), int64(len(data)), "image/png")
	assert.ErrorIs(t, err, context.Canceled)

	// A cancelled copy leaves no half file and no temp file behind.
	_, err = store.Get(t.Context(), fsTestBucket, key)
	assert.ErrorIs(t, err, ErrNotFound)
	keys, listErr := listedKeys(t.Context(), store, fsTestBucket)
	require.NoError(t, listErr)
	assert.Empty(t, keys)
}

func TestCtxReaderStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	source := bytes.NewReader([]byte("payload"))
	reader := ctxReader{ctx: ctx, r: source}

	buf := make([]byte, 4)
	n, err := reader.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 4, n, "a live context passes the read through")

	cancel()
	_, err = reader.Read(buf)
	assert.ErrorIs(t, err, context.Canceled, "a dead context stops the next read")
}

func TestContentHashIsTheWholeFile(t *testing.T) {
	// The content hash is the engine's "the backend holds these bytes"
	// check; it is the SHA-256 of the whole file, the value the sync
	// commits and the retry compares.
	data := []byte("hash me whole")
	sum := sha256.Sum256(data)
	assert.Equal(t, hex.EncodeToString(sum[:]), hashOf(data))
}
