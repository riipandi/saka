-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.storage_buckets (the logical namespaces over the one
-- configured backend: a bucket row names the first path segment every
-- object key rides under)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.storage_buckets (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL,
    -- Per-bucket ceiling for the bytes of one upload. NULL means unlimited.
    file_size_limit BIGINT,
    -- Per-bucket accept list for the declared content type. NULL means any.
    allowed_mime_types TEXT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (name),
    CONSTRAINT chk_storage_buckets_file_size_limit CHECK (file_size_limit IS NULL OR file_size_limit >= 0)
) USING heap;

-- --------------------------------------------------------
-- Table: public.storage_objects (one row per stored file; the manifest
-- the re-upload decision and the garbage collection read)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.storage_objects (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    bucket_id UUID NOT NULL REFERENCES public.storage_buckets (id) ON DELETE RESTRICT,
    key TEXT NOT NULL,
    size BIGINT NOT NULL DEFAULT 0,
    -- SHA-256 of the whole file: one read tells whether the bytes on the
    -- staging side still match what the backend holds.
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    -- A private object is only served over a signed link: a plain read of
    -- /storage answers 404, the same shape a missing object answers, so the
    -- manifest itself never has to leak which keys exist.
    is_private BOOLEAN NOT NULL DEFAULT false,
    -- Free-form metadata the storing feature owns: content type, original
    -- file name, owner id. The engine never reads it, only carries it.
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- The staging fingerprint the content hash was computed from:
    -- size plus modification time. A retry that finds both unchanged
    -- reuses the stored hash instead of reading the file again.
    staging_size BIGINT NOT NULL DEFAULT 0,
    staging_mtime TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (bucket_id, key),
    CONSTRAINT chk_storage_objects_status CHECK (status IN ('pending', 'ready', 'failed')),
    CONSTRAINT chk_storage_objects_size CHECK (size >= 0)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_storage_objects_bucket_id ON public.storage_objects (bucket_id);
CREATE INDEX IF NOT EXISTS idx_storage_objects_status ON public.storage_objects (status);
CREATE INDEX IF NOT EXISTS idx_storage_objects_content_hash ON public.storage_objects (content_hash);

CREATE OR REPLACE FUNCTION fn_update_storage_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = clock_timestamp(); RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_storage_buckets_updated_at
    BEFORE UPDATE ON public.storage_buckets
    FOR EACH ROW EXECUTE FUNCTION fn_update_storage_updated_at();

CREATE TRIGGER trg_storage_objects_updated_at
    BEFORE UPDATE ON public.storage_objects
    FOR EACH ROW EXECUTE FUNCTION fn_update_storage_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_storage_objects_updated_at ON public.storage_objects;
DROP TRIGGER IF EXISTS trg_storage_buckets_updated_at ON public.storage_buckets;
DROP FUNCTION IF EXISTS fn_update_storage_updated_at();

DROP TABLE IF EXISTS public.storage_objects;
DROP TABLE IF EXISTS public.storage_buckets;

-- The chunk table is the earlier engine's residue: a database the old up
-- created still carries it, and its foreign key blocks the drop above.
DROP TABLE IF EXISTS public.storage_chunks;
DROP TABLE IF EXISTS public.storage_files;

-- +goose StatementEnd
