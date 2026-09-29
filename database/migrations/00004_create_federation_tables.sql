-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.jwks — JSON Web Key Sets for signing/verifying JWTs
-- --------------------------------------------------------

DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'jwt_algorithm') THEN
    CREATE TYPE public.jwt_algorithm AS ENUM (
        'HS256',
        'HS384',
        'HS512',
        'RS256',
        'RS384',
        'RS512',
        'ES256',
        'ES384',
        'ES512'
    );
END IF; END$$;

CREATE TABLE IF NOT EXISTS public.jwks (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    key_id TEXT NOT NULL UNIQUE, -- kid
    algorithm public.jwt_algorithm NOT NULL DEFAULT 'HS256',
    key_type TEXT NOT NULL, -- e.g. RSA, EC, oct
    public_key BYTEA NOT NULL, -- public key for verification (encrypted)
    private_key BYTEA, -- nullable, only for internal use (encrypted)
    use_for TEXT NOT NULL DEFAULT 'sig', -- 'sig' (signature) or 'enc' (encryption)
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    -- Recoverable private key material is always sealed with the
    -- canonical enc: prefix; convert_from fails closed on non-UTF8
    -- bytes, so unprefixed or malformed values are rejected.
    CONSTRAINT chk_jwks_private_key_enc CHECK (private_key IS NULL OR convert_from(private_key, 'UTF8') LIKE 'enc:%')
) USING heap;

CREATE TRIGGER trg_jwks_updated_at BEFORE UPDATE ON public.jwks FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

CREATE INDEX IF NOT EXISTS idx_jwks_active ON public.jwks(is_active);
CREATE INDEX IF NOT EXISTS idx_jwks_algorithm ON public.jwks(algorithm);
CREATE INDEX IF NOT EXISTS idx_jwks_use_for ON public.jwks(use_for);
CREATE INDEX IF NOT EXISTS idx_jwks_expires_at ON public.jwks(expires_at);
CREATE INDEX IF NOT EXISTS idx_jwks_active_algorithm_use_for ON public.jwks(is_active, algorithm, use_for);

-- --------------------------------------------------------
-- Table: public.oidc_clients
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oidc_clients (
    id TEXT NOT NULL PRIMARY KEY,
    name TEXT,
    description TEXT NOT NULL DEFAULT '',
    callback_urls JSONB,
    logout_callback_urls JSONB,
    launch_url TEXT,
    credentials JSONB,
    is_public BOOLEAN DEFAULT FALSE,
    pkce_enabled BOOLEAN DEFAULT FALSE CHECK (pkce_enabled IN (TRUE, FALSE)),
    pkce_supported BOOLEAN NOT NULL DEFAULT FALSE,
    requires_reauthentication BOOLEAN NOT NULL DEFAULT FALSE,
    requires_pushed_authorization_requests BOOLEAN NOT NULL DEFAULT FALSE,
    skip_consent BOOLEAN NOT NULL DEFAULT FALSE,
    is_group_restricted BOOLEAN NOT NULL DEFAULT FALSE,
    client_type TEXT NOT NULL DEFAULT 'standard', -- 'standard' or 'cimd' (Client-ID Metadata Document)
    metadata_url TEXT, -- CIMD: the metadata document URL a client materializes from
    metadata_expires_at TIMESTAMPTZ, -- CIMD document refresh deadline
    metadata_grant_types JSONB, -- Grant types allowed by the CIMD document
    logo_path TEXT, -- Blob path; NULL = no logo
    access_token_duration_minutes BIGINT NOT NULL DEFAULT 60,
    refresh_token_duration_minutes BIGINT NOT NULL DEFAULT 43200,
    -- The back-channel logout delivery: where the provider POSTs the
    -- logout token when a signed-in session ends. An empty URI is the
    -- client opted out — the delivery runs only for the clients that
    -- named a destination. The session-required flag is the OIDC Session
    -- Management signal the delivery carries with the token.
    backchannel_logout_uri TEXT NOT NULL DEFAULT '',
    backchannel_logout_session_required BOOLEAN NOT NULL DEFAULT FALSE,
    -- The grant types the client may use: the wire words the token
    -- endpoint judges, `authorization_code`, `refresh_token`,
    -- `urn:ietf:params:oauth:grant-type:device_code`, and
    -- `client_credentials`. An empty list is the registered default —
    -- the code and refresh and device trio — while a CIMD client's list
    -- is its document's, capped by what the operator granted.
    allowed_grant_types JSONB,
    created_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL
) USING heap;

