-- +goose Up
-- +goose StatementBegin

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
-- Table: public.oauth2_sessions (Fosite-style OAuth 2.0 storage)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oauth2_sessions (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    kind TEXT NOT NULL,
    key TEXT NOT NULL,
    request_id TEXT NOT NULL,
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

-- The protocol's login/consent state lives in oauth2_sessions (one kind
-- per manager pointer); the historical interaction_sessions draft never
-- shipped and no code claims it.

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
DROP INDEX IF EXISTS idx_oauth2_jtis_expires_at;
DROP INDEX IF EXISTS idx_oauth2_sessions_client_subject;
DROP INDEX IF EXISTS idx_oauth2_sessions_expires_at;
DROP INDEX IF EXISTS idx_oauth2_sessions_kind_request;
DROP INDEX IF EXISTS idx_oauth2_sessions_kind_key;
DROP INDEX IF EXISTS idx_user_authorized_oidc_clients_last_used_at;
DROP INDEX IF EXISTS idx_user_groups_allowed_oidc_clients_client_id;
DROP INDEX IF EXISTS idx_custom_claims_user_group_id;
DROP INDEX IF EXISTS idx_custom_claims_user_id;

DROP TABLE IF EXISTS public.scim_service_providers;
DROP TABLE IF EXISTS public.oauth2_jtis;
DROP TABLE IF EXISTS public.oauth2_sessions;
DROP TABLE IF EXISTS public.user_groups_allowed_oidc_clients;
DROP TABLE IF EXISTS public.oidc_clients_allowed_user_groups;
DROP TABLE IF EXISTS public.user_authorized_oidc_clients;
DROP TABLE IF EXISTS public.custom_claims;
DROP TABLE IF EXISTS public.oidc_clients;

-- +goose StatementEnd
