package storage

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/riipandi/saka/internal/datastore"
)

// DB is the persistence surface the manager needs: plain reads on the shared
// Querier, writes inside the transaction WithTx owns. datastore.Postgres
// satisfies it; the indirection keeps the manager testable against a
// transaction the test owns.
type DB interface {
	datastore.Querier
	WithTx(ctx context.Context, fn func(ctx context.Context, tx datastore.Querier) error) error
}

// Progress is the upload state of one key, the data a status endpoint reads
// while the queue works. A whole-file upload is one step — pending while the
// manifest says so, ready once the backend holds the bytes — and the size is
// the only magnitude the round has.
type Progress struct {
	// Status is pending while the file travels, ready once the backend
	// holds it.
	Status string
	// Size is the file's byte size, known from the moment it is staged.
	Size int64
}

// BeforeSyncHook gates one sync attempt. It runs after the staging file
// exists and before its fingerprint is read, so a hook that rewrites or
// replaces the file is hashed from its own output. path is the staging
// file's location; the hook owns reading and writing it. Returning an
// error fails the round — the queue retries it, and a hook that keeps
// refusing a file walks it to the dead letters. Idempotence is the
// hook's contract: the queue's retries replay it.
type BeforeSyncHook func(ctx context.Context, key, path string) error

// AfterSyncHook runs once a sync's manifest is committed ready — the
// backend holds the bytes, the staging copy still exists. It runs before
// the staging copy is removed, so a failed hook is retried with the file
// still present, and a retry of an already-ready file reaches it through
// the finished-manifest fast path. Because retries replay it, it must be
// idempotent; it must also stay quick — long work belongs in a job the
// hook enqueues, not in the upload worker's slot.
type AfterSyncHook func(ctx context.Context, manifest Manifest) error

// Manager is the file-level engine over a Store: staging, the upload, the
// read, and the deletion. Features hold this, not a backend; which backend
// answers is the configuration's business.
type Manager struct {
	metrics   *storageMetrics
	store     Store
	db        DB
	manifests *Manifests
	staging   string
	log       *slog.Logger
	// beforeSync and afterSync are the feature extension points around
	// the upload round; nil means no hook, and a nil hook costs nothing.
	beforeSync BeforeSyncHook
	afterSync  AfterSyncHook
}

// NewManager builds the engine. staging is the directory a caller writes the
// next file into.
func NewManager(store Store, db DB, staging string, log *slog.Logger) *Manager {
	return &Manager{
		metrics:   newStorageMetrics(),
		store:     store,
		db:        db,
		manifests: NewManifests(),
		staging:   staging,
		log:       log,
	}
}

// WithBeforeSync installs the hook that runs before each sync attempt —
// the gate a feature validates or pre-processes the staging file through.
// Returns the manager, so registry wiring reads as a chain.
func (m *Manager) WithBeforeSync(hook BeforeSyncHook) *Manager {
	m.beforeSync = hook
	return m
}

// WithAfterSync installs the hook that runs once the manifest is ready —
// the point a feature's post-processing (thumbnail, notification) starts
// from. Returns the manager, so registry wiring reads as a chain.
func (m *Manager) WithAfterSync(hook AfterSyncHook) *Manager {
	m.afterSync = hook
	return m
}

// Manifest reads the stored manifest of a key, the version a feature reads
// to know what the backend holds. ErrNotFound for a key nothing stored yet.
func (m *Manager) Manifest(ctx context.Context, key string) (Manifest, error) {
	manifest, err := m.manifests.Load(ctx, m.db, key)
	if errors.Is(err, ErrNoManifest) {
		return Manifest{}, ErrNotFound
	}
	return manifest, err
}

// UpdateMetadata replaces the metadata a key carries — content type, the
// original file name, whatever the feature records — leaving the status and
// the content hash untouched. Works before and after the upload.
func (m *Manager) UpdateMetadata(ctx context.Context, key string, metadata map[string]any) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	return m.manifests.UpdateMetadata(ctx, m.db, key, metadata)
}

