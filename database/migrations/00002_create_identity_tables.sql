-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.users
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.users (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    username CITEXT NOT NULL UNIQUE,
    email TEXT NOT NULL UNIQUE CHECK (email ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'),
    first_name VARCHAR(100),
    last_name VARCHAR(100),
    display_name TEXT NOT NULL CHECK (char_length(display_name) > 0),
    avatar_url TEXT, -- Storage key of the uploaded avatar; NULL = bundled default picture
    locale TEXT,
    is_admin BOOLEAN NOT NULL DEFAULT FALSE,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    metadata JSONB DEFAULT NULL, -- Metadata can contain user-specific information
    email_verified_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    last_login_at TIMESTAMPTZ DEFAULT NULL,
    banned_at TIMESTAMPTZ DEFAULT NULL,
    ban_expires TIMESTAMPTZ DEFAULT NULL,
    ban_reason TEXT DEFAULT NULL,
    -- Username only allows alphanumeric characters and underscores, must be between 3 and 32 characters long
    CONSTRAINT chk_username_format CHECK (username IS NULL OR username ~ '^[a-zA-Z0-9_]{3,32}$')
) USING heap;

CREATE TRIGGER trg_users_updated_at BEFORE UPDATE ON public.users FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();
CREATE TRIGGER trg_users_deleted_record AFTER DELETE ON public.users FOR EACH ROW EXECUTE FUNCTION fn_soft_delete();

CREATE INDEX IF NOT EXISTS idx_users_display_name ON public.users USING gin (display_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_username ON public.users USING GIN (username gin_trgm_ops) WHERE username IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_banned_at ON public.users (banned_at) WHERE banned_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_ban_expires ON public.users (ban_expires) WHERE ban_expires IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_banned_expires ON public.users (banned_at, ban_expires) WHERE banned_at IS NOT NULL AND ban_expires IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_created_at ON public.users (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_users_updated_at ON public.users (updated_at) WHERE updated_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_metadata_gin ON public.users USING GIN (metadata);
CREATE INDEX IF NOT EXISTS idx_users_last_login_at ON public.users (last_login_at) WHERE last_login_at IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_normalized_username ON public.users (LOWER(username));

-- --------------------------------------------------------
-- Table: public.user_passwords (junction table)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.user_passwords (
    user_id UUID NOT NULL PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    CONSTRAINT user_passwords_one_per_user UNIQUE (user_id) -- Ensure only one password per user
) USING heap;

CREATE TRIGGER trg_user_passwords_updated_at BEFORE UPDATE ON public.user_passwords FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

CREATE INDEX IF NOT EXISTS idx_user_passwords_user_id ON public.user_passwords (user_id);
CREATE INDEX IF NOT EXISTS idx_user_passwords_created_at ON public.user_passwords (created_at);
CREATE INDEX IF NOT EXISTS idx_user_passwords_updated_at ON public.user_passwords (updated_at) WHERE updated_at IS NOT NULL;

-- --------------------------------------------------------
-- Table: public.user_groups
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.user_groups (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL CHECK (char_length(display_name) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL
) USING heap;

CREATE TRIGGER trg_user_groups_updated_at BEFORE UPDATE ON public.user_groups FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

-- --------------------------------------------------------
-- Table: public.user_groups_users (junction table)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.user_groups_users (
    user_id UUID NOT NULL,
    user_group_id UUID NOT NULL,
    PRIMARY KEY (user_id, user_group_id),
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE
) USING heap;

CREATE INDEX IF NOT EXISTS idx_user_groups_users_user_id ON public.user_groups_users USING btree (user_id);
CREATE INDEX IF NOT EXISTS idx_user_groups_users_user_group_id ON public.user_groups_users USING btree (user_group_id);

-- --------------------------------------------------------
-- Table: public.sessions
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.sessions (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    user_agent TEXT,
    device_name TEXT,
    device_fingerprint TEXT,
    ip_address INET,
    remember BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP),
    refreshed_at TIMESTAMPTZ DEFAULT NULL,
    revoked_at TIMESTAMPTZ DEFAULT NULL,
    revoked_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    impersonated_by UUID REFERENCES public.users(id) ON DELETE SET NULL
) USING heap;

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON public.sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON public.sessions (expires_at) WHERE expires_at IS NOT NULL;

-- The table remembering every browser fingerprint an account has signed in
-- from. A session row is a poor record of a device — it expires and gets
-- cleaned up — so the first-seen judgement behind the new-device notice needs
-- a row that outlives the sessions.
CREATE TABLE IF NOT EXISTS public.known_devices (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    device_fingerprint TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id, device_fingerprint)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_sessions_user_id_expires_at ON public.sessions USING btree (user_id, expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_ip_address ON public.sessions (ip_address) WHERE ip_address IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_device_fingerprint ON public.sessions (device_fingerprint);
CREATE INDEX IF NOT EXISTS idx_sessions_impersonated_by ON public.sessions (impersonated_by) WHERE impersonated_by IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_token_hash ON public.sessions (token_hash);

-- --------------------------------------------------------
-- Table: public.auth_tokens — one-time access, email verification,
-- reauthentication, password-reset, and email-change tokens (hash-only).
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.auth_tokens (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    device_token VARCHAR(16), -- Used only for one_time_access
    payload TEXT,             -- Used only for email_change: the pending address the token is bound to
    purpose TEXT NOT NULL DEFAULT 'one_time_access',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP),
    last_sent_at TIMESTAMPTZ DEFAULT NULL,
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT chk_auth_token_purpose CHECK (purpose IN ('email_verification', 'one_time_access', 'reauthentication', 'password_reset', 'email_change'))
) USING heap;

CREATE INDEX IF NOT EXISTS idx_auth_tokens_user_id ON public.auth_tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_purpose ON public.auth_tokens (purpose);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_expires_at ON public.auth_tokens USING btree (expires_at);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_token_hash ON public.auth_tokens USING HASH (token_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_auth_tokens_user_id_purpose ON public.auth_tokens USING btree (user_id, purpose);

-- --------------------------------------------------------
-- Table: public.signup_tokens
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.signup_tokens (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    token_hash TEXT NOT NULL UNIQUE,
    usage_limit INTEGER NOT NULL DEFAULT 1,
    usage_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL
) USING heap;

CREATE INDEX IF NOT EXISTS idx_signup_tokens_expires_at ON public.signup_tokens USING btree (expires_at);

-- --------------------------------------------------------
-- Table: public.signup_tokens_user_groups (junction table)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.signup_tokens_user_groups (
    signup_token_id UUID NOT NULL,
    user_group_id UUID NOT NULL,
    PRIMARY KEY (signup_token_id, user_group_id),
    FOREIGN KEY (signup_token_id) REFERENCES public.signup_tokens(id) ON DELETE CASCADE,
    FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE
) USING heap;

-- --------------------------------------------------------
-- Table: public.device_login_requests — QR / cross-device sign-in
-- state (a plain table is the portable equivalent of the upstream
-- actor framework)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.device_login_requests (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    code TEXT NOT NULL UNIQUE,             -- short user code (P + 7 chars)
    device_token_hash TEXT NOT NULL,       -- binds the polling device
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'consumed')),
    user_id UUID REFERENCES public.users (id) ON DELETE CASCADE,
    ip_address TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL
) USING heap;

CREATE INDEX IF NOT EXISTS idx_device_login_requests_expires_at ON public.device_login_requests (expires_at);

-- --------------------------------------------------------
-- Table: public.audit_logs — track user actions and system events
-- --------------------------------------------------------

DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'audit_action_status') THEN
    CREATE TYPE public.audit_action_status AS ENUM ('success', 'failed', 'pending', 'unknown');
END IF; END$$;

DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'audit_event_trigger') THEN
    CREATE TYPE public.audit_event_trigger AS ENUM ('user', 'system', 'external');
