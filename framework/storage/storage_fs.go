package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
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

// bucketRoot opens the bucket's subtree as the rooted fence every file
// operation goes through: each name the backend resolves is held inside the
// bucket, so a key that spells a traversal — whatever a caller above
// validated — lands on os.Root's refusal instead of the machine's wider
// filesystem. The key validation contract (ErrInvalidKey at the manager)
// stays the primary fence; this is the second one, and it is the same
// fence the listing has always walked.
//
// A bucket nothing wrote yet has no directory to root; the readers answer
// their not-found shapes and the delete answers the state it asked for,
// while the write creates the bucket directory before opening the root.
func (s *FS) bucketRoot(bucket string) (*os.Root, error) {
	return os.OpenRoot(filepath.Join(s.root, uploadsDir, bucket))
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
	root, err := s.bucketRoot(bucket)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("storage: file %s: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: open file %s: %w", key, err)
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(key)
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
	// The bucket's own directory is the write's first act — a plain Mkdir,
	// at a path the manager's bucket-name validation already fenced. The
	// rooted operations refuse to touch anything outside it from here on.
	bucketDir := filepath.Join(s.root, uploadsDir, bucket)
	if err := os.MkdirAll(bucketDir, 0o755); err != nil {
		return fmt.Errorf("storage: file directory: %w", err)
	}
	root, err := os.OpenRoot(bucketDir)
	if err != nil {
		return fmt.Errorf("storage: file directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	if dir := path.Dir(key); dir != "." {
		if mkdirErr := root.MkdirAll(dir, 0o755); mkdirErr != nil {
			return fmt.Errorf("storage: file directory: %w", mkdirErr)
		}
	}
	tmp, tmpName, err := createTempInRoot(root, key)
	if err != nil {
		return fmt.Errorf("storage: write file %s: %w", key, err)
	}
	if _, err = io.Copy(tmp, ctxReader{ctx: ctx, r: r}); err != nil {
		_ = tmp.Close()
		_ = root.Remove(tmpName)
		return fmt.Errorf("storage: write file %s: %w", key, err)
	}
	if err = tmp.Close(); err != nil {
		_ = root.Remove(tmpName)
		return fmt.Errorf("storage: write file %s: %w", key, err)
	}
	if err = root.Rename(tmpName, key); err != nil {
		_ = root.Remove(tmpName)
		return fmt.Errorf("storage: write file %s: %w", key, err)
	}
	return nil
}

// createTempInRoot opens a unique dot-prefixed temp file inside the bucket
// root — the shape the listing's scan skips, so a crash's leftover never
// names itself a file. os.Root has no CreateTemp, so the O_EXCL retry is
// the writer's own: the random suffix makes a collision a curiosity, not
// an expected round.
func createTempInRoot(root *os.Root, key string) (*os.File, string, error) {
	base := path.Base(key)
	for range 8 {
		name := "." + base + "." + rand.Text() + ".tmp"
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return f, name, nil
	}
	return nil, "", fmt.Errorf("storage: temp file name retries exhausted for %s", key)
}

// Delete removes the key's file and the directories an empty subtree leaves
// behind. A missing file is the state the caller asked for, not an error.
func (s *FS) Delete(_ context.Context, bucket, key string) error {
	root, err := s.bucketRoot(bucket)
	if errors.Is(err, fs.ErrNotExist) {
		// A bucket nothing stored in has nothing to delete.
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: delete file %s: %w", key, err)
	}
	defer func() { _ = root.Close() }()

	if err := root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: delete file %s: %w", key, err)
	}
	s.pruneDirs(root, key)
	return nil
}

// pruneDirs removes the directories a deleted file's subtree emptied, so a
// churn of keys does not leave an empty skeleton behind. The bucket root
// itself is kept — an empty bucket is still a bucket. Errors are the
// caller's to never see: an unremovable directory only costs a listing.
func (s *FS) pruneDirs(root *os.Root, key string) {
	for dir := path.Dir(key); dir != "."; dir = path.Dir(dir) {
		if err := root.Remove(dir); err != nil {
			// A directory that still holds something — or cannot go —
			// stops the walk; the empty skeleton above it stays.
			return
		}
	}
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

	return fs.WalkDir(bucketRoot.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		// A walk over an fs.FS yields the paths relative to its root —
		// exactly the keys the backend stores for this bucket.
		return fn(filepath.ToSlash(name))
	})
}
