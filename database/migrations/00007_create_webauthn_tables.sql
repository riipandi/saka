-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.webauthn_credentials — passkeys and browser
-- authenticators. The credential_id's UNIQUE constraint carries the
-- lookup index; the roll's reads filter by user_id (the limit's count
-- and the settings page's list, at most max_credentials rows).
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.webauthn_credentials (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    credential_id BYTEA NOT NULL,
    public_key BYTEA NOT NULL,
    -- The signature counter the authenticator reports. Persisted so a counter
    -- regression between two assertions is visible: that regression is the
    -- clone signal, and upstream's choice not to store it disables detection.
    sign_count BIGINT NOT NULL DEFAULT 0,
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

CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_user_id ON public.webauthn_credentials USING btree (user_id);

-- --------------------------------------------------------
-- Table: public.webauthn_sessions — one row per ceremony (registration
-- or authentication), the challenge the browser answers.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.webauthn_sessions (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID REFERENCES public.users(id) ON DELETE CASCADE,
    challenge TEXT NOT NULL UNIQUE,
    challenge_type TEXT NOT NULL CHECK (challenge_type IN ('registration', 'authentication')),
    -- A registration ceremony always names its account; an authentication
    -- ceremony may not, because usernameless sign-in resolves the account
    -- from the credential, not from the request.
    CONSTRAINT chk_webauthn_sessions_type_user CHECK (
        (challenge_type = 'registration' AND user_id IS NOT NULL)
        OR challenge_type = 'authentication'
    ),
    user_verification TEXT NOT NULL DEFAULT 'preferred' CHECK (user_verification IN ('required', 'preferred', 'discouraged')),
    credential_params JSONB NOT NULL DEFAULT '[]'::JSONB,
    extensions JSONB NOT NULL DEFAULT '{}', -- Authenticator extension data from ceremonies
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP)
) USING heap;

-- The ceremonies are read by their handle alone; the sweep reads the
-- expiry. No query filters by holder or kind — an authentication session
-- names no account until its assertion resolves one.
CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_expires_at ON public.webauthn_sessions USING btree (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_webauthn_sessions_expires_at;
DROP INDEX IF EXISTS idx_webauthn_credentials_user_id;

DROP TRIGGER IF EXISTS trg_webauthn_credentials_updated_at ON public.webauthn_credentials;
DROP TABLE IF EXISTS public.webauthn_sessions;
DROP TABLE IF EXISTS public.webauthn_credentials;

-- +goose StatementEnd
