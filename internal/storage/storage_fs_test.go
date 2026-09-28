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

// newFSStore builds a local backend over a throwaway directory and returns
// a key with known bytes, the shape every store test runs through.
func newFSStore(t *testing.T) (*FS, string, []byte) {
	t.Helper()

	root := t.TempDir()
	data := []byte("a whole file, stored once under the key its feature composed")
	return NewFS(root), "avatars/usr_1.png", data
}

func TestFSStoreRoundTripsAFile(t *testing.T) {
	store, key, data := newFSStore(t)
	ctx := t.Context()

	keys, err := listedKeys(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, keys)

	require.NoError(t, store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/png"))

	reader, err := store.Get(ctx, key)
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, data, got)

	keys, err = listedKeys(ctx, store)
	require.NoError(t, err)
	assert.Equal(t, []string{key}, keys)

	require.NoError(t, store.Delete(ctx, key))
	keys, err = listedKeys(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestFSStorePutReplacesWhole(t *testing.T) {
	store, key, data := newFSStore(t)
	ctx := t.Context()

	require.NoError(t, store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/png"))
	changed := append(bytes.Clone(data), []byte("-changed")...)
	require.NoError(t, store.Put(ctx, key, bytes.NewReader(changed), int64(len(changed)), "image/png"))

	reader, err := store.Get(ctx, key)
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, changed, got)
}

func TestFSStoreGetMissingFileIsNotFound(t *testing.T) {
	store, key, _ := newFSStore(t)

	_, err := store.Get(t.Context(), key)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestFSStoreDeleteMissingFileIsQuiet(t *testing.T) {
	store, key, _ := newFSStore(t)

	assert.NoError(t, store.Delete(t.Context(), key))
}

func TestFSStoreListSkipsTempFiles(t *testing.T) {
	store, key, data := newFSStore(t)
	ctx := t.Context()

	require.NoError(t, store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/png"))
	// A temp file shares the file's directory: a crash's leftover, which
	// the scan must not name, because only real files may be deleted. The
	// backend writes one under the key's own basename, so the fixture does
	// too.
	require.NoError(t, os.WriteFile(
		filepath.Join(store.root, filesDir, "avatars", ".usr_1.png.tmp"),
		[]byte("x"), 0o600))

	keys, err := listedKeys(ctx, store)
	require.NoError(t, err)
	assert.Equal(t, []string{key}, keys)
}

func TestFSStoreDeletePrunesTheEmptyDirs(t *testing.T) {
	store, key, data := newFSStore(t)
	ctx := t.Context()

	require.NoError(t, store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/png"))
	require.NoError(t, store.Delete(ctx, key))

	_, err := os.Stat(filepath.Join(store.root, filesDir, "avatars", "usr_1"))
	assert.True(t, os.IsNotExist(err), "an emptied subtree must not linger")
}

func TestFSStorePutStopsWhenTheContextEnds(t *testing.T) {
	store, key, data := newFSStore(t)

	// The local write is the one leg of an upload with no network call to
	// notice a cancelled request, so the reader is what makes the caller's
	// deadline real: a copy on a dead context stops at its first read.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/png")
	assert.ErrorIs(t, err, context.Canceled)

	// A cancelled copy leaves no half file and no temp file behind.
	_, err = store.Get(t.Context(), key)
	assert.ErrorIs(t, err, ErrNotFound)
	keys, listErr := listedKeys(t.Context(), store)
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
