package storage

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"go.jetify.com/typeid"

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

// The manifest tables the builders name, the one-constant-per-table rule the
// queue's store follows.
const (
	objectsTable = "storage_objects"
	bucketsTable = "storage_buckets"
)

// File is one stored file. The content hash is the SHA-256 of the whole
// file: one read tells whether the bytes on the staging side still match
// what the backend holds.
//
// Bucket is the name of the bucket the file lives in (the logical namespace
// the row is scoped by) and BucketID its storage_buckets row id. Metadata is
// the feature's own free-form record — content type, original file name,
// owner — carried and rewritten but never interpreted here. IsPrivate marks
// the file signed-link-only: the /storage mount answers a plain read with
// the 404 a missing object gets, and the bytes travel only behind a link
// the Signer minted. StagingSize and StagingMtime fingerprint the staging
// file the hash was computed from: a retry that finds both unchanged
// reuses the stored hash instead of reading the file again.
//
// ID is the object row's identifier in its wire form — the TypeID whose
// prefix tells a reader of a log line or a support ticket what it names
// without a lookup. The column stays a UUID; the conversion lives in the
// scan and nowhere else.
type File struct {
	ID           string
	BucketID     string
	Bucket       string
	Key          string
	Size         int64
	ContentHash  string
	Status       string
	IsPrivate    bool
	Metadata     map[string]any
	StagingSize  int64
	StagingMtime time.Time
}

// FileIDPrefix is the TypeID prefix of an object row's identifier.
type FileIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (FileIDPrefix) Prefix() string { return "file" }

// FileID is the typed identifier of one row of the objects table, in its
// wire form.
type FileID = typeid.TypeID[FileIDPrefix]

// FormatObjectID renders the wire form of an object row's UUID. Rows read
// from the database always carry a valid UUID, so the render cannot fail;
// an invalid one answers the empty string, which no consumer should
// mistake for an id.
func FormatObjectID(raw uuid.UUID) string {
	id, err := typeid.FromUUID[FileID](raw.String())
	if err != nil {
		return ""
	}
	return id.String()
}

// formatObjectID reads the object row's UUID out of the text form its
// column scans into, and answers the wire form. A column value that is not
// a UUID is a database defect the read refuses.
func formatObjectID(raw string) (string, error) {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("storage: object id %q: %w", raw, err)
	}
	return FormatObjectID(parsed), nil
}

// Manifest is one stored file's record.
type Manifest = File

// ErrNoManifest is what Load answers for a bucket/key nothing stored yet.
var ErrNoManifest = errors.New("storage: no manifest for key")

// Manifests reads and writes the manifest table. Every method takes the
// Querier to run on, so a caller that must be transactional passes its
// transaction and one that must not passes the pool — the repository never
// opens a transaction of its own, the same rule the seeders follow. Bucket
// scoping is by bucket id, which the manager resolves from the bucket name
// before calling in.
type Manifests struct{}

// NewManifests builds the manifest repository.
func NewManifests() *Manifests { return &Manifests{} }

// Load reads the manifest a bucket/key holds. ErrNoManifest for a pair
// nothing stored yet — the answer that makes an upload the file's first.
func (Manifests) Load(ctx context.Context, q datastore.Querier, bucketID, key string) (Manifest, error) {
	fb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	fb.Select(
		"o.id", "b.name", "o.key", "o.size", "o.content_hash", "o.status",
		"o.is_private", "o.metadata", "o.staging_size", "o.staging_mtime",
	)
	fb.From(objectsTable + " o")
	fb.Join(bucketsTable + " b ON b.id = o.bucket_id")
	fb.Where(fb.Equal("o.bucket_id", bucketID), fb.Equal("o.key", key))

	var file File
	var rawID, metadata string
	query, args := fb.Build()
	err := q.QueryRow(ctx, query, args...).Scan(
		&rawID, &file.Bucket, &file.Key, &file.Size, &file.ContentHash, &file.Status,
		&file.IsPrivate, &metadata, &file.StagingSize, &file.StagingMtime,
	)
	if errors.Is(err, datastore.ErrNoRows) {
		return Manifest{}, ErrNoManifest
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("storage: load file %q: %w", key, err)
	}
	file.BucketID = bucketID
	if file.ID, err = formatObjectID(rawID); err != nil {
		return Manifest{}, fmt.Errorf("storage: load file %q: %w", key, err)
	}
	if err := json.Unmarshal([]byte(metadata), &file.Metadata); err != nil {
		return Manifest{}, fmt.Errorf("storage: decode metadata of %q: %w", key, err)
	}
	return file, nil
}

