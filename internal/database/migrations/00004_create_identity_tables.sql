-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.users
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.users (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    username CITEXT UNIQUE,
    email TEXT NOT NULL UNIQUE CHECK (email ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'),
    first_name VARCHAR(100),
    last_name VARCHAR(100),
    display_name TEXT NOT NULL CHECK (char_length(display_name) > 0),
    picture_file_id UUID REFERENCES public.storage_objects (id) ON DELETE SET NULL,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    -- The account's preference document (locale, timezone, and whatever a
    -- later preference adds); NULL means every preference is absent, and an
    -- absent key answers its default. The readers and writers live in the
    -- user module's metadata helpers — no query filters on the document.
    metadata JSONB DEFAULT NULL,
    -- The attributes an identity source wrote onto the account: each entry
    -- a key and the value the source answered, stored as the JSON it
    -- arrived in (a scalar or an array of scalars; blanks are dropped and
    -- structured values refuse the write). Read and written whole through
    -- the user module's seam, never queried. NULL is the account no source
    -- has written.
    custom_attributes JSONB DEFAULT NULL,
    email_verified_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    last_login_at TIMESTAMPTZ DEFAULT NULL,
    -- The failed-password-verification streak behind the lockout policy. A
    -- success zeroes it, and so does the lift or expiry of the lockout row
    -- in account_restrictions — the streak never outlives a sign-in run, so
    -- it stays a counter on the account row while the durable restriction
    -- (ban or lockout) lives in its own table.
    failed_attempts INT NOT NULL DEFAULT 0,
    -- The self-delete override is the per-account answer the global setting
    -- defers to: NULL follows `users.self_delete_enabled`, TRUE admits the
    -- account even when the global gate is off, FALSE refuses it even when
    -- the global gate is on.
    self_delete_override BOOLEAN DEFAULT NULL,
    -- Username only allows alphanumeric characters and underscores, must be between 3 and 32 characters long
    CONSTRAINT chk_username_format CHECK (username IS NULL OR username ~ '^[a-zA-Z0-9_]{3,32}$')
) USING heap;

CREATE TRIGGER trg_users_updated_at BEFORE UPDATE ON public.users FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();
CREATE TRIGGER trg_users_deleted_record AFTER DELETE ON public.users FOR EACH ROW EXECUTE FUNCTION fn_soft_delete();

CREATE INDEX IF NOT EXISTS idx_users_display_name ON public.users USING gin (display_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_username ON public.users USING GIN (username gin_trgm_ops) WHERE username IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_created_at ON public.users (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_users_last_login_at ON public.users (last_login_at) WHERE last_login_at IS NOT NULL;

-- --------------------------------------------------------
-- Table: public.account_restrictions — ban and lockout as one concept:
-- a restriction row with an expiry window. The active predicate every
-- check shares is `lifted_at IS NULL AND (expires_at IS NULL OR
-- expires_at > now())`. A NULL expires_at is indefinite (the ban shape);
-- an expired lockout is lifted by the next read that finds it.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.account_restrictions (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CONSTRAINT chk_account_restrictions_kind CHECK (kind IN ('ban', 'lockout')),
    reason TEXT DEFAULT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ DEFAULT NULL,
    lifted_at TIMESTAMPTZ DEFAULT NULL,
    lifted_by UUID REFERENCES public.users(id) ON DELETE SET NULL
) USING heap;

CREATE INDEX IF NOT EXISTS idx_account_restrictions_user_active
    ON public.account_restrictions USING btree (user_id) WHERE lifted_at IS NULL;


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
    provider TEXT NOT NULL CONSTRAINT chk_sessions_provider CHECK (provider IN ('credential', 'one_time_access', 'totp', 'webauthn', 'impersonation', 'oauth_sso')),
    token_hash TEXT NOT NULL UNIQUE,
    user_agent TEXT,
    device_fingerprint TEXT,
    ip_address INET,
    remember BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP),
    refreshed_at TIMESTAMPTZ DEFAULT NULL,
    revoked_at TIMESTAMPTZ DEFAULT NULL,
    revoked_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    impersonated_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    rotated_token_hash TEXT DEFAULT NULL
) USING heap;

-- (user_id needs no single-column index: idx_sessions_user_id_expires_at leads with it)
CREATE INDEX IF NOT EXISTS idx_sessions_rotated_token_hash ON public.sessions (rotated_token_hash) WHERE rotated_token_hash IS NOT NULL;

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
-- (ip_address needs no index: no query filters sessions by it)