// Progress reports how far one key's upload has travelled. The REST
// progress read (`GET /api/uploads/{key}`, mounted beside the transport)
// maps the manifest onto its answer, and the after-sync hook pushes the
// finished transition to the metadata's owner — the sources of truth stay
// the manifest's row and this read of it.
func (m *Manager) Progress(ctx context.Context, key string) (Progress, error) {
	manifest, err := m.Manifest(ctx, key)
	if err != nil {
		return Progress{}, err
	}
	return Progress{Status: manifest.Status, Size: manifest.Size}, nil
}

// Staging is the directory the next file is written into. The watcher owns
// it; a caller that does not run the watcher names the same directory.
func (m *Manager) Staging() string { return m.staging }

// Stage writes a file into the staging directory and records the intent:
// the key's metadata and the staging fingerprint land in the manifest
// before the first byte travels, so a crash between the two leaves a
// pending row, never a half-stored file. The write is the one cost on the
// request path — local and buffered; the bytes move to the backend later,
// on the queue.
//
// metadata is the feature's own record (content type, original file name,
// owner); the engine carries it, never reads it. Re-staging a key replaces
// its metadata and makes the stored manifest stale, so the next sync
// uploads the new version.
func (m *Manager) Stage(ctx context.Context, key string, r io.Reader, metadata map[string]any) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := os.MkdirAll(m.staging, 0o755); err != nil {
		return fmt.Errorf("storage: staging directory: %w", err)
	}
	// A key may name a subdirectory, so the target directory exists before
	// the rename. The temp file makes a half-written staging file
	// invisible: the watcher only ever sees the final name, complete or
	// absent.
	target := m.stagingPath(key)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("storage: staging %q: %w", key, err)
	}
	tmp, err := os.CreateTemp(m.staging, "."+filepath.Base(key)+".*")
	if err != nil {
		return fmt.Errorf("storage: staging %q: %w", key, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err = io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: staging %q: %w", key, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("storage: staging %q: %w", key, err)
	}
	if err = os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("storage: staging %q: %w", key, err)
	}

	// The row is the durable half of the staging write: metadata and the
	// fingerprint the sync's checkpoint compares against, committed before
	// the upload is enqueued.
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("storage: staging %q: %w", key, err)
	}
	if err := m.manifests.Stage(ctx, m.db, key, info.Size(), micros(info.ModTime()), metadata); err != nil {
		return err
	}
	m.metrics.recordStaged(ctx)
	return nil
}

// Sync uploads a staging file: it hashes the file (or reuses the stored
// hash a retry's fingerprint matches), stores it whole in the backend, and
// commits the ready manifest. It is the body of the upload job; idempotent,
// so the queue's retries replay it. The before- and after-sync hooks (nil
// by default) wrap the round; their contracts sit on their types.
func (m *Manager) Sync(ctx context.Context, key string) error {
	started := time.Now()
	outcome, uploaded, err := m.sync(ctx, key)
	if err != nil && outcome == "" {
		outcome = uploadError
	}
	m.metrics.recordSync(ctx, outcome, time.Since(started), uploaded)
	return err
}

