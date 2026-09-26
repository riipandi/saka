-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.webauthn_credentials
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.webauthn_credentials (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    credential_id BYTEA NOT NULL,
    public_key BYTEA NOT NULL,
    device_type TEXT NOT NULL,
    attestation_type TEXT NOT NULL,
    transport JSONB DEFAULT '[]'::jsonb,
    backup_eligible BOOLEAN NOT NULL DEFAULT FALSE,
    backup_state BOOLEAN NOT NULL DEFAULT FALSE,
    aaguid VARCHAR(36) CHECK (
        aaguid IS NULL OR
        aaguid ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    ), -- AAGUID (Authenticator Attestation GUID)
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    last_used_at TIMESTAMPTZ DEFAULT NULL,
    CONSTRAINT unique_credential_id UNIQUE (credential_id)
) USING heap;

CREATE TRIGGER trg_webauthn_credentials_updated_at BEFORE UPDATE ON public.webauthn_credentials FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

-- NOTICE: Index for Bytea column is only optimal for the search for exact match, not LIKE or range.
CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_user_id ON public.webauthn_credentials USING btree (user_id);
CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_credential_id ON public.webauthn_credentials (credential_id);
CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_device_type ON public.webauthn_credentials (device_type);
CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_backup_eligible ON public.webauthn_credentials (backup_eligible) WHERE backup_eligible = TRUE;
CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_last_used_at ON public.webauthn_credentials (last_used_at) WHERE last_used_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_created_at ON public.webauthn_credentials (created_at);
CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_user_device_type ON public.webauthn_credentials (user_id, device_type);

-- --------------------------------------------------------
-- Table: public.webauthn_sessions
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.webauthn_sessions (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID REFERENCES public.users(id) ON DELETE CASCADE,
    challenge TEXT NOT NULL UNIQUE,
    challenge_type TEXT NOT NULL CHECK (challenge_type IN ('registration', 'authentication')),
    user_verification TEXT NOT NULL DEFAULT 'preferred' CHECK (user_verification IN ('required', 'preferred', 'discouraged')),
    credential_params JSONB NOT NULL DEFAULT '[]'::JSONB,
    extensions JSONB NOT NULL DEFAULT '{}', -- Authenticator extension data from ceremonies
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_user_id ON public.webauthn_sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_challenge ON public.webauthn_sessions (challenge);
CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_expires_at ON public.webauthn_sessions USING btree (expires_at);
CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_type ON public.webauthn_sessions (challenge_type);
CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_user_type ON public.webauthn_sessions (user_id, challenge_type);

-- --------------------------------------------------------
-- Table: public.user_mfa_totp — TOTP MFA state: one row per enrolled
-- authenticator, so a user registers several devices or apps and any
-- confirmed one answers the challenge. The secret column is sealed with
-- the canonical enc: prefix.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.user_mfa_totp (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    secret TEXT NOT NULL CHECK (secret LIKE 'enc:%'),
    digits SMALLINT NOT NULL DEFAULT 6 CHECK (digits IN (6, 8)),
    period SMALLINT NOT NULL DEFAULT 30 CHECK (period BETWEEN 15 AND 120),
    algorithm TEXT NOT NULL DEFAULT 'SHA1' CHECK (algorithm IN ('SHA1', 'SHA256', 'SHA512')),
    confirmed_at TIMESTAMPTZ,
    last_used_step BIGINT,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_user_mfa_totp_user_id ON public.user_mfa_totp (user_id);
CREATE INDEX IF NOT EXISTS idx_user_mfa_totp_user_confirmed ON public.user_mfa_totp (user_id) WHERE confirmed_at IS NOT NULL;

CREATE TRIGGER trg_user_mfa_totp_updated_at BEFORE UPDATE ON public.user_mfa_totp FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

-- --------------------------------------------------------
-- Table: public.user_mfa_recovery_codes — hashed single-use codes.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.user_mfa_recovery_codes (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL UNIQUE,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_user_mfa_recovery_codes_user_id ON public.user_mfa_recovery_codes (user_id);
CREATE INDEX IF NOT EXISTS idx_user_mfa_recovery_codes_user_used ON public.user_mfa_recovery_codes (user_id, used_at);

-- --------------------------------------------------------
-- Table: public.user_mfa_pending — the short-lived pending-auth
-- bridge between a successful password sign-in and full session
-- issuance. Several live bridges per account are legitimate: each
-- attempt at the challenge mints its own, and every one dies at its
-- expiry or its consumption.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.user_mfa_pending (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    remember BOOLEAN NOT NULL DEFAULT false,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_user_mfa_pending_expires_at ON public.user_mfa_pending (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS public.user_mfa_pending;
DROP TABLE IF EXISTS public.user_mfa_recovery_codes;

DROP TRIGGER IF EXISTS trg_user_mfa_totp_updated_at ON public.user_mfa_totp;
DROP TABLE IF EXISTS public.user_mfa_totp;

DROP TRIGGER IF EXISTS trg_webauthn_credentials_updated_at ON public.webauthn_credentials;

DROP INDEX IF EXISTS idx_webauthn_sessions_user_type;
DROP INDEX IF EXISTS idx_webauthn_sessions_type;
DROP INDEX IF EXISTS idx_webauthn_sessions_expires_at;
DROP INDEX IF EXISTS idx_webauthn_sessions_challenge;
DROP INDEX IF EXISTS idx_webauthn_sessions_user_id;
DROP INDEX IF EXISTS idx_webauthn_credentials_user_device_type;
DROP INDEX IF EXISTS idx_webauthn_credentials_created_at;
DROP INDEX IF EXISTS idx_webauthn_credentials_last_used_at;
DROP INDEX IF EXISTS idx_webauthn_credentials_backup_eligible;
DROP INDEX IF EXISTS idx_webauthn_credentials_device_type;
DROP INDEX IF EXISTS idx_webauthn_credentials_credential_id;
DROP INDEX IF EXISTS idx_webauthn_credentials_user_id;

DROP TABLE IF EXISTS public.webauthn_sessions;
DROP TABLE IF EXISTS public.webauthn_credentials;

-- +goose StatementEnd
