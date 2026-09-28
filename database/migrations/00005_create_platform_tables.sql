-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.apis (resource servers)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.apis (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    resource TEXT NOT NULL UNIQUE CHECK (char_length(resource) BETWEEN 1 AND 350),
    allow_cimd_clients BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL
) USING heap;

CREATE TRIGGER trg_apis_updated_at BEFORE UPDATE ON public.apis FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

CREATE INDEX IF NOT EXISTS idx_apis_created_at ON public.apis (created_at);
CREATE INDEX IF NOT EXISTS idx_apis_name ON public.apis USING gin (name gin_trgm_ops);

-- --------------------------------------------------------
-- Table: public.api_permissions (scopes per API)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.api_permissions (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    api_id UUID NOT NULL REFERENCES public.apis(id) ON DELETE CASCADE,
    key TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    description TEXT CHECK (char_length(description) <= 200),
    allowed_for_cimd_clients BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    -- A permission key is unique within its API; the wire scope is
    -- "<resource>:<key>".
    CONSTRAINT uq_api_permissions_api_key UNIQUE (api_id, key)
) USING heap;

CREATE TRIGGER trg_api_permissions_updated_at BEFORE UPDATE ON public.api_permissions FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

CREATE INDEX IF NOT EXISTS idx_api_permissions_api_id ON public.api_permissions (api_id);

-- --------------------------------------------------------
-- Table: public.oidc_client_api_grants (client → API grants)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oidc_client_api_grants (
    api_id UUID NOT NULL REFERENCES public.apis(id) ON DELETE CASCADE,
    client_id TEXT NOT NULL REFERENCES public.oidc_clients(id) ON DELETE CASCADE,
    user_delegated_access BOOLEAN NOT NULL DEFAULT FALSE,
    client_access BOOLEAN NOT NULL DEFAULT FALSE,
    cimd_granted_access BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    PRIMARY KEY (api_id, client_id)
) USING heap;

CREATE TRIGGER trg_oidc_client_api_grants_updated_at BEFORE UPDATE ON public.oidc_client_api_grants FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

-- --------------------------------------------------------
-- Table: public.oidc_client_api_grant_permissions (granted scopes)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oidc_client_api_grant_permissions (
    api_id UUID NOT NULL,
    client_id TEXT NOT NULL,
    permission_id UUID NOT NULL REFERENCES public.api_permissions(id) ON DELETE CASCADE,
    subject TEXT NOT NULL CHECK (subject IN ('client', 'user_delegated', 'cimd')),
    PRIMARY KEY (api_id, client_id, permission_id, subject),
    FOREIGN KEY (api_id, client_id) REFERENCES public.oidc_client_api_grants(api_id, client_id) ON DELETE CASCADE
) USING heap;

CREATE INDEX IF NOT EXISTS idx_grant_permissions_client ON public.oidc_client_api_grant_permissions (client_id);

-- --------------------------------------------------------
-- Table: public.settings — database-backed application settings
-- for product flows, as distinct from the system configuration
-- (the JSON file, served read-only by AppConfigService). One row
-- per namespaced key. Whether a value rests sealed is told by its
-- enc: prefix alone, and a public row never rests sealed — the
-- unauthenticated read can never carry a ciphertext.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.settings (
    key TEXT NOT NULL PRIMARY KEY CHECK (char_length(key) BETWEEN 1 AND 350),
    value TEXT NOT NULL,
    public BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    CONSTRAINT chk_settings_public_not_sealed CHECK (NOT (public AND value LIKE 'enc:%'))
) USING heap;

CREATE TRIGGER trg_settings_updated_at
    BEFORE UPDATE ON public.settings
    FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

-- --------------------------------------------------------
-- Table: public.api_keys
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
CREATE INDEX IF NOT EXISTS idx_api_keys_revoked_at ON public.api_keys (revoked_at) WHERE revoked_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_last_used_at ON public.api_keys (last_used_at) WHERE last_used_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_created_at ON public.api_keys (created_at);
CREATE INDEX IF NOT EXISTS idx_api_keys_updated_at ON public.api_keys (updated_at) WHERE updated_at IS NOT NULL;

-- Unique constraint: name must be unique per user (including NULL user_id for global keys)
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_name_user_id ON public.api_keys USING btree (name, COALESCE(user_id, '00000000-0000-0000-0000-000000000000'::UUID));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_api_keys_updated_at ON public.api_keys;
DROP TRIGGER IF EXISTS trg_settings_updated_at ON public.settings;
DROP TRIGGER IF EXISTS trg_oidc_client_api_grants_updated_at ON public.oidc_client_api_grants;
DROP TRIGGER IF EXISTS trg_api_permissions_updated_at ON public.api_permissions;
DROP TRIGGER IF EXISTS trg_apis_updated_at ON public.apis;

DROP INDEX IF EXISTS idx_api_keys_name_user_id;
DROP INDEX IF EXISTS idx_api_keys_updated_at;
DROP INDEX IF EXISTS idx_api_keys_created_at;
DROP INDEX IF EXISTS idx_api_keys_last_used_at;
DROP INDEX IF EXISTS idx_api_keys_revoked_at;
DROP INDEX IF EXISTS idx_api_keys_user_id_expires_at;
DROP INDEX IF EXISTS idx_api_keys_expires_at;
DROP INDEX IF EXISTS idx_api_keys_user_id;
DROP INDEX IF EXISTS idx_grant_permissions_client;
DROP INDEX IF EXISTS idx_api_permissions_api_id;
DROP INDEX IF EXISTS idx_apis_name;
DROP INDEX IF EXISTS idx_apis_created_at;

DROP TABLE IF EXISTS public.api_keys;
DROP TABLE IF EXISTS public.settings;
DROP TABLE IF EXISTS public.oidc_client_api_grant_permissions;
DROP TABLE IF EXISTS public.oidc_client_api_grants;
DROP TABLE IF EXISTS public.api_permissions;
DROP TABLE IF EXISTS public.apis;

-- +goose StatementEnd
