-- +goose Up
-- +goose StatementBegin
SET timezone = 'UTC';

-- ============================================================================
-- PostgreSQL extensions
-- ============================================================================
CREATE EXTENSION IF NOT EXISTS citext;    -- Case-insensitive text type (slower than varchar or text columns)
CREATE EXTENSION IF NOT EXISTS hstore;    -- Key-Value pairs for storing unstructured data
CREATE EXTENSION IF NOT EXISTS pg_trgm;   -- Text similarity measurement and index searching based on trigrams
CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- Cryptographic functions for hashing and encryption
CREATE EXTENSION IF NOT EXISTS plpgsql;   -- PL/pgSQL procedural language

-- ============================================================================
-- Additional schemas for application use. Avoid circular dependency
-- between schemas! Schema owner `pg_database_owner` is the owner of
-- the database, typically the user who created it.
-- ============================================================================
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'internal') THEN
        EXECUTE 'CREATE SCHEMA internal AUTHORIZATION pg_database_owner';
        EXECUTE 'GRANT USAGE, CREATE ON SCHEMA internal TO pg_database_owner;';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'reference') THEN
        EXECUTE 'CREATE SCHEMA reference AUTHORIZATION pg_database_owner';
        EXECUTE 'GRANT USAGE, CREATE ON SCHEMA reference TO pg_database_owner;';
    END IF;
END$$;

-- ============================================================================
-- Auto-update function: fills the updated_at column automatically.
-- CURRENT_TIMESTAMP is equivalent to timezone('utc'::text, now())::timestamptz.
-- ============================================================================
CREATE OR REPLACE FUNCTION fn_updated_at_value()
RETURNS TRIGGER AS $$ BEGIN NEW.updated_at = CURRENT_TIMESTAMP; RETURN NEW; END; $$
LANGUAGE plpgsql;

-- ============================================================================
-- Returns the size of each database in bytes, KB, MB, and GB.
-- Example: SELECT * FROM get_database_sizes();
-- ============================================================================
CREATE OR REPLACE FUNCTION get_database_sizes()
RETURNS TABLE (db_name TEXT, size_in_bytes TEXT, size_in_kb TEXT, size_in_mb TEXT, size_in_gb TEXT)
AS $$
BEGIN
    RETURN QUERY
    WITH db_sizes AS (
        SELECT datname::TEXT AS db_name, pg_database_size(datname) AS raw_size
        FROM pg_database WHERE datname NOT IN ('template0', 'template1')
    ),
    all_databases AS (SELECT 'all_database'::TEXT AS db_name, sum(raw_size) AS raw_size FROM db_sizes)
    SELECT
        cd.db_name,
        CASE
            WHEN cd.raw_size % 1 = 0 THEN cd.raw_size::text
            ELSE ROUND(cd.raw_size::numeric, 4)::text
        END AS size_in_bytes,
        CASE
            WHEN cd.raw_size % 1024 = 0 THEN (cd.raw_size / 1024)::text
            ELSE ROUND((cd.raw_size / 1024)::numeric, 4)::text
        END AS size_in_kb,
        CASE
            WHEN cd.raw_size % (1024 * 1024) = 0 THEN (cd.raw_size / (1024 * 1024))::text
            ELSE ROUND((cd.raw_size / (1024 * 1024))::numeric, 2)::text
        END AS size_in_mb,
        CASE
            WHEN cd.raw_size % (1024 * 1024 * 1024) = 0 THEN (cd.raw_size / (1024 * 1024 * 1024))::text
            ELSE ROUND((cd.raw_size / (1024 * 1024 * 1024))::numeric, 4)::text
        END AS size_in_gb
    FROM (SELECT * FROM db_sizes UNION ALL SELECT * FROM all_databases) AS cd
    ORDER BY cd.db_name ASC;
END;
$$ LANGUAGE plpgsql STABLE;