// Save commits one manifest. It runs inside the caller's transaction, so a
// manifest is visible whole or not at all. A pending save is the checkpoint
// a retry resumes from; a ready save is the finished one. IsPrivate is
// deliberately absent from the write set: the stage owns the flag, and a
// sync's round trip must not flip what the staging write decided.
func (Manifests) Save(ctx context.Context, q datastore.Querier, file File) error {
	metadata, err := json.Marshal(file.Metadata)
	if err != nil {
		return fmt.Errorf("storage: encode metadata of %q: %w", file.Key, err)
	}

	// The trailing clause rides on the builder the flavor documents for
	// exactly this shape: an upsert that hands back the row it wrote.
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(objectsTable)
	ib.Cols("bucket_id", "key", "size", "content_hash", "status", "metadata", "staging_size", "staging_mtime")
	ib.Values(file.BucketID, file.Key, file.Size, file.ContentHash, file.Status, metadata, file.StagingSize, nullableTime(file.StagingMtime))
	ib.SQL("ON CONFLICT (bucket_id, key) DO UPDATE SET " +
		"size = EXCLUDED.size, " +
		"content_hash = EXCLUDED.content_hash, " +
		"status = EXCLUDED.status, " +
		"metadata = EXCLUDED.metadata, " +
		"staging_size = EXCLUDED.staging_size, " +
		"staging_mtime = EXCLUDED.staging_mtime " +
		"RETURNING id")

	query, args := ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("storage: save file %q in bucket %s: %w", file.Key, file.BucketID, err)
	}
	return nil
}

// Stage records the intent to store a file: the row exists before the first
// byte travels, carrying the feature's metadata and the staging fingerprint.
// An upload that never arrives leaves a pending row the next Stage or Sync
// of the same bucket/key overwrites, not a half-stored file.
//
// A re-stage clears the stored content hash. The fingerprint written here is
// the new file's, so the row's hash — the previous version's — must not
// survive it: a Sync that reads a matching fingerprint would otherwise skip
// hashing and upload the new bytes under the old digest. Clearing it means a
// re-stage always pays for one hash, and a retry of an unchanged file still
// takes the reuse path off the checkpoint the sync itself committed.
func (Manifests) Stage(ctx context.Context, q datastore.Querier, bucketID, key string, size int64, mtime time.Time, metadata map[string]any, isPrivate bool) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("storage: encode metadata of %q: %w", key, err)
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(objectsTable)
	ib.Cols("bucket_id", "key", "size", "content_hash", "status", "is_private", "metadata", "staging_size", "staging_mtime")
	ib.Values(bucketID, key, size, "", StatusPending, isPrivate, encoded, size, nullableTime(mtime))
	ib.SQL("ON CONFLICT (bucket_id, key) DO UPDATE SET " +
		"content_hash = EXCLUDED.content_hash, " +
		"is_private = EXCLUDED.is_private, " +
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

// UpdateMetadata replaces the metadata a bucket/key carries, keeping
// everything else — the status, the content hash — exactly as it is. A pair
// nothing stored yet gets a pending row, so metadata can be set before or
// after the bytes travel.
func (Manifests) UpdateMetadata(ctx context.Context, q datastore.Querier, bucketID, key string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("storage: encode metadata of %q: %w", key, err)
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(objectsTable)
	ib.Cols("bucket_id", "key", "content_hash", "status", "metadata")
	ib.Values(bucketID, key, "", StatusPending, encoded)
	ib.SQL("ON CONFLICT (bucket_id, key) DO UPDATE SET metadata = EXCLUDED.metadata")

	query, args := ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("storage: update metadata of %q: %w", key, err)
	}
	return nil
}