// sync is Sync's body; the wrapper records its outcome. The outcome travels
// beside the error because a failed cleanup is not a failed upload: the
// manifest is ready, and the staging copy waits for the next pass.
func (m *Manager) sync(ctx context.Context, key string) (string, int64, error) {
	if err := ValidateKey(key); err != nil {
		return uploadError, 0, err
	}
	path := m.stagingPath(key)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		// A key with no staging file has nothing to sync: either another
		// attempt already finished, or the file was staged by a caller
		// that manages its own lifecycle.
		return uploadSkipped, 0, nil
	} else if err != nil {
		return uploadError, 0, fmt.Errorf("storage: stat staging %q: %w", key, err)
	}

	// The gate runs before the fingerprint is read: a hook that rewrites
	// or replaces the staging file is hashed from its own output, and the
	// fingerprint the TOCTOU guard trusts is the post-hook file's.
	if m.beforeSync != nil {
		if err := m.beforeSync(ctx, key, path); err != nil {
			return uploadError, 0, fmt.Errorf("storage: before-sync hook for %q: %w", key, err)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return uploadError, 0, fmt.Errorf("storage: open staging %q: %w", key, err)
	}
	defer func() { _ = f.Close() }()

	// The fingerprint is read once and trusted for the whole sync: the file
	// the hash was computed from is the file the TOCTOU guard compares
	// against before the staging copy is removed.
	info, err := f.Stat()
	if err != nil {
		return uploadError, 0, fmt.Errorf("storage: stat staging %q: %w", key, err)
	}
	fingerprint := stagingFingerprint{size: info.Size(), mtime: micros(info.ModTime())}

	prior, err := m.manifests.Load(ctx, m.db, key)
	if err != nil && !errors.Is(err, ErrNoManifest) {
		return uploadError, 0, err
	}

	// The checkpoint is the retry's fast path: a pending manifest whose
	// fingerprint matches this staging file was computed from the same
	// bytes, so the stored content hash is reused and the whole hashing
	// pass — the one thing a retry of a large file cannot afford — is
	// skipped. A hash the manifest does not name yet (a Stage row a sync
	// never reached) is no reuse: the file is hashed as if it were new.
	reuse := prior.Status == StatusPending &&
		prior.ContentHash != "" &&
		prior.StagingSize == fingerprint.size &&
		prior.StagingMtime.Equal(fingerprint.mtime)

	contentHash := prior.ContentHash
	if !reuse {
		h := sha256.New()
		if _, err := io.Copy(h, ctxReader{ctx: ctx, r: f}); err != nil {
			return uploadError, 0, fmt.Errorf("storage: hash staging %q: %w", key, err)
		}
		contentHash = hex.EncodeToString(h.Sum(nil))
	}

	// The finished-manifest fast path: the stored manifest is ready and
	// hashes to the same content, so the backend already holds these
	// bytes. The row is refreshed and the staging copy can go — but the
	// after-sync hook still runs: a retry that reaches this path after a
	// failed hook is how the hook gets its replay.
	if prior.Status == StatusReady && sameHash(prior.ContentHash, contentHash) {
		prior.Size = fingerprint.size
		prior.StagingSize = fingerprint.size
		prior.StagingMtime = fingerprint.mtime
		if err := m.saveManifest(ctx, prior); err != nil {
			return uploadError, 0, err
		}
		if m.afterSync != nil {
			if err := m.afterSync(ctx, prior); err != nil {
				return uploadError, 0, fmt.Errorf("storage: after-sync hook for %q: %w", key, err)
			}
		}
		return uploadSuccess, 0, m.clearStaging(ctx, path, fingerprint)
	}

	// The checkpoint: the pending row lands before the first byte travels,
	// so a crash inside the PUT leaves a manifest a retry resumes from.
	checkpoint := File{
		Key:          key,
		Size:         fingerprint.size,
		ContentHash:  contentHash,
		Status:       StatusPending,
		Metadata:     prior.Metadata,
		StagingSize:  fingerprint.size,
		StagingMtime: fingerprint.mtime,
	}
	if !reuse {
		if err := m.saveManifest(ctx, checkpoint); err != nil {
			return uploadError, 0, err
		}
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return uploadError, 0, fmt.Errorf("storage: rewind staging %q: %w", key, err)
	}
	// The content type is the feature's own record, carried from the stage
	// and riding on the stored object: a direct read answers what the
	// bytes are without consulting the manifest.
	contentType, _ := checkpoint.Metadata["content_type"].(string)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := m.store.Put(ctx, key, f, fingerprint.size, contentType); err != nil {
		return uploadError, 0, err
	}

	// The ready commit closes the round: the backend holds the bytes, the
	// manifest says so, and the staging copy has served its purpose —
	// unless it changed underneath the sync, which the guard catches.
	checkpoint.Status = StatusReady
	if err := m.saveManifest(ctx, checkpoint); err != nil {
		return uploadError, 0, err
	}
	m.log.InfoContext(ctx, "storage: file synced",
		"key", key, "size", fingerprint.size)

	// The after-sync hook runs before the staging copy is removed: a
	// failed hook leaves the file in place, so the queue's retry finds a
	// ready manifest with a matching fingerprint and replays the hook
	// through the finished-manifest fast path.
	if m.afterSync != nil {
		if err := m.afterSync(ctx, checkpoint); err != nil {
			return uploadError, 0, fmt.Errorf("storage: after-sync hook for %q: %w", key, err)
		}
	}
	return uploadSuccess, fingerprint.size, m.clearStaging(ctx, path, fingerprint)
}

