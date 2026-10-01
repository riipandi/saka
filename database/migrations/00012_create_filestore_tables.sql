-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.storage_files (one row per stored file; the manifest
-- the re-upload decision and the garbage collection read)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.storage_files (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    key TEXT NOT NULL,
    size BIGINT NOT NULL DEFAULT 0,
    -- SHA-256 of the whole file: one read tells whether the bytes on the
    -- staging side still match what the backend holds.
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
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
    UNIQUE (key),
    CONSTRAINT chk_storage_files_status CHECK (status IN ('pending', 'ready', 'failed')),
    CONSTRAINT chk_storage_files_size CHECK (size >= 0)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_storage_files_status ON public.storage_files (status);
CREATE INDEX IF NOT EXISTS idx_storage_files_content_hash ON public.storage_files (content_hash);

CREATE OR REPLACE FUNCTION fn_update_storage_files_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = clock_timestamp(); RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_storage_files_updated_at
    BEFORE UPDATE ON public.storage_files
    FOR EACH ROW EXECUTE FUNCTION fn_update_storage_files_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_storage_files_updated_at ON public.storage_files;
DROP FUNCTION IF EXISTS fn_update_storage_files_updated_at();

-- The chunk table is the earlier engine's residue: a database the old up
-- created still carries it, and its foreign key blocks this drop.
DROP TABLE IF EXISTS public.storage_chunks;
DROP TABLE IF EXISTS public.storage_files;

-- +goose StatementEnd
