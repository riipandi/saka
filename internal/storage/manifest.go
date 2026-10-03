package storage

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/internal/datastore"
)

// File status values. A file is pending while its bytes travel, ready once
// the manifest is committed, failed when a run gave up on it — the queue's
// own retry schedule decides when a failed one is tried again.
const (
	StatusPending = "pending"
	StatusReady   = "ready"
	StatusFailed  = "failed"
)

// storageFilesTable is the manifest table the builders name, the same
// one-constant-per-table rule the queue's store follows.
const storageFilesTable = "storage_files"

// File is one stored file. The content hash is the SHA-256 of the whole
// file: one read tells whether the bytes on the staging side still match
// what the backend holds.
//
// Metadata is the feature's own free-form record — content type, original
// file name, owner — carried and rewritten but never interpreted here.
// StagingSize and StagingMtime fingerprint the staging file the hash was
// computed from: a retry that finds both unchanged reuses the stored hash
// instead of reading the file again.
type File struct {
	ID           string
	Key          string
	Size         int64
	ContentHash  string
	Status       string
	Metadata     map[string]any
	StagingSize  int64
	StagingMtime time.Time
}

// Manifest is one stored file's record.
type Manifest = File

// ErrNoManifest is what Load answers for a key nothing stored yet.
var ErrNoManifest = errors.New("storage: no manifest for key")

// Manifests reads and writes the manifest table. Every method takes the
// Querier to run on, so a caller that must be transactional passes its
// transaction and one that must not passes the pool — the repository never
// opens a transaction of its own, the same rule the seeders follow.
type Manifests struct{}

// NewManifests builds the manifest repository.
func NewManifests() *Manifests { return &Manifests{} }

// Load reads the manifest a key holds. ErrNoManifest for a key nothing
// stored yet — the answer that makes an upload the file's first.
func (Manifests) Load(ctx context.Context, q datastore.Querier, key string) (Manifest, error) {
	fb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	fb.Select(
		"id", "key", "size", "content_hash", "status",
		"metadata", "staging_size", "staging_mtime",
	)
	fb.From(storageFilesTable)
	fb.Where(fb.Equal("key", key))

	var file File
	var metadata []byte
	query, args := fb.Build()
	err := q.QueryRow(ctx, query, args...).Scan(
		&file.ID, &file.Key, &file.Size, &file.ContentHash, &file.Status,
		&metadata, &file.StagingSize, &file.StagingMtime,
	)
	if errors.Is(err, datastore.ErrNoRows) {
		return Manifest{}, ErrNoManifest
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("storage: load file %q: %w", key, err)
	}
	err = json.Unmarshal(metadata, &file.Metadata)
	if err != nil {
		return Manifest{}, fmt.Errorf("storage: decode metadata of %q: %w", key, err)
	}
	return file, nil
}

// Save commits one manifest. It runs inside the caller's transaction, so a
// manifest is visible whole or not at all. A pending save is the checkpoint
// a retry resumes from; a ready save is the finished one.
func (Manifests) Save(ctx context.Context, q datastore.Querier, file File) error {
	metadata, err := json.Marshal(file.Metadata)
	if err != nil {
		return fmt.Errorf("storage: encode metadata of %q: %w", file.Key, err)
	}

	// The trailing clause rides on the builder the flavor documents for
	// exactly this shape: an upsert that hands back the row it wrote.
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(storageFilesTable)
	ib.Cols("key", "size", "content_hash", "status", "metadata", "staging_size", "staging_mtime")
	ib.Values(file.Key, file.Size, file.ContentHash, file.Status, metadata, file.StagingSize, nullableTime(file.StagingMtime))
	ib.SQL("ON CONFLICT (key) DO UPDATE SET " +
		"size = EXCLUDED.size, " +
		"content_hash = EXCLUDED.content_hash, " +
		"status = EXCLUDED.status, " +
		"metadata = EXCLUDED.metadata, " +
		"staging_size = EXCLUDED.staging_size, " +
		"staging_mtime = EXCLUDED.staging_mtime " +
		"RETURNING id")

	query, args := ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("storage: save file %q: %w", file.Key, err)
	}
	return nil
}

// Stage records the intent to store a file: the row exists before the first
// byte travels, carrying the feature's metadata and the staging fingerprint.
// An upload that never arrives leaves a pending row the next Stage or Sync
// of the same key overwrites, not a half-stored file.
//
// A re-stage clears the stored content hash. The fingerprint written here is
// the new file's, so the row's hash — the previous version's — must not
// survive it: a Sync that reads a matching fingerprint would otherwise skip
// hashing and upload the new bytes under the old digest. Clearing it means a
// re-stage always pays for one hash, and a retry of an unchanged file still
// takes the reuse path off the checkpoint the sync itself committed.
func (Manifests) Stage(ctx context.Context, q datastore.Querier, key string, size int64, mtime time.Time, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("storage: encode metadata of %q: %w", key, err)
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(storageFilesTable)
	ib.Cols("key", "size", "content_hash", "status", "metadata", "staging_size", "staging_mtime")
	ib.Values(key, size, "", StatusPending, encoded, size, nullableTime(mtime))
	ib.SQL("ON CONFLICT (key) DO UPDATE SET " +
		"content_hash = EXCLUDED.content_hash, " +
		"metadata = EXCLUDED.metadata, " +
		"staging_size = EXCLUDED.staging_size, " +
		"staging_mtime = EXCLUDED.staging_mtime, " +
		"status = EXCLUDED.status")

	query, args := ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("storage: stage file %q: %w", key, err)
	}
	return nil
}

// UpdateMetadata replaces the metadata a key carries, keeping everything
// else — the status, the content hash — exactly as it is. A key nothing
// stored yet gets a pending row, so metadata can be set before or after the
// bytes travel.
func (Manifests) UpdateMetadata(ctx context.Context, q datastore.Querier, key string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("storage: encode metadata of %q: %w", key, err)
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(storageFilesTable)
	ib.Cols("key", "content_hash", "status", "metadata")
	ib.Values(key, "", StatusPending, encoded)
	ib.SQL("ON CONFLICT (key) DO UPDATE SET metadata = EXCLUDED.metadata")

	query, args := ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("storage: update metadata of %q: %w", key, err)
	}
	return nil
}

// Delete removes a key's manifest row. ErrNotFound for a key nothing
// stored; the caller deletes the bytes after the row is gone, so a crash
// between the two leaves an unreferenced object for the garbage collection,
// never a manifest that names missing bytes.
func (Manifests) Delete(ctx context.Context, q datastore.Querier, key string) error {
	db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db.DeleteFrom(storageFilesTable)
	db.Where(db.Equal("key", key))

	query, args := db.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("storage: delete file %q: %w", key, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// StoredKeys lists every key at least one manifest row names. It is the
// keep-set the garbage collection diffs the backend's listing against: an
// object the backend holds and this set does not is unreferenced.
func (Manifests) StoredKeys(ctx context.Context, q datastore.Querier) (map[string]struct{}, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key")
	sb.From(storageFilesTable)

	query, args := sb.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list stored keys: %w", err)
	}
	defer rows.Close()

	keys := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("storage: scan stored key: %w", err)
		}
		keys[key] = struct{}{}
	}
	return keys, rows.Err()
}

// nullableTime hands a zero time to the driver as NULL, the value an absent
// staging fingerprint is.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