// Delete removes a bucket/key's manifest row. ErrNotFound for a pair
// nothing stored; the caller deletes the bytes after the row is gone, so a
// crash between the two leaves an unreferenced object for the garbage
// collection, never a manifest that names missing bytes.
func (Manifests) Delete(ctx context.Context, q datastore.Querier, bucketID, key string) error {
	db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db.DeleteFrom(objectsTable)
	db.Where(db.Equal("bucket_id", bucketID), db.Equal("key", key))

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

// TouchStaging moves a pending row's fingerprint to where the staging file
// now sits: the bytes a session has received. The write is the receipt
// each chunk leaves, so a retry's checkpoint compares against what the
// last chunk wrote and the expiry job can age the row by its last activity.
func (Manifests) TouchStaging(ctx context.Context, q datastore.Querier, bucketID, key string, size int64, mtime time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(objectsTable)
	ub.Set(
		ub.Assign("size", size),
		ub.Assign("staging_size", size),
		ub.Assign("staging_mtime", mtime),
	)
	ub.Where(ub.Equal("bucket_id", bucketID), ub.Equal("key", key), ub.Equal("status", StatusPending))

	query, args := ub.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("storage: touch staging %q: %w", key, err)
	}
	return nil
}

// PendingRefs lists the bucket-scoped references whose manifest is pending
// and carries no session marker — the Stage-path uploads a dead run staged
// but never enqueued. A session's interrupted bytes are not here: the
// session's metadata key is the marker, and its staging file is whole only
// when its completion enqueue happened.
func (Manifests) PendingRefs(ctx context.Context, q datastore.Querier) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("b.name", "o.key")
	sb.From(objectsTable + " o")
	sb.Join(bucketsTable + " b ON b.id = o.bucket_id")
	sb.Where(sb.Equal("o.status", StatusPending), "NOT (o.metadata ? "+sb.Var(TusLengthKey)+")")

	query, args := sb.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list pending uploads: %w", err)
	}
	defer rows.Close()

	var refs []string
	for rows.Next() {
		var bucket, key string
		if err := rows.Scan(&bucket, &key); err != nil {
			return nil, fmt.Errorf("storage: scan pending upload: %w", err)
		}
		refs = append(refs, bucket+"/"+key)
	}
	return refs, rows.Err()
}

// ExpiredSessionRefs lists the bucket-scoped references of the sessions
// whose last activity rests before the instant the caller computed — the
// rows the expiry job reclaims, staging file and manifest row together.
func (Manifests) ExpiredSessionRefs(ctx context.Context, q datastore.Querier, before time.Time) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("b.name", "o.key")
	sb.From(objectsTable + " o")
	sb.Join(bucketsTable + " b ON b.id = o.bucket_id")
	sb.Where(sb.Equal("o.status", StatusPending), "o.metadata ? "+sb.Var(TusLengthKey), sb.LessThan("o.updated_at", before))

	query, args := sb.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list expired sessions: %w", err)
	}
	defer rows.Close()

	var refs []string
	for rows.Next() {
		var bucket, key string
		if err := rows.Scan(&bucket, &key); err != nil {
			return nil, fmt.Errorf("storage: scan expired session: %w", err)
		}
		refs = append(refs, bucket+"/"+key)
	}
	return refs, rows.Err()
}

// StoredKeys lists every key of one bucket at least one manifest row names.
// It is the keep-set the garbage collection diffs the backend's listing
// against: an object the backend holds and this set does not is
// unreferenced.
func (Manifests) StoredKeys(ctx context.Context, q datastore.Querier, bucketID string) (map[string]struct{}, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key")
	sb.From(objectsTable)
	sb.Where(sb.Equal("bucket_id", bucketID))

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

// StoredRefs lists every bucket-scoped reference (`bucket/key`) the manifest
// table names, across all buckets. It is the keep-set the garbage
// collection diffs the backend's physical listing against, since the backend
// stores each file at `bucket/key`.
func (Manifests) StoredRefs(ctx context.Context, q datastore.Querier) (map[string]struct{}, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("b.name", "o.key")
	sb.From(objectsTable + " o")
	sb.Join(bucketsTable + " b ON b.id = o.bucket_id")

	query, args := sb.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list stored references: %w", err)
	}
	defer rows.Close()

	refs := make(map[string]struct{})
	for rows.Next() {
		var bucket, key string
		if err := rows.Scan(&bucket, &key); err != nil {
			return nil, fmt.Errorf("storage: scan stored reference: %w", err)
		}
		refs[bucket+"/"+key] = struct{}{}
	}
	return refs, rows.Err()
}

// nullableTime hands a zero time to the driver as NULL, the value an absent
// staging fingerprint is.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
