# Storage

Storage is tango's file engine: whole-file uploads over a local filesystem or an S3-compatible
object store, with the manifest in PostgreSQL. The request path is one local disk write,
everything else runs on the durable queue.

> **Relation to the queue:** storage is the payload; the queue (`internal/queue`) is the
> executor. A staged file is noticed by the watcher (or named by any caller) as a
> `ChunkUploadTask`, and the queue owns execution, retries, backoff, and the archive. A sync
> is idempotent, so a replayed task finishes the round the crash interrupted.

## Features

- **Whole-file storage** — a staged file is hashed (SHA-256 of the whole file) and stored
  under the key its feature composed; both drivers keep the same tree of keys, the local one
  under `storage.local_path/files/<key>`, the S3 one under the configured prefix
- **Manifest in PostgreSQL** — `storage_files` (migration `00009`) is the durable record; the
  content hash is the one-value "the backend already holds these bytes" check
- **Cheap request path** — `Stage` writes the file to the staging directory through a temp
  file and a rename and commits the intent row; the watcher never sees a half-written name
- **Resumable rounds** — a pending manifest whose staging fingerprint (size + mtime) matches
  is trusted for its content hash: a retry skips the hashing pass and goes straight to the PUT
- **Batched backend probe** — none needed: a whole-file PUT is atomic, so a crash inside one
  leaves nothing behind and a retry re-PUTs idempotently
- **Flexible, validated keys** — `storage.Key("avatar", userID, "128.png")` composes the
  multi-purpose name a feature owns; every manager entry point refuses traversal, absolute,
  and hidden segments before a path is built
- **Per-file metadata** — a JSONB column the feature owns (content type, original file name,
  owner); written at stage time, rewritten by `UpdateMetadata`, carried but never read by the
  engine
- **Upload hooks** — `WithBeforeSync` (the gate: validate, preprocess) and `WithAfterSync`
  (the post-processing point: thumbnail, notification), nil by default, idempotence their
  contract
- **File watching** — `fsnotify` over every directory under staging (nested keys are
  first-class), per-path debounce, a start-up scan that picks up an upload that outlived its
  process
- **Garbage collection as a recurring job** — `storage_gc` sweeps the backend against the
  manifest's key set, draining whatever a delete left unreferenced
- **Streamed reads** — `Manager.Open` returns an `io.ReadCloser` the backend serves on
  demand; no whole-file buffer exists on the read path
- **Graceful shutdown** — the watcher stops through context cancellation; in-flight syncs are
  queue tasks and finish through the queue's own drain

## Architecture

```mermaid
flowchart TB
    subgraph Request path
        H[Feature handler] -->|Stage: write + intent row| ST[(staging dir)]
        H -->|metadata + fingerprint| SF[(storage_files)]
    end

    subgraph Watcher
        W[fsnotify + debounce] -->|ChunkUploadTask| Q[queue]
    end

    subgraph Sync (queue worker)
        Q -->|claim| S[Manager.Sync]
        S -->|reuse or hash the file| S -->|PUT whole| BE[Backend]
        S -->|ready manifest| SF
        S -->|remove| ST
    end

    subgraph Backend
        FS[Local FS]
        S3[S3-compatible]
    end

    BE --> FS
    BE --> S3

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

## Requirements

- Go >= 1.27
- PostgreSQL >= 18 (the manifest table; shared `datastore` pool)
- One backend: the local filesystem, or any S3-compatible service (aws-sdk-go-v2; path style
  for MinIO/Silo)
- `internal/queue` — the upload and the garbage collection run as queue jobs
- Docker for the tests (testcontainers: Postgres, MinIO)

## Wiring

The package lives inside the `tango` module and is not published. The composition root wires
it in `internal/registry`: the manager is built from the shared `datastore.Postgres` pool, the
configured backend, and the `storage` config section; the watcher is built over the manager's
staging directory and enqueues `ChunkUploadTask` through the queue client. The storage jobs
are registered in `internal/jobs/register.go` (`Register` skips them when the manager is
absent, so a build without storage runs the rest unchanged).

Schema is owned by the migration (`database/migrations/00009_create_filestore_tables.sql`) —
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

err = manager.Stage(ctx, key, r, map[string]any{
    "content_type": contentType,
    "original":     filename,
    "owner_id":     userID,
})
```