-- --------------------------------------------------------
-- Table: public.auth_tokens — one-time access, email verification,
-- reauthentication, reauthentication-code, password-reset, and
-- email-change tokens (hash-only).
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.auth_tokens (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    device_token VARCHAR(16), -- Used only for one_time_access
    payload TEXT,             -- Used only for email_change: the pending address the token is bound to
    purpose TEXT NOT NULL DEFAULT 'one_time_access',
    -- The wrong-guess budget a one-time-access token spends. The count rides
    -- the row so every replica judges the same token the same way.
    wrong_attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP),
    last_sent_at TIMESTAMPTZ DEFAULT NULL,
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT chk_auth_token_purpose CHECK (purpose IN ('email_verification', 'one_time_access', 'reauthentication', 'reauthentication_code', 'password_reset', 'email_change')),
    -- A device pair travels only beside a one-time-access code, and a
    -- pending address only beside an email-change token. One-time-access
    -- may carry no device at all: the email path binds the code to nothing.
    CONSTRAINT chk_auth_tokens_purpose_columns CHECK (
        (device_token IS NULL OR purpose = 'one_time_access')
        AND (payload IS NULL OR purpose = 'email_change')
    )
) USING heap;

-- (purpose needs no index: every lookup pairs it with token_hash — UNIQUE —
-- or with user_id through idx_auth_tokens_user_id_purpose)
CREATE INDEX IF NOT EXISTS idx_auth_tokens_user_id ON public.auth_tokens (user_id);
-- The token_hash's UNIQUE constraint carries the lookup index; the sweep
-- reads the expiry.
CREATE INDEX IF NOT EXISTS idx_auth_tokens_expires_at ON public.auth_tokens USING btree (expires_at);
-- One live token per account and purpose — resend replaces. Reauthentication
-- is excluded: several live step-up tokens per account are legitimate, the
-- same rule mfa_pending follows, and consumption is single-use anyway.
CREATE UNIQUE INDEX IF NOT EXISTS idx_auth_tokens_user_id_purpose ON public.auth_tokens USING btree (user_id, purpose) WHERE purpose <> 'reauthentication';

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

CREATE INDEX IF NOT EXISTS idx_signup_tokens_user_groups_user_group_id
    ON public.signup_tokens_user_groups USING btree (user_group_id);

-- --------------------------------------------------------
-- Table: public.device_login_requests — QR / cross-device sign-in state
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.device_login_requests (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_code_hash TEXT NOT NULL UNIQUE,   -- the code lives only as a hash
    device_token_hash TEXT NOT NULL,       -- binds the polling device
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'consumed')),
    decided_at TIMESTAMPTZ DEFAULT NULL,   -- the instant the paired browser answered
    user_id UUID REFERENCES public.users (id) ON DELETE CASCADE,
    ip_address TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_device_login_requests_expiry CHECK (expires_at > created_at)
) USING heap;


-- The archive triggers rest here, after every table they name exists.
CREATE TRIGGER trg_user_groups_deleted_record AFTER DELETE ON public.user_groups FOR EACH ROW EXECUTE FUNCTION fn_soft_delete();
CREATE TRIGGER trg_signup_tokens_deleted_record AFTER DELETE ON public.signup_tokens FOR EACH ROW EXECUTE FUNCTION fn_soft_delete();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_users_deleted_record ON public.users;
DROP TRIGGER IF EXISTS trg_user_groups_deleted_record ON public.user_groups;
DROP TRIGGER IF EXISTS trg_signup_tokens_deleted_record ON public.signup_tokens;
DROP TRIGGER IF EXISTS trg_users_updated_at ON public.users;
DROP TRIGGER IF EXISTS trg_user_passwords_updated_at ON public.user_passwords;
DROP TRIGGER IF EXISTS trg_user_groups_updated_at ON public.user_groups;

DROP INDEX IF EXISTS idx_auth_tokens_user_id_purpose;
DROP INDEX IF EXISTS idx_auth_tokens_expires_at;
DROP INDEX IF EXISTS idx_auth_tokens_user_id;
DROP INDEX IF EXISTS idx_sessions_user_id_expires_at;
DROP INDEX IF EXISTS idx_user_groups_users_user_group_id;
DROP INDEX IF EXISTS idx_user_groups_users_user_id;
DROP INDEX IF EXISTS idx_users_last_login_at;
DROP INDEX IF EXISTS idx_users_created_at;
DROP INDEX IF EXISTS idx_users_username;
DROP INDEX IF EXISTS idx_users_display_name;
DROP INDEX IF EXISTS idx_user_passwords_updated_at;
DROP INDEX IF EXISTS idx_user_passwords_created_at;

DROP TABLE IF EXISTS public.device_login_requests;
DROP TABLE IF EXISTS public.known_devices;
DROP TABLE IF EXISTS public.account_restrictions;
DROP TABLE IF EXISTS public.signup_tokens_user_groups;
DROP TABLE IF EXISTS public.signup_tokens;
DROP TABLE IF EXISTS public.auth_tokens;
DROP TABLE IF EXISTS public.sessions;
DROP TABLE IF EXISTS public.user_groups_users;
DROP TABLE IF EXISTS public.user_groups;
DROP TABLE IF EXISTS public.user_passwords;
DROP TABLE IF EXISTS public.users;


-- +goose StatementEnd