-- --------------------------------------------------------
-- Table: public.custom_claims
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.custom_claims (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    user_id UUID,
    user_group_id UUID,
    -- Unique per subject: NULLS NOT DISTINCT is what makes one NULL group
    -- id behave like a value — without it, the same claim key could attach
    -- to one account once per NULL row.
    UNIQUE NULLS NOT DISTINCT (key, user_id, user_group_id),
    CHECK (user_id IS NOT NULL OR user_group_id IS NOT NULL),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE
) USING heap;

CREATE INDEX IF NOT EXISTS idx_custom_claims_user_id ON public.custom_claims USING btree (user_id);
CREATE INDEX IF NOT EXISTS idx_custom_claims_user_group_id ON public.custom_claims USING btree (user_group_id);

-- --------------------------------------------------------
-- Table: public.oidc_authorization_codes
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oidc_authorization_codes (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    code TEXT NOT NULL UNIQUE,
    scope TEXT NOT NULL,
    nonce TEXT,
    code_challenge TEXT,
    code_challenge_method_sha256 BOOLEAN,
    authentication_method TEXT NOT NULL DEFAULT '',
    user_id UUID NOT NULL,
    client_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    FOREIGN KEY (client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE
) USING heap;

CREATE INDEX IF NOT EXISTS idx_oidc_authorization_codes_expires_at ON public.oidc_authorization_codes USING btree (expires_at);

-- --------------------------------------------------------
-- Table: public.user_authorized_oidc_clients (junction table)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.user_authorized_oidc_clients (
    user_id UUID NOT NULL,
    client_id TEXT NOT NULL,
    scope JSONB NOT NULL DEFAULT '[]', -- granted scopes as a JSON array
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, client_id),
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    FOREIGN KEY (client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE
) USING heap;

CREATE INDEX IF NOT EXISTS idx_user_authorized_oidc_clients_last_used_at ON public.user_authorized_oidc_clients USING btree (last_used_at);

-- --------------------------------------------------------
-- Table: public.oidc_clients_allowed_user_groups (client-side group restriction)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oidc_clients_allowed_user_groups (
    user_group_id UUID NOT NULL,
    oidc_client_id TEXT NOT NULL,
    PRIMARY KEY (oidc_client_id, user_group_id),
    FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE,
    FOREIGN KEY (oidc_client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE
) USING heap;

-- --------------------------------------------------------
-- Table: public.user_groups_allowed_oidc_clients — group-side client
-- allowlist (the inverse of oidc_clients_allowed_user_groups).
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.user_groups_allowed_oidc_clients (
    user_group_id UUID NOT NULL,
    oidc_client_id TEXT NOT NULL,
    PRIMARY KEY (user_group_id, oidc_client_id),
    FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE,
    FOREIGN KEY (oidc_client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE
) USING heap;

CREATE INDEX IF NOT EXISTS idx_user_groups_allowed_oidc_clients_client_id
    ON public.user_groups_allowed_oidc_clients USING btree (oidc_client_id);

-- --------------------------------------------------------
-- Table: public.oidc_device_codes — OAuth 2.0 Device Flow (RFC 8628).
-- Device and user codes are stored as SHA-256 hashes; status drives
-- the token poll (pending → approved/denied → consumed). PAR (RFC
-- 9126) lives in oauth2_sessions with kind 'par'.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oidc_device_codes (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    device_code_hash TEXT NOT NULL UNIQUE,
    user_code_hash TEXT NOT NULL UNIQUE,
    scope TEXT NOT NULL,
    resource TEXT,
    nonce TEXT,
    client_id TEXT NOT NULL,
    user_id UUID,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'consumed')),
    last_polled_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    approved_at TIMESTAMPTZ DEFAULT NULL,
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE,
    FOREIGN KEY (client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE,
    CONSTRAINT chk_oidc_device_codes_expiry CHECK (expires_at > created_at)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_oidc_device_codes_expires_at ON public.oidc_device_codes (expires_at);
CREATE INDEX IF NOT EXISTS idx_oidc_device_codes_status ON public.oidc_device_codes (status);

-- --------------------------------------------------------
-- Table: public.oauth2_sessions (Fosite-style OAuth 2.0 storage)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oauth2_sessions (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    kind TEXT NOT NULL,
    key TEXT NOT NULL,
    request_id TEXT NOT NULL,
    access_token_signature TEXT NOT NULL DEFAULT '',
    active BOOLEAN NOT NULL DEFAULT TRUE,
    request_data JSONB NOT NULL,
    -- A logout session may name no client: an RP-initiated logout
    -- without a client_id or an id_token_hint is still a flow the
    -- provider tracks.
    client_id TEXT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE,
    CONSTRAINT chk_oauth2_sessions_client_id CHECK (client_id IS NULL OR client_id = request_data ->> 'client_id')
) USING heap;

CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth2_sessions_kind_key ON public.oauth2_sessions (kind, key);
CREATE INDEX IF NOT EXISTS idx_oauth2_sessions_kind_request ON public.oauth2_sessions (kind, request_id);
CREATE INDEX IF NOT EXISTS idx_oauth2_sessions_expires_at ON public.oauth2_sessions (expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth2_sessions_client_subject
    ON public.oauth2_sessions (client_id, (request_data #>> '{session,subject}'), kind, active);

-- --------------------------------------------------------
-- Table: public.oauth2_jtis (replay-protected JWT IDs)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oauth2_jtis (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    jti TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
) USING heap;

CREATE INDEX IF NOT EXISTS idx_oauth2_jtis_expires_at ON public.oauth2_jtis (expires_at);

-- --------------------------------------------------------
-- Table: public.interaction_sessions (OIDC login/consent flow state)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.interaction_sessions (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    consent_required BOOLEAN NOT NULL DEFAULT FALSE,
    reauthentication_required BOOLEAN NOT NULL DEFAULT FALSE,
    authentication_required BOOLEAN NOT NULL DEFAULT FALSE,
    account_selection_required BOOLEAN NOT NULL DEFAULT FALSE,
    scopes JSONB NOT NULL DEFAULT '[]',
    client_id TEXT NOT NULL,
    user_id UUID,
    requested_at TIMESTAMPTZ NOT NULL,
    reauthenticated_at TIMESTAMPTZ,
    parameters JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE
) USING heap;

CREATE INDEX IF NOT EXISTS idx_interaction_sessions_client_id ON public.interaction_sessions (client_id);
CREATE INDEX IF NOT EXISTS idx_interaction_sessions_user_id ON public.interaction_sessions (user_id);

-- --------------------------------------------------------
-- Table: public.scim_service_providers
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.scim_service_providers (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    endpoint TEXT NOT NULL,
    token TEXT NOT NULL, -- sealed with the canonical enc: prefix
    oidc_client_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_synced_at TIMESTAMPTZ,
    FOREIGN KEY (oidc_client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE,
    CONSTRAINT chk_scim_token_enc CHECK (token LIKE 'enc:%')
) USING heap;

-- One provider per client: the provider IS the client's outbound provisioning target; a second row for the same client is a defect.
CREATE UNIQUE INDEX IF NOT EXISTS idx_scim_providers_client ON public.scim_service_providers (oidc_client_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_jwks_updated_at ON public.jwks;

DROP INDEX IF EXISTS idx_scim_providers_client;
DROP INDEX IF EXISTS idx_interaction_sessions_client_id;
DROP INDEX IF EXISTS idx_interaction_sessions_user_id;
DROP INDEX IF EXISTS idx_oauth2_jtis_expires_at;
DROP INDEX IF EXISTS idx_oauth2_sessions_client_subject;
DROP INDEX IF EXISTS idx_oauth2_sessions_expires_at;
DROP INDEX IF EXISTS idx_oauth2_sessions_kind_request;
DROP INDEX IF EXISTS idx_oauth2_sessions_kind_key;
DROP INDEX IF EXISTS idx_oidc_device_codes_status;
DROP INDEX IF EXISTS idx_oidc_device_codes_expires_at;
DROP INDEX IF EXISTS idx_oidc_authorization_codes_expires_at;
DROP INDEX IF EXISTS idx_user_authorized_oidc_clients_last_used_at;
DROP INDEX IF EXISTS idx_user_groups_allowed_oidc_clients_client_id;
DROP INDEX IF EXISTS idx_custom_claims_user_group_id;
DROP INDEX IF EXISTS idx_custom_claims_user_id;
DROP INDEX IF EXISTS idx_jwks_active_algorithm_use_for;
DROP INDEX IF EXISTS idx_jwks_expires_at;
DROP INDEX IF EXISTS idx_jwks_use_for;
DROP INDEX IF EXISTS idx_jwks_algorithm;
DROP INDEX IF EXISTS idx_jwks_active;

DROP TABLE IF EXISTS public.scim_service_providers;
DROP TABLE IF EXISTS public.interaction_sessions;
DROP TABLE IF EXISTS public.oauth2_jtis;
DROP TABLE IF EXISTS public.oauth2_sessions;
DROP TABLE IF EXISTS public.oidc_device_codes;
DROP TABLE IF EXISTS public.user_groups_allowed_oidc_clients;
DROP TABLE IF EXISTS public.oidc_clients_allowed_user_groups;
DROP TABLE IF EXISTS public.user_authorized_oidc_clients;
DROP TABLE IF EXISTS public.oidc_authorization_codes;
DROP TABLE IF EXISTS public.custom_claims;
DROP TABLE IF EXISTS public.oidc_clients;
DROP TABLE IF EXISTS public.jwks;

DROP TYPE IF EXISTS public.jwt_algorithm;

-- +goose StatementEnd
