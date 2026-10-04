# Storage

Storage is the file engine: bucket-scoped, whole-file uploads over a local filesystem or an
S3-compatible object store, with the manifest in PostgreSQL. The request path is one local disk
write; everything else runs on the durable queue. The resumable-upload protocol (tus 1.0.0,
implemented in-house) is the engine's client surface. The engine takes typed options (driver,
local path, S3 connection, telemetry namespace, link path prefix) and reads nothing itself; the
schema mapping lives in `internal/config`'s adapter and the buckets are the app's rows.

> **Relation to the queue:** storage is the payload; the queue (`internal/queue`) is the
> executor. The code that finished a write — a `Stage` caller or the tus completion — enqueues
> a `StorageUploadTask` naming the `bucket/key` reference, and the queue owns execution,
> retries, backoff, and the archive. A sync is idempotent, so a replayed task finishes the
> round the crash interrupted.

## Buckets

A **bucket** is a row in `storage_buckets`: a unique name (one path segment, ≤ 100 characters,
validated in Go), a nullable `file_size_limit`, and a nullable `allowed_mime_types` list (NULL
= unlimited / any). Every file lives inside one bucket — the name is the container the backend
addresses the file by: the local backend writes `storage.local_path/uploads/{bucket}/{key}`
(the engine's own subtrees `staging/`, `logs/`, `backup/`, `config/` sit beside `uploads/` at
the top level), the S3 driver writes into the bucket of the same name — the logical bucket and
the physical one are the same, so `{endpoint}/{bucket}/{key}` is the object's own URL under
path style. The seeded `devbucket` bucket is where a feature that does not care about buckets
lands; the `storage.default_bucket` appconfig setting can name another.
`modules/storage` (`saka.storage.v1.BucketService`) is the Admin-gated CRUD over the rows;
a creation provisions the physical container through `EnsureBucket` before the row is written,
and deletion refuses a non-empty bucket and the setting-named one.

## Features

- **Whole-file storage** — a staged file is hashed (SHA-256 of the whole file) and stored
  under its bucket-scoped key; both drivers keep the same `/{bucket}/{key}` tree, the local
  one under `storage.local_path/uploads/{bucket}/{key}`, the S3 one in the bucket the row
  names
- **Manifest in PostgreSQL** — `storage_objects` (migration `00009`, `UNIQUE (bucket_id, key)`,
  FK ON DELETE RESTRICT) is the durable record; the content hash is the one-value "the backend
  already holds these bytes" check
- **Cheap request path** — `Stage` writes the file to the staging directory through a temp
  file and a rename and commits the intent row; no reader ever sees a half-written name
- **Resumable rounds** — a pending manifest whose staging fingerprint (size + mtime) matches
  is trusted for its content hash: a retry skips the hashing pass and goes straight to the PUT
- **tus resumable uploads** — the protocol at `/api/uploads` (see below): create, probe,
  append, terminate, expire
- **Flexible, validated keys** — `storage.Key("avatar", userID, "128.png")` composes the
  multi-purpose name a feature owns; every manager entry point refuses traversal, absolute,
  and hidden segments before a path is built
- **Per-file metadata** — a JSONB column the feature owns (content type, original file name,
  owner); written at stage time, rewritten by `UpdateMetadata`, carried but never read by the
  engine — except the tus session's length marker, which the protocol state reads and
  completion strips
- **Upload hooks** — `WithBeforeSync` (the gate: validate, preprocess) and `WithAfterSync`
  (the post-processing point: thumbnail, notification), nil by default, idempotence their
  contract
- **Deterministic settle** — no staging watcher: the finished write's own code enqueues the
  upload, the boot-time sweep re-enqueues the staged rows a dead run left, and
  `storage_upload_expiry` reclaims the interrupted tus sessions
- **Garbage collection as a recurring job** — `storage_gc` sweeps the backend against the
  manifest's key set, draining whatever a delete left unreferenced
- **Streamed reads** — `Manager.Open` returns an `io.ReadCloser` the backend serves on
  demand; no whole-file buffer exists on the read path

## Architecture

