package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeforeSyncHookRunsBeforeTheFileIsHashed(t *testing.T) {
	// The hook may rewrite the staging file — pre-processing such as
	// sanitizing or re-encoding — and the upload must hash what the hook
	// wrote, not what was staged.
	manager, _, _ := newManager(t)
	ctx := t.Context()

	original := bytes.Repeat([]byte("original"), 40)
	replacement := bytes.Repeat([]byte("clean"), 40)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(original), nil))

	manager.WithBeforeSync(func(ctx context.Context, ref, path string) error {
		assert.Equal(t, testBucket+"/k", ref)
		assert.FileExists(t, path)
		return os.WriteFile(path, replacement, 0o600)
	})
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	reader, err := manager.Open(ctx, testBucket, "k")
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, replacement, got, "the upload carries the hook's bytes")
}

func TestBeforeSyncHookRejectionFailsTheRoundAndKeepsTheFile(t *testing.T) {
	// A refused file must fail the sync — the queue retries it, and a
	// hook that keeps refusing walks it to the dead letters — and the
	// staging file must stay, so the retry sees the same evidence.
	manager, _, _ := newManager(t)
	ctx := t.Context()

	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader([]byte("bad")), nil))
	manager.WithBeforeSync(func(ctx context.Context, ref, path string) error {
		return errors.New("rejected by policy")
	})

	err := manager.Sync(ctx, testBucket+"/k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before-sync hook")

	// The retry replays the hook: it runs again on the next attempt.
	calls := 0
	manager.WithBeforeSync(func(ctx context.Context, ref, path string) error {
		calls++
		return nil
	})
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"), "a retry with a passing hook finishes the round")
	assert.Equal(t, 1, calls)
}

func TestAfterSyncHookRunsOnceTheManifestIsReady(t *testing.T) {
	manager, _, _ := newManager(t)
	ctx := t.Context()

	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(bytes.Repeat([]byte("x"), 40)), nil))

	var seen Manifest
	manager.WithAfterSync(func(ctx context.Context, manifest Manifest) error {
		seen = manifest
		// The hook owns the moment before cleanup: its post-processing
		// can still read the staging copy if it needs to.
		assert.FileExists(t, filepath.Join(manager.Staging(), testBucket, "k"))
		return nil
	})
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))

	assert.Equal(t, StatusReady, seen.Status)

	// Cleanup happened after the hook: the upload round is fully closed.
	assert.NoFileExists(t, filepath.Join(manager.Staging(), testBucket, "k"))
}

func TestAfterSyncHookFailureIsReplayedByTheRetry(t *testing.T) {
	// The contract in practice: a failed hook leaves the staging file, so
	// the retry reaches the finished-manifest fast path — no re-hash, no
	// re-upload — and the hook runs again. Idempotence is what makes the
	// replay safe.
	manager, _, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("retry"), 40)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(data), nil))

	fail := true
	var runs int
	manager.WithAfterSync(func(ctx context.Context, manifest Manifest) error {
		runs++
		if fail {
			return errors.New("post-processing unavailable")
		}
		return nil
	})

	require.Error(t, manager.Sync(ctx, testBucket+"/k"))
	assert.FileExists(t, filepath.Join(manager.Staging(), testBucket, "k"),
		"the staging file survives so the retry can replay the hook")

	fail = false
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))
	assert.Equal(t, 2, runs)
	assert.NoFileExists(t, filepath.Join(manager.Staging(), testBucket, "k"))

	// The retry went through the fast path: the file is intact and ready.
	manifest, err := manager.Manifest(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, StatusReady, manifest.Status)
}

func TestManagerWithoutHooksBehavesAsBefore(t *testing.T) {
	// The nil hooks are the default: no hook installed, no behavior
	// change, and the round completes untouched.
	manager, _, _ := newManager(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("plain"), 40)
	require.NoError(t, manager.Stage(ctx, testBucket, "k", bytes.NewReader(data), nil))
	require.NoError(t, manager.Sync(ctx, testBucket+"/k"))
	assert.NoFileExists(t, filepath.Join(manager.Staging(), testBucket, "k"))
}