From here the watcher (or the feature itself, via `client.Add(jobs.ChunkUploadTask{Key: key})`)
sends the upload to the queue. The feature does not wait for it.

### 2. Read, Update, Delete

```go
// One stream the backend serves on demand.
rc, err := manager.Open(ctx, key)
defer func() { _ = rc.Close() }()

// The manifest a feature reads to know what the backend holds.
manifest, err := manager.Manifest(ctx, key)
progress, err := manager.Progress(ctx, key) // status, size

// Metadata is rewritten any time; status and content hash are untouched.
err = manager.UpdateMetadata(ctx, key, map[string]any{"original": "renamed.png"})

// Deletion removes the manifest row first, the object second.
err = manager.Delete(ctx, key)
```

### 3. Hooks

Both are optional and wired in the registry, where the feature that owns the behavior lives:

```go
manager.
    WithBeforeSync(func(ctx context.Context, key, path string) error {
        // The gate: runs before the fingerprint is read, so a rewrite
        // here is what gets hashed. An error refuses the file.
        return validateImage(path)
    }).
    WithAfterSync(func(ctx context.Context, manifest storage.Manifest) error {
        // The post-processing point: the object is durable, the
        // manifest is ready, the staging copy still exists.
        return client.Add(ThumbnailTask{Key: manifest.Key}).Save()
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

| Key                     | Default   | Description                                                                     |
| ----------------------- | --------- | ------------------------------------------------------------------------------- |
| `storage.driver`        | `local`   | `local` or `s3`; only the selected backend's client is built                     |
| `storage.local_path`    | `storage` | The one data directory: `files/` (the local backend), `staging/`, and the log sink live under it |
| `storage.watch.enable`  | true      | The staging watcher enqueues uploads as staging paths settle                     |
| `storage.watch.debounce`| 2         | Seconds a path must stay quiet before its upload is enqueued                     |
| `storage.s3.*`          | —         | `access_key`, `secret_key` (both secrets), `bucket_name`, `endpoint_url`, `force_path_style`, `path_prefix`, `region` |

The local path needs no flag and no variable of its own — a deployment sets it in the config
file. Postgres plus local storage are enough; no backend is required.

### Manager construction

| Parameter | Meaning                                                                      |
| --------- | ---------------------------------------------------------------------------- |
| `store`   | The backend (`New` builds the one `storage.driver` selects)                   |
| `db`      | The shared Postgres pool (`Querier` + `WithTx`)                               |
| `staging` | The staging directory (the registry joins `storage.local_path` + `staging`)   |

## API Reference

### `New(store Store, db DB, staging string, log) *Manager`

Builds the engine: the manifest store, the staging directory. Nothing touches the backend or
the database yet.

### `(*Manager).Stage(ctx, key, r io.Reader, metadata map[string]any) error`

Writes the file into staging through a temp file + rename and commits the intent row —
metadata and the staging fingerprint — before the first byte travels. Re-staging a key
replaces its metadata and makes the stored manifest stale, so the next sync uploads the new
version.

### `(*Manager).Sync(ctx, key) error`

The upload round the `ChunkUploadTask` processor runs: hook gate → fingerprint → reuse or
hash → PUT whole → ready commit → after hook → staging cleanup. Idempotent; the queue's
retries replay it. A key with no staging file returns nil — another attempt already finished.

### `(*Manager).Open(ctx, key) (io.ReadCloser, error)`

A stream the backend serves on demand. `ErrNotFound` for a key nothing stored.

### `(*Manager).Manifest(ctx, key) (Manifest, error)` / `(*Manager).Progress(ctx, key) (Progress, error)`

The stored state a feature reads: status, content hash, metadata — and the progress shape
(`Status`, `Size`) a status endpoint would serve.

### `(*Manager).UpdateMetadata(ctx, key, metadata map[string]any) error`

Replaces the metadata; works before and after the upload (a key nothing stored yet gets a
pending row).

### `(*Manager).Delete(ctx, key) error`

Removes the manifest row and then the object the backend holds; the staging copy goes with
it.

### `(*Manager).CollectGarbage(ctx) (int, error)`

Removes every backend object no manifest names; returns the count.

### `Key(parts ...string) (string, error)` / `ValidateKey(key string) error`

The naming helpers. `Key` sanitizes each part onto one path segment (letters, digits, dot,
dash, underscore stay; the rest becomes `_`); both refuse empty parts, `.`/`..`, hidden
segments, absolute paths, and separators where one part was asked for.

### `Store`

The backend contract: `Get`, `Put` (whole, with the byte size), `Delete`, `List`.
`storage.New(cfg)` builds the one the `storage.driver` section names (`FS` over
`storage.local_path`, `S3` over `storage.s3.*`); both translate their protocol's not-found
shape into `ErrNotFound` at the edge.

## Database Schema

One table, created by migration `database/migrations/00009_create_filestore_tables.sql`:

### `storage_files`

| Column          | Type          | Description                                                        |
| --------------- | ------------- | ------------------------------------------------------------------ |
| `id`            | `UUID`        | Primary key, `uuidv7()` default                                     |
| `key`           | `TEXT`        | The feature's naming, unique                                       |
| `size`          | `BIGINT`      | File size in bytes                                                 |
| `content_hash`  | `TEXT`        | SHA-256 of the whole file — the one-value "already stored" check   |
| `status`        | `TEXT`        | `pending` / `ready` / `failed` (`chk_storage_files_status`)        |
| `metadata`      | `JSONB`       | The feature's own record; the engine carries it, never reads it    |
| `staging_size`  | `BIGINT`      | Fingerprint half one: the staging file's size                      |
| `staging_mtime` | `TIMESTAMPTZ` | Fingerprint half two: the staging file's modification time         |
| `created_at` / `updated_at` | `TIMESTAMPTZ` | `updated_at` maintained by trigger                    |

Indexes on `status` and `content_hash`; the named check guards the status vocabulary.

## Error Handling

- **A refused key** — `ErrInvalidKey` before any path is built or any byte moves; traversal
  shapes never reach the filesystem
- **A failed sync** — the queue retries it; the checkpoint makes the retry cheap, and a round
  that exhausts its attempts rests in the archive with its error
- **A failed after-hook** — the data is already durable; the retry replays the hook through
  the fast path
- **A re-staged file under a running sync** — the TOCTOU guard leaves it for a fresh round
  and fails this one, so nothing is destroyed unrecorded
- **A lost staging file** — `Sync` returns nil: another attempt finished, or the caller owns
  the lifecycle
- **An orphaned object** — a crash between a manifest deletion and the object's removal
  leaves one for `storage_gc`; it is never a manifest naming missing bytes

## Testing

Tests run against real containers (testcontainers) using the shared `pkg/testutils` helpers:
`StartPostgres` per test database (migrations applied), `StartMinIO` for the S3 driver. Each
fixture uses topic vocabulary (Dan Brown, Harry Potter), so two tests — or two runs — sharing
one bucket cannot answer each other's probes.

```bash
go test ./internal/storage/
go test -race ./internal/storage/
```

## Design Decisions

| Decision | Rationale |
| -------- | --------- |
| Whole-file PUT, no chunk store | Both drivers keep the same tree of keys a final file appears in; the staging file and the manifest are the resume points |
| Manifest in Postgres, not sidecar files | Migrations are the schema truth; the manifest survives a lost disk and is queryable |
| Staging first, upload on the queue | The request path is one local write; a slow backend never sits inside a request |
| Content hash = SHA-256 of the file | One read tells whether the backend holds these bytes; an unchanged replay uploads nothing |
| Fingerprint (size + mtime) reuse | A retry skips the hashing pass — the one cost a large file cannot afford twice |
| Hooks at the round's edges, nil default | Features get validation and post-processing exactly once per finished upload, with zero cost when absent |
| Idempotence as the hooks' contract | The queue retries the round; a hook that cannot be replayed cannot be safe |
| Watcher with per-path debounce and a start-up scan | A burst of writes is one upload; an upload that outlived its process is not lost |
| Garbage collection as a recurring job | Crash leftovers are swept on a clock instead of a goroutine the process must babysit |
| Keys validated at every entry point | The staging path is built from the key; the door is the only place a traversal can be stopped |
| Schema owned by migrations | One source of schema truth; the engine never mutates the schema |