```mermaid
flowchart TB
    subgraph Request path
        H[Feature handler or tus completion] -->|Stage: write + intent row| ST[(staging/bucket/key)]
        H -->|metadata + fingerprint| SO[(storage_objects)]
        H -->|StorageUploadTask| Q[queue]
    end

    subgraph Sync (queue worker)
        Q -->|claim| S[Manager.Sync]
        S -->|reuse or hash the file| S -->|PUT whole| BE[Backend /{bucket}/{key}]
        S -->|ready manifest| SO
        S -->|remove| ST
    end

    subgraph Backend
        FS[Local FS]
        S3[S3-compatible]
    end

    BE --> FS
    BE --> S3

    SW[upload sweep runner] -->|re-enqueue staged rows at boot| Q
    EX[storage_upload_expiry job] -->|reclaim dead sessions| ST
    GC[storage_gc job] -->|sweep unreferenced objects| BE
```

**One sync, two fast paths.** The ready manifest with a matching content hash uploads nothing;
the pending manifest with a matching staging fingerprint skips the hashing pass. Everything
else in the round is the same code a first upload runs.

**Crash safety by ordering.** The pending checkpoint lands before the bytes travel, the ready
manifest commits after the PUT returns, and the staging copy is removed only if its
fingerprint still matches what the sync read. A crash anywhere leaves either a staging file to
re-sync or an unreferenced object to collect — never a manifest naming missing bytes, and
never a deleted file whose re-stage was destroyed mid-round.

**Why no chunks.** The earlier design cut a file into content-addressed chunks the backend
held individually, buying cross-file dedup and per-chunk resume at the cost of a bucket of
hash-named objects no final file ever appeared in. A whole-file PUT answers the same
durability — the staging file and the manifest row are the resume points — so the chunk layer
is gone rather than kept as a second representation. The trade is explicit: a change replaces
its object whole (no per-chunk diff), and the single-PUT ceiling (5 GiB on the services this
targets) is a deployment ceiling, not a code path.

## The tus protocol

`tus.go` (engine) and `tus_handler.go` (protocol) speak tus 1.0.0 in-house:
core methods plus `creation-with-upload`, `termination`, and `expiration`. There is no
progress route — the client reads the offset it needs from the protocol's own `HEAD`.

| Method | Path | What it does |
| ------ | ---- | ------------ |
| OPTIONS | `/api/uploads` | Discovery: `Tus-Version`, extensions. Public — no bearer. |
| POST | `/api/uploads` | Creation: `Upload-Length` + `Upload-Metadata` (bucket, key, filetype). A body is the first chunk. 201 with `Location` and `Upload-Offset`. |
| HEAD | `/api/uploads/{bucket}/{key}` | The staging file's size as `Upload-Offset`. |
| PATCH | `/api/uploads/{bucket}/{key}` | One `application/offset+octet-stream` chunk at the claimed offset; 409 on a mismatch. Reaching the declared length completes inside the same request. |
| DELETE | `/api/uploads/{bucket}/{key}` | Termination: the staging file and the manifest row go together. |

The engine's methods behind the handler: `TusBegin` (empty staging file + pending row carrying
the `tus_upload_length` marker), `TusOffset`, `TusAppend` (the claimed offset is the whole
concurrency story; a session the marker no longer names — Stage-path or completed — refuses
appends), `TusComplete` (the length check, marker strip, and enqueue through the
`UploadEnqueuer` seam), `TusDiscard`. `ExpireSessions` is the expiry job's door: every session
whose last activity rests past `TusSessionExpiry` (24 h) loses its staging file and its row.
The bucket's existence, size limit, and mime allowlist are checked at creation, before any
byte travels. Any signed-in account may upload into any bucket — per-bucket write permissions
are a later surface.

The `metadata` parser reads the protocol's base64 pairs in both the padded and the raw
alphabet — clients disagree on the padding, and a value that decodes under either is a name,
not a refusal. The debug build serves `/debug/file-upload`, a probe page that drives the whole
flow with `tus-js-client`'s UMD bundle from the CDN.

## Requirements

- Go >= 1.27
- PostgreSQL >= 18 (the manifest tables; shared `datastore` pool)
- One backend: the local filesystem, or any S3-compatible service (aws-sdk-go-v2; path style
  for MinIO/Silo)
- `internal/queue` — the upload, the expiry, and the garbage collection run as queue jobs
- Docker for the tests (testcontainers: Postgres, MinIO)

## Wiring

The package is a directory of the main module and is not published. The composition root wires it
in `internal/registry`: the manager is built from the shared `datastore.Postgres` pool, the
engine's options (`Config.StorageOptions()`), and the staging directory; the queue provider hands
the manager its `UploadEnqueuer` (`WithUploadEnqueuer`) and its after-sync hook, both adapted over
the client
the provider builds. The storage jobs are registered in `internal/jobs/register.go`
(`Register` skips them when the manager is absent, so a build without storage runs the rest
unchanged); the sweep is the `upload sweep` entry in the registry's runners.