// saveManifest commits a manifest in its own transaction.
func (m *Manager) saveManifest(ctx context.Context, file File) error {
	return m.db.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		return m.manifests.Save(ctx, tx, file)
	})
}

// Open reads a stored file back as one stream: the backend is read on
// demand, so a caller never holds the bytes whole.
func (m *Manager) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if _, err := m.Manifest(ctx, key); err != nil {
		return nil, err
	}
	body, err := m.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// Delete removes a file's manifest row and the bytes the backend holds. The
// deletion happens after the row is gone, so a crash between the two leaves
// an unreferenced object for the garbage collection, never a manifest that
// names missing bytes.
func (m *Manager) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}

	if err := m.manifests.Delete(ctx, m.db, key); err != nil {
		return err
	}
	if err := m.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("storage: delete file %s: %w", key, err)
	}

	// A staged-but-never-synced copy would outlive its manifest; the key
	// is gone, so its staging file goes with it.
	_ = os.Remove(m.stagingPath(key))
	return nil
}

// CollectGarbage removes every object the backend holds that no manifest
// names. It is the drain of the paths a crash can leave: a delete that
// finished its rows but not its object removal. A listed name that is not
// a valid key is skipped, not deleted — a bucket shared with another
// tenant's objects, or a prefix that carries the deployment's own strays,
// is never swept by the engine's vocabulary. Returns the number of objects
// removed.
func (m *Manager) CollectGarbage(ctx context.Context) (int, error) {
	keep, err := m.manifests.StoredKeys(ctx, m.db)
	if err != nil {
		return 0, err
	}

	var removed int
	err = m.store.List(ctx, func(key string) error {
		if _, ok := keep[key]; ok {
			return nil
		}
		if invalid := ValidateKey(key); invalid != nil {
			return nil
		}
		if delErr := m.store.Delete(ctx, key); delErr != nil {
			return delErr
		}
		removed++
		return nil
	})
	if err != nil {
		return removed, fmt.Errorf("storage: garbage collection: %w", err)
	}
	return removed, nil
}

// sameHash compares two content hashes in constant time: a hash is an
// equality the timing of whose answer names no secret, but the compare
// costs nothing and settles the habit.
func sameHash(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// clearStaging removes the staging copy, but only the copy this sync read:
// a file re-staged underneath a running sync has a different fingerprint,
// and removing it would destroy an upload nobody has recorded. The mismatch
// is an error, so the queue retries and the newer staging file gets its own
// round.
func (m *Manager) clearStaging(ctx context.Context, path string, fingerprint stagingFingerprint) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: clear staging: %w", err)
	}
	if info.Size() != fingerprint.size || !micros(info.ModTime()).Equal(fingerprint.mtime) {
		return fmt.Errorf("storage: staging %s changed during sync, left for a fresh round", filepath.Base(path))
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: clear staging: %w", err)
	}
	return nil
}

// stagingPath is where a key's staging file waits. The key is validated by
// every entry point before it reaches here, so the join cannot walk out of
// the staging directory.
func (m *Manager) stagingPath(key string) string {
	return filepath.Join(m.staging, key)
}

// stagingFingerprint is the identity of the staging file one sync read:
// size and modification time. Equal fingerprints mean the same bytes.
type stagingFingerprint struct {
	size  int64
	mtime time.Time
}

// micros truncates an instant to microseconds, the precision a `timestamptz`
// column keeps. A fingerprint is written to Postgres and compared against the
// file system's own reading, so both sides go through this one function: the
// comparison then sees the same value on both sides of the round trip,
// whatever precision the volume reports. Without it a nanosecond file system
// would make an honest retry re-hash the whole file, and a coarse one could
// let the reuse path trust a fingerprint the database had rounded.
func micros(t time.Time) time.Time {
	return t.Truncate(time.Microsecond)
}
