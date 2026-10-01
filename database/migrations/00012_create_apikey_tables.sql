-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.api_keys — the account's machine credentials, a
-- tango-only surface. The key travels once, in the create response;
-- only the hash rests here.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.api_keys (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID REFERENCES public.users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) > 0),
    prefix VARCHAR(10) NOT NULL CHECK (char_length(prefix) > 0),
    key_hash BYTEA NOT NULL,
    description TEXT,
    expiration_email_sent_at TIMESTAMPTZ DEFAULT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    last_used_at TIMESTAMPTZ DEFAULT NULL,
    revoked_at TIMESTAMPTZ DEFAULT NULL,
    CONSTRAINT chk_name_format CHECK (name ~ '^[a-zA-Z0-9_ -]{3,64}$')
) USING heap;

CREATE TRIGGER trg_api_keys_updated_at BEFORE UPDATE ON public.api_keys FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON public.api_keys USING btree (user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_expires_at ON public.api_keys (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id_expires_at ON public.api_keys USING btree (user_id, expires_at);
CREATE INDEX IF NOT EXISTS idx_api_keys_last_used_at ON public.api_keys (last_used_at) WHERE last_used_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_created_at ON public.api_keys (created_at);

-- Unique constraint: name must be unique per user (including NULL user_id for global keys)
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_name_user_id ON public.api_keys USING btree (name, COALESCE(user_id, '00000000-0000-0000-0000-000000000000'::UUID));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_api_keys_name_user_id;
DROP INDEX IF EXISTS idx_api_keys_created_at;
DROP INDEX IF EXISTS idx_api_keys_last_used_at;
DROP INDEX IF EXISTS idx_api_keys_user_id_expires_at;
DROP INDEX IF EXISTS idx_api_keys_expires_at;
DROP INDEX IF EXISTS idx_api_keys_user_id;

DROP TRIGGER IF EXISTS trg_api_keys_updated_at ON public.api_keys;
DROP TABLE IF EXISTS public.api_keys;

-- +goose StatementEnd