Schema is owned by the migration (`internal/database/migrations/00009_create_filestore_tables.sql`) —
run `task db:migrate`; the engine never creates tables itself.

The engine logs through `log/slog` — the process logger `serve` hands over — so storage lines
reach every configured sink and carry the trace context of the run.

## Quick Start

### 1. Stage a File

Staging is the whole request path: one buffered local write and one intent row.

```go
// Key composes the feature's own naming; each part becomes one safe
// path segment. ValidateKey refuses any key that could escape staging.
key, err := storage.Key("avatar", userID, "128.png")
if err != nil {
    return err
}

err = manager.Stage(ctx, storage.DefaultBucketName, key, r, map[string]any{
    "content_type": contentType,
    "original":     filename,
    "owner_id":     userID,
})

// The write that finished is the fact the queue carries: enqueue
// without waiting for it.
_, err = queueClient.Add(jobs.StorageUploadTask{
    Key: bucket + "/" + key,
}).Save()
```

A feature may also name the bucket the `storage.default_bucket` setting resolves to —
`manager.Bucket(ctx, name)` answers the row a feature checks limits against.

### 2. Read, Update, Delete

```go
// One stream the backend serves on demand.
rc, err := manager.Open(ctx, bucket, key)
defer func() { _ = rc.Close() }()

// The manifest a feature reads to know what the backend holds.
manifest, err := manager.Manifest(ctx, bucket, key)

// Metadata is rewritten any time; status and content hash are untouched.
err = manager.UpdateMetadata(ctx, bucket, key, map[string]any{"original": "renamed.png"})

// Deletion removes the manifest row first, the object second.
err = manager.Delete(ctx, bucket, key)
```

### 3. Hooks

Both are optional and wired in the registry, where the feature that owns the behavior lives:

```go
manager.
    WithBeforeSync(func(ctx context.Context, ref, path string) error {
        // The gate: runs before the fingerprint is read, so a rewrite
        // here is what gets hashed. An error refuses the file.
        return validateImage(path)
    }).
    WithAfterSync(func(ctx context.Context, manifest storage.Manifest) error {
        // The post-processing point: the object is durable, the
        // manifest is ready, the staging copy still exists.
        return client.Add(ThumbnailTask{Ref: manifest.Bucket + "/" + manifest.Key}).Save()
    })
```

**Contracts** (documented on the types, enforced by review):

- **Idempotent** — the queue's retries replay both hooks; a failed after-hook is retried
  through the finished-manifest fast path, without re-hashing or re-uploading
- **Quick** — long work belongs in a job the hook enqueues, not in the upload worker's slot
- **Before** receives the staging path and may rewrite or replace the file; an error fails
  the round, and a hook that keeps refusing a file walks it to the dead letters

### 4. Garbage Collection

`storage_gc` runs on its own schedule (`DefaultStorageGCInterval`, six hours) and needs
nothing from a feature. To collect immediately — rarely needed outside tests:

```go
removed, err := manager.CollectGarbage(ctx)
```

## Configuration

### `storage` section

| Key                  | Default   | Description                                                                     |
| -------------------- | --------- | ------------------------------------------------------------------------------- |
| `storage.driver`     | `local`   | `local` or `s3`; only the selected backend's client is built                     |
| `storage.local_path` | `storage` | The one data directory: `uploads/` (the buckets), `staging/`, and the log sink live under it |
| `storage.s3.*`       | —         | `access_key`, `secret_key` (both secrets), `endpoint_url`, `force_path_style`, `region` |

There is no watcher section — the settle mechanisms are the deterministic enqueue, the boot
sweep, and the expiry job. There is no bucket key either: the `storage_buckets` rows are the
buckets, and the object store's containers are created (idempotently) through `EnsureBucket`
when the management surface creates a row. The presigned-link lifetime is the
`storage.signed_url_expires` appconfig setting, not a config-file key. The local path needs no
flag and no variable of its own — a deployment sets it in the config file. Postgres plus local
storage are enough; no backend is required.

### Manager construction

