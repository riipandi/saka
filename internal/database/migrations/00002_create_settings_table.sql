-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.app_settings — database-backed overrides for the
-- application settings the product flows read, as distinct from
-- the system configuration (the JSON file, served read-only by
-- the configuration endpoint). The catalog in code declares every
-- item — key, default, and its flags — so this table holds the
-- overrides alone: a key that rests here replaces the default,
-- an absent key answers it. Whether a value rests sealed is told
-- by its enc: prefix alone. updated_at is set on insert and
-- maintained by the trigger on every update after.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.app_settings (
    key TEXT NOT NULL PRIMARY KEY CHECK (char_length(key) BETWEEN 1 AND 350),
    value TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
) USING heap;

CREATE TRIGGER trg_app_settings_updated_at
    BEFORE UPDATE ON public.app_settings
    FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_app_settings_updated_at ON public.app_settings;
DROP TABLE IF EXISTS public.app_settings;

-- +goose StatementEnd
