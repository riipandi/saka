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

// reservedDirs are the data-directory subtrees the engine owns for itself.
// Bucket directories sit beside them, so the listing walk skips them: a
// staging file or a log must never be swept by the garbage collection.
var reservedDirs = map[string]struct{}{
	"staging": {},
	"logs":    {},
	"backup":  {},
	"config":  {},
	"files":   {}, // the pre-bucket finals tree, retired with the revamp
}

// FS is the local-filesystem backend: every bucket/key is one file under the
// data directory, at the same path its key spells — the deployment where a
// stored file has a visible form on the machine that stored it.
type FS struct {
	root string
}

// NewFS builds the local backend over the data directory. The directory is
// not created here: the first write creates it, so a read-only run of a
// command that never stores a file touches nothing.
func NewFS(root string) *FS {
	return &FS{root: root}
}

// Get opens the key's file. The caller closes the handle; no buffer holds
// the file on the read path.
func (s *FS) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f, err := os.Open(s.path(key))
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
func (s *FS) Put(ctx context.Context, key string, r io.Reader, _ int64, _ string) error {
	path := s.path(key)
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
func (s *FS) Delete(_ context.Context, key string) error {
	if err := os.Remove(s.path(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: delete file %s: %w", key, err)
	}
	s.pruneDirs(key)
	return nil
}

// List walks the data directory and calls fn for every bucket/key the
// backend holds. Temp files — the crash leftovers a put's rename leaves
// behind — are skipped, as are the engine's own reserved subtrees
// (staging, logs, backup, config) and dot entries: only the backend's real
// files are listed, and only they may be deleted.
func (s *FS) List(_ context.Context, fn func(key string) error) error {
	entries, err := os.OpenRoot(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: file directory: %w", err)
	}
	defer entries.Close()

	return fs.WalkDir(entries.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		// Only bucket directories (a single top-level segment) are the
		// engine's to sweep; everything deeper is a key segment.
		if !strings.Contains(path, "/") {
			if _, reserved := reservedDirs[path]; reserved {
				return fs.SkipDir
			}
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		// A walk over an fs.FS yields the paths relative to its root —
		// exactly the bucket-scoped keys the backend stores.
		return fn(filepath.ToSlash(path))
	})
}

// pruneDirs removes the directories a deleted file's subtree emptied, so a
// churn of keys does not leave an empty skeleton behind. The bucket
// directory itself is kept — an empty bucket is still a bucket. Errors are
// the caller's to never see: an unremovable directory only costs a listing.
func (s *FS) pruneDirs(key string) {
	dir := filepath.Dir(s.path(key))
	for {
		// Stop before removing the bucket's own directory: dir's parent
		// being the root means dir is the bucket directory.
		if dir == s.root || filepath.Dir(dir) == s.root {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// path is where one bucket/key's file lives: <bucket>/<key> under the data
// directory, mirroring the public /storage path.
func (s *FS) path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}