| Parameter | Meaning                                                                      |
| --------- | ---------------------------------------------------------------------------- |
| `store`   | The backend (`New` builds the one `storage.driver` selects)                   |
| `db`      | The shared Postgres pool (`Querier` + `WithTx`)                               |
| `staging` | The staging directory (the registry joins `storage.local_path` + `staging`)   |

Seam wiring after construction: `WithBeforeSync`, `WithAfterSync`, `WithUploadEnqueuer` (the
tus completion's door to the queue).

## API Reference

### `NewManager(store Store, db DB, staging string, log, opts ...Options) *Manager`

Builds the engine: the manifest store, the staging directory. Nothing touches the backend or
the database yet. The options carry the telemetry identity (scope, namespace); zero values
take the package's own.

### `(*Manager).Stage(ctx, bucket, key, r io.Reader, metadata map[string]any) error`

Writes the file into `staging/{bucket}/{key}` through a temp file + rename and commits the
intent row — metadata and the staging fingerprint — before the first byte travels. Re-staging
a key replaces its metadata and makes the stored manifest stale, so the next sync uploads the
new version.

### `(*Manager).Sync(ctx, ref string) error`

The upload round the `StorageUploadTask` processor runs over the `bucket/key` reference: hook
gate → fingerprint → reuse or hash → PUT whole → ready commit → after hook → staging cleanup.
Idempotent; the queue's retries replay it. A reference with no staging file returns nil —
another attempt already finished.

### `(*Manager).Open(ctx, bucket, key string) (io.ReadCloser, error)`

A stream the backend serves on demand. `ErrNotFound` for a key nothing stored — the manifest
row is not proof the bytes exist.

### `(*Manager).Manifest(ctx, bucket, key string) (Manifest, error)`

The stored state a feature reads: status, content hash, metadata. The id answers in the wire
form (`file_` TypeID).

### `(*Manager).UpdateMetadata(ctx, bucket, key string, metadata map[string]any) error`

Replaces the metadata; works before and after the upload (a key nothing stored yet gets a
pending row).

### `(*Manager).Delete(ctx, bucket, key string) error`

Removes the manifest row and then the object the backend holds; the staging copy goes with
it.

### `(*Manager).Bucket(ctx, name string) (Bucket, error)`

The bucket row a feature checks limits against. `ErrNotFound` for a name the table does not
hold.

### `(*Manager).CollectGarbage(ctx) (int, error)`

Removes every backend object no manifest names; returns the count.

### `Key(parts ...string) (string, error)` / `ValidateKey(key string) error`

The naming helpers. `Key` sanitizes each part onto one path segment (letters, digits, dot,
dash, underscore stay; the rest becomes `_`); both refuse empty parts, `.`/`..`, hidden
segments, absolute paths, and separators where one part was asked for. `ValidateBucketName`
refuses a name that is not one such segment. `SplitRef(ref)` splits the `bucket/key`
composite the queue and the logs carry.

### `Store`

The backend contract: `EnsureBucket` (the container, idempotent), `Get`, `Put` (whole, with
the byte size), `Delete`, `List` (one bucket's keys). Every method carries the bucket the
file belongs to — the storage_buckets row's own name. `New(Options)` builds the one the
`storage.driver` section names (`FS` over `storage.local_path`, `S3` over `storage.s3.*`);
both translate their protocol's not-found shape into `ErrNotFound` at the edge.

### Private files and signed links

A manifest row's `is_private` flag (set at stage time through `StageOptions`) marks a file
readable only over a signed link: a plain read of `/storage` answers the 404 a missing
object gets — never a 403 that would confirm the key exists — and the response carries
`Cache-Control: private, no-store`, so the bytes are not cached where a later holder of the
link could dig them out. The link is `?exp=<unix>&sig=<hex>`: the expiry is inside the HMAC
payload (`storage.Signer`, keyed with `app.secret_key`, constant-time compare), so a
truncated lifetime cannot be edited longer. `Manager.SignedURL` composes the full link; its
default ttl is the `storage.signed_url_expires` appconfig setting, read at every mint. A
run without a signer fails every private read closed; public files are untouched.

## Database Schema

Two tables, created by migration `internal/database/migrations/00009_create_filestore_tables.sql`:

### `storage_buckets`

| Column                | Type          | Description                                                      |
| --------------------- | ------------- | ---------------------------------------------------------------- |
| `id`                  | `UUID`        | Primary key, `uuidv7()` default                                   |
| `name`                | `TEXT`        | The namespace's name, `UNIQUE` (≤ 100 characters, validated in Go) |
| `file_size_limit`     | `BIGINT`      | NULL = unlimited; the creation's `Upload-Length` is held to it     |
| `allowed_mime_types`  | `TEXT[]`      | NULL = any; the creation's declared type is held to the list       |
| `created_at` / `updated_at` | `TIMESTAMPTZ` | `updated_at` maintained by trigger                            |

### `storage_objects`

| Column          | Type          | Description                                                        |
| --------------- | ------------- | ------------------------------------------------------------------ |
| `id`            | `UUID`        | Primary key, `uuidv7()` default                                     |
| `bucket_id`     | `UUID`        | FK to `storage_buckets(id)` ON DELETE RESTRICT                      |
| `key`           | `TEXT`        | The feature's naming, `UNIQUE (bucket_id, key)`                     |
| `size`          | `BIGINT`      | File size in bytes                                                 |
| `content_hash`  | `TEXT`        | SHA-256 of the whole file — the one-value "already stored" check   |
| `status`        | `TEXT`        | `pending` / `ready` / `failed`                                     |
| `metadata`      | `JSONB`       | The feature's own record; the engine reads only the tus marker     |
| `staging_size`  | `BIGINT`      | Fingerprint half one: the staging file's size                      |
| `staging_mtime` | `TIMESTAMPTZ` | Fingerprint half two: the staging file's modification time         |
| `created_at` / `updated_at` | `TIMESTAMPTZ` | `updated_at` maintained by trigger                    |

Indexes on `status` and `content_hash`.

## Error Handling

- **A refused key or bucket name** — `ErrInvalidKey` before any path is built or any byte
  moves; traversal shapes never reach the filesystem
- **A failed sync** — the queue retries it; the checkpoint makes the retry cheap, and a round
  that exhausts its attempts rests in the archive with its error
- **A failed after-hook** — the data is already durable; the retry replays the hook through
  the fast path
- **A re-staged file under a running sync** — the TOCTOU guard leaves it for a fresh round
  and fails this one, so nothing is destroyed unrecorded
- **A lost staging file** — `Sync` returns nil: another attempt finished, or the caller owns
  the lifecycle
- **A misplaced chunk** — the tus append refuses an offset the staging file's size does not
  name (409 to the client); the resume path is a HEAD followed by a correct PATCH
- **An orphaned object** — a crash between a manifest deletion and the object's removal
  leaves one for `storage_gc`; it is never a manifest naming missing bytes

## Testing

Tests run against real containers (testcontainers) using the shared `pkg/testutils` helpers:
`StartPostgres` per test database (migrations applied), `StartMinIO` for the S3 driver. Each
fixture uses topic vocabulary (Dan Brown, Harry Potter), so two tests — or two runs — sharing
one bucket cannot answer each other's probes.

```bash
go test ./framework/storage/
go test -race ./framework/storage/
```

## Design Decisions

| Decision | Rationale |
| -------- | --------- |
| Buckets as table rows, not config | A namespace a feature names at write time is data the serving mount resolves; the management surface and the default-bucket setting read the same table |
| Whole-file PUT, no chunk store | Both drivers keep the same tree of keys a final file appears in; the staging file and the manifest are the resume points |
| Manifest in Postgres, not sidecar files | Migrations are the schema truth; the manifest survives a lost disk and is queryable |
| Staging first, upload on the queue | The request path is one local write; a slow backend never sits inside a request |
| Deterministic settle, no watcher | The finished write's own code knows the file is whole; two settle mechanisms over one staging tree race each other, and a debounce is latency the enqueue does not need |
| tus in-house, protocol-only | The protocol's headers carry the state; the library's filestore and S3 layout would run a second manifest beside the engine's |
| Content hash = SHA-256 of the file | One read tells whether the backend holds these bytes; an unchanged replay uploads nothing |
| Fingerprint (size + mtime) reuse | A retry skips the hashing pass — the one cost a large file cannot afford twice |
| Hooks at the round's edges, nil default | Features get validation and post-processing exactly once per finished upload, with zero cost when absent |
| Idempotence as the hooks' contract | The queue retries the round; a hook that cannot be replayed cannot be safe |
| Garbage collection as a recurring job | Crash leftovers are swept on a clock instead of a goroutine the process must babysit |
| Keys validated at every entry point | The staging path is built from the key; the door is the only place a traversal can be stopped |
| Schema owned by migrations | One source of schema truth; the engine never mutates the schema |
