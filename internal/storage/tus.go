package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// TusLengthKey is the manifest-metadata key that marks a row a tus session
// owns: its value is the upload's declared length, a number. The expiry job
// reads it to age a session out, and the startup sweep reads it to leave an
// interrupted session alone — a session's staging file is whole only when
// its size has reached the length, and the completion enqueue is the proof
// the bytes arrived.
const TusLengthKey = "tus_upload_length"

// TusSessionExpiry is how long an interrupted session's row and staging
// file live before the expiry job reclaims them. A tus client resumes with
// a HEAD and continues from the offset, so the window only has to cover a
// client's absence; a day is generous and needs no configuration.
const TusSessionExpiry = 24 * time.Hour

// UploadEnqueuer is the seam the completion path rides: the final PATCH
// hands the finished staging file to the queue, and the queue's sync does
// the rest. internal/jobs adapts it over the durable queue — the engine
// names no queue, the way it names no notification channel.
type UploadEnqueuer interface {
	EnqueueUpload(ctx context.Context, ref string) error
}

// WithUploadEnqueuer arms the completion enqueue. Nil leaves completion
// silent — the startup sweep is the slower way a finished staging file
// still reaches the queue.
func (m *Manager) WithUploadEnqueuer(enqueuer UploadEnqueuer) *Manager {
	m.uploadEnqueuer = enqueuer
	return m
}

// tus session failures. The handler maps the offset one onto the tus
// protocol's own answer; the rest name the fact.
var (
	// ErrOffsetMismatch is a PATCH whose Upload-Offset does not name the
	// staging file's current end — two writers raced, or the client lost
	// track of its own position.
	ErrOffsetMismatch = errors.New("storage: upload offset mismatch")

	// ErrLengthMismatch is a completion whose staging file has not reached
	// the length the session declared — the bytes are not all here.
	ErrLengthMismatch = errors.New("storage: upload length mismatch")
)

// TusBegin opens a session: an empty staging file and a pending manifest
// row that carries the declared length inside the feature's metadata. A
// re-begin of the same bucket/key rewrites both — the session's state is
// the staging file and the row, nothing else, so a client that restarts an
// upload from zero needs no other cleanup.
func (m *Manager) TusBegin(ctx context.Context, bucket, key string, length int64, metadata map[string]any) error {
	if length < 0 {
		return fmt.Errorf("storage: upload length %d: %w", length, ErrLengthMismatch)
	}
	if err := ValidateBucketName(bucket); err != nil {
		return err
	}
	if err := ValidateKey(key); err != nil {
		return err
	}
	bucketID, err := m.resolveBucket(ctx, bucket)
	if err != nil {
		return err
	}

	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata[TusLengthKey] = length

	if err := m.truncateStaging(bucket, key); err != nil {
		return err
	}
	if err := m.manifests.Stage(ctx, m.db, bucketID, key, 0, micros(time.Now()), metadata, false); err != nil {
		return err
	}
	m.metrics.recordStaged(ctx)
	return nil
}

// TusOffset answers the offset the session's staging file has reached: the
// bytes a resuming client continues from. ErrNotFound names a session the
// table does not hold — an expired one, or one that never existed.
func (m *Manager) TusOffset(ctx context.Context, bucket, key string) (int64, error) {
	if _, err := m.Manifest(ctx, bucket, key); err != nil {
		return 0, err
	}
	return m.stagingSize(bucket, key)
}

// TusAppend writes one chunk at the offset the client claims the file has
// reached. The claim is the whole concurrency story: a PATCH whose offset
// does not name the staging file's current end is refused, so two writers
// cannot interleave and a client that lost its position is told so rather
// than served a corrupted file. The row's fingerprint follows the file, so
// a retry's checkpoint compares against what this chunk left.
func (m *Manager) TusAppend(ctx context.Context, bucket, key string, offset int64, r io.Reader) (int64, error) {
	if err := ValidateBucketName(bucket); err != nil {
		return 0, err
	}
	if err := ValidateKey(key); err != nil {
		return 0, err
	}
	bucketID, err := m.resolveBucket(ctx, bucket)
	if err != nil {
		return 0, err
	}

	manifest, err := m.Manifest(ctx, bucket, key)
	if err != nil {
		return 0, err
	}

	path := m.stagingPath(bucket, key)
	current, err := m.stagingSize(bucket, key)
	if err != nil {
		return 0, err
	}
	// A session the manifest's metadata does not mark is not an open tus
	// session — it is a Stage-path file, or a session whose completion
	// already fired — and neither takes a chunk. A marked one takes no
	// chunk once its file has reached the declared length.
	length, marked := tusLengthMarked(manifest.Metadata)
	if !marked || (length >= 0 && current >= length) {
		return current, ErrOffsetMismatch
	}
	if offset != current {
		return current, ErrOffsetMismatch
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return current, fmt.Errorf("storage: staging %q: %w", key, err)
	}
	written, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		return current, fmt.Errorf("storage: staging %q: %w", key, copyErr)
	}
	if closeErr != nil {
		return current, fmt.Errorf("storage: staging %q: %w", key, closeErr)
	}

	fresh := current + written
	if err := m.manifests.TouchStaging(ctx, m.db, bucketID, key, fresh, micros(time.Now())); err != nil {
		return fresh, err
	}
	return fresh, nil
}

