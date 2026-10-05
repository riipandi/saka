package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// uploadsDir is the data-directory subtree the final files live under, one
// directory per bucket inside it — `uploads/{bucket}/{key}` — so the
// engine's own subtrees (staging, logs, backup, config) sit beside it at
// the top level and no bucket name can reach them.
const uploadsDir = "uploads"

// FS is the local-filesystem backend: every bucket/key is one file under
// the data directory's uploads subtree, at the same path its key spells —
// the deployment where a stored file has a visible form on the machine that
// stored it.
type FS struct {
	root string
}

// NewFS builds the local backend over the data directory. The directory is
// not created here: the first write creates it, so a read-only run of a
// command that never stores a file touches nothing.
func NewFS(root string) *FS {
	return &FS{root: root}
}

// EnsureBucket makes the bucket's container exist. The local driver creates
// directories on the write, so there is nothing to do ahead of it — the
// call only validates the name, which the manager has already done.
func (s *FS) EnsureBucket(_ context.Context, _ string) error {
	return nil
}

// Get opens the key's file. The caller closes the handle; no buffer holds
// the file on the read path.
func (s *FS) Get(_ context.Context, bucket, key string) (io.ReadCloser, error) {
	f, err := os.Open(s.path(bucket, key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("storage: file %s: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: open file %s: %w", key, err)
	}
	return f, nil
}

// Put writes the file through a temp file and a rename, so a reader never
// observes half of it and a crash leaves a temp file — a name the garbage
// collection's listing skips — instead of a truncated one. The content type
// is the local filesystem's to have no opinion about: the feature serves it
// from the manifest's own record.
//
// The copy watches the context: a local write is the one leg of an upload
// with no network call to notice a cancelled request, so the reader is what
// makes the caller's deadline real here. A cancelled copy removes its temp
// file and leaves the key's previous bytes untouched.
func (s *FS) Put(ctx context.Context, bucket, key string, r io.Reader, _ int64, _ string) error {
	path := s.path(bucket, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("storage: file directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(key)+".*")
	if err != nil {
		return fmt.Errorf("storage: write file %s: %w", key, err)
	}
	if _, err = io.Copy(tmp, ctxReader{ctx: ctx, r: r}); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("storage: write file %s: %w", key, err)
	}
	if err = tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("storage: write file %s: %w", key, err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("storage: write file %s: %w", key, err)
	}
	return nil
}

// Delete removes the key's file and the directories an empty subtree leaves
// behind. A missing file is the state the caller asked for, not an error.
func (s *FS) Delete(_ context.Context, bucket, key string) error {
	if err := os.Remove(s.path(bucket, key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: delete file %s: %w", key, err)
	}
	s.pruneDirs(bucket, key)
	return nil
}

// List calls fn for every key one bucket's subtree holds, relative to the
// bucket — the key the rest of the Store contract speaks. Temp files — the
// crash leftovers a put's rename leaves behind — are skipped, as are dot
// entries: only the backend's real files are listed, and only they may be
// deleted.
func (s *FS) List(_ context.Context, bucket string, fn func(key string) error) error {
	bucketRoot, err := os.OpenRoot(filepath.Join(s.root, uploadsDir, bucket))
	if errors.Is(err, fs.ErrNotExist) {
		// A bucket nothing stored yet has no subtree: nothing to list.
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: file directory: %w", err)
	}
	defer func() { _ = bucketRoot.Close() }()

	return fs.WalkDir(bucketRoot.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		// A walk over an fs.FS yields the paths relative to its root —
		// exactly the keys the backend stores for this bucket.
		return fn(filepath.ToSlash(path))
	})
}

// pruneDirs removes the directories a deleted file's subtree emptied, so a
// churn of keys does not leave an empty skeleton behind. The bucket
// directory itself is kept — an empty bucket is still a bucket. Errors are
// the caller's to never see: an unremovable directory only costs a listing.
func (s *FS) pruneDirs(bucket, key string) {
	dir := filepath.Dir(s.path(bucket, key))
	bucketDir := filepath.Join(s.root, uploadsDir, bucket)
	for {
		if dir == bucketDir {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// path is where one bucket/key's file lives: uploads/{bucket}/{key} under
// the data directory — the public /storage path, with the container subtree
// the engine's own subtrees sit beside.
func (s *FS) path(bucket, key string) string {
	return filepath.Join(s.root, uploadsDir, bucket, filepath.FromSlash(key))
}
