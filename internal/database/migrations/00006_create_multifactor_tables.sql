-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.mfa_totp — TOTP MFA state: one row per enrolled
-- authenticator, so a user registers several devices or apps and any
-- confirmed one answers the challenge. The secret column is sealed with
-- the canonical enc: prefix.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.mfa_totp (
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

CREATE INDEX IF NOT EXISTS idx_mfa_totp_user_id ON public.mfa_totp (user_id);
CREATE INDEX IF NOT EXISTS idx_mfa_totp_user_confirmed ON public.mfa_totp (user_id) WHERE confirmed_at IS NOT NULL;

CREATE TRIGGER trg_mfa_totp_updated_at BEFORE UPDATE ON public.mfa_totp FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

-- --------------------------------------------------------
-- Table: public.mfa_recovery_codes — hashed single-use codes.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.mfa_recovery_codes (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL UNIQUE,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_mfa_recovery_codes_user_id ON public.mfa_recovery_codes (user_id);
CREATE INDEX IF NOT EXISTS idx_mfa_recovery_codes_user_used ON public.mfa_recovery_codes (user_id, used_at);

-- --------------------------------------------------------
-- Table: public.mfa_pending — the short-lived pending-auth
-- bridge between a successful password sign-in and full session
-- issuance. Several live bridges per account are legitimate: each
-- attempt at the challenge mints its own, and every one dies at its
-- expiry or its consumption.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.mfa_pending (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    remember BOOLEAN NOT NULL DEFAULT false,
    -- What the bridge admits its holder to: `verify` is the second-factor
    -- challenge a confirmed account completes, `enroll` is the enrollment a
    -- zero-factor account is routed to while `mfa.required` stands — the
    -- bridge proves the password, the enrollment endpoints judge it.
    purpose TEXT NOT NULL DEFAULT 'verify' CONSTRAINT chk_mfa_pending_purpose CHECK (purpose IN ('verify', 'enroll')),
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- The wrong-code budget the bridge has spent. The count rides the row so
    -- every replica judges the same bridge the same way, and so the budget
    -- survives a process restart inside the bridge's life.
    wrong_attempts INT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_mfa_pending_expires_at ON public.mfa_pending (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS public.mfa_pending;
DROP TABLE IF EXISTS public.mfa_recovery_codes;

DROP TRIGGER IF EXISTS trg_mfa_totp_updated_at ON public.mfa_totp;
DROP TABLE IF EXISTS public.mfa_totp;

-- +goose StatementEnd