END IF; END$$;

CREATE TABLE IF NOT EXISTS public.audit_logs (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    event TEXT NOT NULL,
    trigger_type public.audit_event_trigger NOT NULL,
    action_status public.audit_action_status DEFAULT 'pending',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip_address INET,
    user_agent TEXT,
    device_fingerprint TEXT,
    country TEXT,
    city TEXT,
    resource_type TEXT,
    resource_id UUID,
    user_id UUID REFERENCES public.users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
) USING heap;

CREATE INDEX IF NOT EXISTS idx_audit_logs_client_name ON public.audit_logs USING btree (((payload ->> 'client_name'::text)));
CREATE INDEX IF NOT EXISTS idx_audit_logs_country ON public.audit_logs USING btree (country);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON public.audit_logs USING btree (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_event ON public.audit_logs USING btree (event);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_agent ON public.audit_logs USING btree (user_agent);
CREATE INDEX IF NOT EXISTS idx_audit_logs_device_fingerprint ON public.audit_logs (device_fingerprint) WHERE device_fingerprint IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON public.audit_logs USING btree (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_users_deleted_record ON public.users;
DROP TRIGGER IF EXISTS trg_users_updated_at ON public.users;
DROP TRIGGER IF EXISTS trg_user_passwords_updated_at ON public.user_passwords;
DROP TRIGGER IF EXISTS trg_user_groups_updated_at ON public.user_groups;

DROP INDEX IF EXISTS idx_audit_logs_user_id;
DROP INDEX IF EXISTS idx_audit_logs_user_agent;
DROP INDEX IF EXISTS idx_audit_logs_device_fingerprint;
DROP INDEX IF EXISTS idx_audit_logs_event;
DROP INDEX IF EXISTS idx_audit_logs_country;
DROP INDEX IF EXISTS idx_audit_logs_created_at;
DROP INDEX IF EXISTS idx_audit_logs_client_name;
DROP INDEX IF EXISTS idx_device_login_requests_expires_at;
DROP INDEX IF EXISTS idx_signup_tokens_expires_at;
DROP INDEX IF EXISTS idx_auth_tokens_user_id_purpose;
DROP INDEX IF EXISTS idx_auth_tokens_token_hash;
DROP INDEX IF EXISTS idx_auth_tokens_expires_at;
DROP INDEX IF EXISTS idx_auth_tokens_purpose;
DROP INDEX IF EXISTS idx_auth_tokens_user_id;
DROP INDEX IF EXISTS idx_sessions_token_hash;
DROP INDEX IF EXISTS idx_sessions_impersonated_by;
DROP INDEX IF EXISTS idx_sessions_device_fingerprint;
DROP INDEX IF EXISTS idx_sessions_ip_address;
DROP INDEX IF EXISTS idx_sessions_user_id_expires_at;
DROP INDEX IF EXISTS idx_sessions_expires_at;
DROP INDEX IF EXISTS idx_sessions_user_id;
DROP INDEX IF EXISTS idx_user_groups_users_user_group_id;
DROP INDEX IF EXISTS idx_user_groups_users_user_id;
DROP INDEX IF EXISTS idx_users_normalized_username;
DROP INDEX IF EXISTS idx_users_last_login_at;
DROP INDEX IF EXISTS idx_users_metadata_gin;
DROP INDEX IF EXISTS idx_users_updated_at;
DROP INDEX IF EXISTS idx_users_created_at;
DROP INDEX IF EXISTS idx_users_banned_expires;
DROP INDEX IF EXISTS idx_users_ban_expires;
DROP INDEX IF EXISTS idx_users_banned_at;
DROP INDEX IF EXISTS idx_users_username;
DROP INDEX IF EXISTS idx_users_display_name;
DROP INDEX IF EXISTS idx_user_passwords_updated_at;
DROP INDEX IF EXISTS idx_user_passwords_created_at;
DROP INDEX IF EXISTS idx_user_passwords_user_id;

DROP TABLE IF EXISTS public.audit_logs;
DROP TABLE IF EXISTS public.device_login_requests;
DROP TABLE IF EXISTS public.known_devices;
DROP TABLE IF EXISTS public.signup_tokens_user_groups;
DROP TABLE IF EXISTS public.signup_tokens;
DROP TABLE IF EXISTS public.auth_tokens;
DROP TABLE IF EXISTS public.sessions;
DROP TABLE IF EXISTS public.user_groups_users;
DROP TABLE IF EXISTS public.user_groups;
DROP TABLE IF EXISTS public.user_passwords;
DROP TABLE IF EXISTS public.users;

DROP TYPE IF EXISTS public.audit_action_status;
DROP TYPE IF EXISTS public.audit_event_trigger;

-- +goose StatementEnd