-- ============================================================================
-- Returns size information of user-defined tables including bytes, KB, MB, GB.
-- Example: SELECT * FROM get_table_sizes();
-- ============================================================================
CREATE OR REPLACE FUNCTION get_table_sizes()
RETURNS TABLE ( schema_name TEXT, table_name TEXT, size_in_bytes TEXT, size_in_kb TEXT, size_in_mb TEXT, size_in_gb TEXT)
AS $$
BEGIN
    RETURN QUERY
    WITH table_sizes AS (
        SELECT
            nsp.nspname::TEXT AS schema_name,
            cls.relname::TEXT AS table_name,
            pg_total_relation_size(cls.oid) AS raw_size
        FROM pg_class cls JOIN pg_namespace nsp ON nsp.oid = cls.relnamespace
        WHERE cls.relkind = 'r'
          AND nsp.nspname NOT IN ('pg_catalog', 'information_schema')
          AND cls.relname <> 'app_migration'
    )
    SELECT ts.schema_name, ts.table_name,
        CASE -- bytes
            WHEN ts.raw_size % 1 = 0 THEN ts.raw_size::TEXT
            ELSE ROUND(ts.raw_size::NUMERIC, 4)::TEXT
        END AS size_in_bytes,
        CASE -- kilobytes
            WHEN ts.raw_size % 1024 = 0 THEN (ts.raw_size / 1024)::TEXT
            ELSE ROUND((ts.raw_size / 1024)::NUMERIC, 4)::TEXT
        END AS size_in_kb,
        CASE -- megabytes
            WHEN ts.raw_size % (1024 * 1024) = 0 THEN (ts.raw_size / (1024 * 1024))::TEXT
            ELSE ROUND((ts.raw_size / (1024 * 1024))::NUMERIC, 2)::TEXT
        END AS size_in_mb,
        CASE -- gigabytes
            WHEN ts.raw_size % (1024 * 1024 * 1024) = 0 THEN (ts.raw_size / (1024 * 1024 * 1024))::TEXT
            ELSE ROUND((ts.raw_size / (1024 * 1024 * 1024))::NUMERIC, 4)::TEXT
        END AS size_in_gb
    FROM table_sizes ts
    ORDER BY ts.table_name ASC;
END;
$$ LANGUAGE plpgsql STABLE;

-- ============================================================================
--- Soft Delete Implementation Using "Deleted Record Insert" Pattern.
---
--- This migration implements an alternative to traditional `deleted_at` soft
--- deletion based on: https://brandur.org/fragments/deleted-record-insert
---
--- Why this pattern:
---  - No need to include `deleted_at IS NULL` in every live query
---  - No foreign key problems that plague traditional soft deletion
---  - Doesn't interfere with mainline code
---  - Works automatically in the background via triggers
---  - Audit-only, no expectation of undeletion
---
--- Benefits:
---  - Saves from bugs caused by accidentally omitting `deleted_at IS NULL`
---  - Countless hours of debugging time saved
---  - No performance impact on main queries
---
--- When a record is deleted from any table with a trigger, the deleted data
--- is automatically captured as JSONB in the `deleted_records` table.
---
--- @see https://brandur.org/fragments/deleted-record-insert
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.deleted_records (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    object_id TEXT NOT NULL, -- ID for the object (PK); text because a client-id is named, not generated
    source_table VARCHAR(200) NOT NULL,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL
) USING heap;

CREATE TRIGGER trg_deleted_records_updated_at BEFORE UPDATE ON public.deleted_records FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

CREATE INDEX IF NOT EXISTS idx_deleted_records_object_id ON public.deleted_records (object_id);
CREATE INDEX IF NOT EXISTS idx_deleted_records_source_table ON public.deleted_records (source_table);
CREATE INDEX IF NOT EXISTS idx_deleted_records_updated_at ON public.deleted_records (updated_at) WHERE updated_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_deleted_records_deleted_at ON public.deleted_records (deleted_at) WHERE deleted_at IS NOT NULL;

CREATE OR REPLACE FUNCTION fn_soft_delete()
RETURNS TRIGGER AS $$
    BEGIN
        EXECUTE 'INSERT INTO public.deleted_records (object_id, source_table, data) VALUES ($1, $2, $3)'
        USING OLD.id, TG_TABLE_NAME, to_jsonb(OLD.*);
        RETURN OLD;
    END;
$$
LANGUAGE plpgsql;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS public.deleted_records;
DROP FUNCTION IF EXISTS fn_soft_delete();
DROP FUNCTION IF EXISTS get_table_sizes();
DROP FUNCTION IF EXISTS get_database_sizes();
DROP FUNCTION IF EXISTS fn_updated_at_value();

REVOKE USAGE, CREATE ON SCHEMA internal FROM pg_database_owner;
REVOKE USAGE, CREATE ON SCHEMA reference FROM pg_database_owner;

DROP SCHEMA IF EXISTS internal CASCADE;
DROP SCHEMA IF EXISTS reference CASCADE;

-- +goose StatementEnd