// TusComplete hands a finished session to the queue. The length check is
// the last word on wholeness: the staging file must have reached the
// length the session declared, so a caller that completes early is refused
// rather than served an upload of missing bytes. Completion also strips
// the session marker from the metadata: the row a finished staging file
// leaves is an ordinary staged upload, so the startup sweep is the path an
// enqueue failure recovers through, and the expiry job stops claiming the
// row the moment the marker is gone.
func (m *Manager) TusComplete(ctx context.Context, bucket, key string) error {
	manifest, err := m.Manifest(ctx, bucket, key)
	if err != nil {
		return err
	}
	size, err := m.stagingSize(bucket, key)
	if err != nil {
		return err
	}
	if length := tusLength(manifest.Metadata); length >= 0 && size < length {
		return ErrLengthMismatch
	}

	if _, marked := manifest.Metadata[TusLengthKey]; marked {
		delete(manifest.Metadata, TusLengthKey)
		if err := m.UpdateMetadata(ctx, bucket, key, manifest.Metadata); err != nil {
			return err
		}
	}
	if m.uploadEnqueuer == nil {
		return nil
	}
	if err := m.uploadEnqueuer.EnqueueUpload(ctx, bucket+"/"+key); err != nil {
		return err
	}
	m.metrics.recordSettled(ctx)
	return nil
}

// tusLength reads the declared length a session's metadata carries. The
// value came out of a JSON document, so its natural type is float64; a
// session whose metadata names no length is unlimited (negative), which
// only a begin without one can produce — and TusBegin always writes one.
func tusLength(metadata map[string]any) int64 {
	length, _ := tusLengthMarked(metadata)
	return length
}

// tusLengthMarked is tusLength with the marker's presence: a metadata that
// names no length is a row no tus session owns.
func tusLengthMarked(metadata map[string]any) (int64, bool) {
	switch length := metadata[TusLengthKey].(type) {
	case float64:
		return int64(length), true
	case int64:
		return length, true
	case int:
		return int64(length), true
	default:
		return -1, false
	}
}

// TusDiscard ends a session without storing anything: the staging file is
// removed and its manifest row deleted, the state a termination request
// leaves and the expiry job reclaims.
func (m *Manager) TusDiscard(ctx context.Context, bucket, key string) error {
	bucketID, err := m.resolveBucket(ctx, bucket)
	if err != nil {
		return err
	}
	if err := m.removeStaging(bucket, key); err != nil {
		return err
	}
	return m.manifests.Delete(ctx, m.db, bucketID, key)
}

// PendingStagedRefs answers the bucket-scoped references whose manifest is
// still pending and carries no session marker — the Stage-path uploads a
// dead run staged but never enqueued. The startup sweep walks them; a
// session's interrupted bytes are the expiry job's, not the sweep's.
func (m *Manager) PendingStagedRefs(ctx context.Context) ([]string, error) {
	return m.manifests.PendingRefs(ctx, m.db)
}

// ExpireSessions removes every session whose row has sat pending past the
// window: the staging file and the row go together, and the answer names
// what was reclaimed. before is the instant the caller has computed — the
// job owns the clock.
func (m *Manager) ExpireSessions(ctx context.Context, before time.Time) (int, error) {
	refs, err := m.manifests.ExpiredSessionRefs(ctx, m.db, before)
	if err != nil {
		return 0, err
	}
	for _, ref := range refs {
		bucket, key, splitErr := SplitRef(ref)
		if splitErr != nil {
			return 0, splitErr
		}
		if discardErr := m.TusDiscard(ctx, bucket, key); discardErr != nil {
			return 0, fmt.Errorf("storage: expire %q: %w", ref, discardErr)
		}
	}
	return len(refs), nil
}

// truncateStaging makes the session's staging file start empty. A file a
// previous session or a previous attempt left is replaced, not appended
// to: the offset a HEAD answers is this file's size, and a stale byte
// count would lie to the resuming client.
func (m *Manager) truncateStaging(bucket, key string) error {
	if err := os.MkdirAll(m.staging, 0o755); err != nil {
		return fmt.Errorf("storage: staging directory: %w", err)
	}
	target := m.stagingPath(bucket, key)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("storage: staging %q: %w", key, err)
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("storage: staging %q: %w", key, err)
	}
	return f.Close()
}

// stagingSize reads the staging file's current size — the offset a HEAD
// answers and a PATCH's claim is checked against. A missing file is an
// empty session, not an error.
func (m *Manager) stagingSize(bucket, key string) (int64, error) {
	info, err := os.Stat(m.stagingPath(bucket, key))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("storage: staging %q: %w", key, err)
	}
	return info.Size(), nil
}

// removeStaging deletes the session's staging file. A file that is already
// gone is the state the caller asked for.
func (m *Manager) removeStaging(bucket, key string) error {
	err := os.Remove(m.stagingPath(bucket, key))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: staging %q: %w", key, err)
	}
	return nil
}
