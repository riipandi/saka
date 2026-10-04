-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.oauth_connections — one configured provider connection,
-- builtin (google, github; the endpoints are the code's own) or custom
-- (discovery document or manual endpoints). One connection per provider
-- slug: the slug is what BeginSignIn names. The client secret rests
-- sealed with the enc: prefix, the application cipher's mark.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oauth_connections (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    kind TEXT NOT NULL CHECK (kind IN ('builtin', 'custom')),
    provider TEXT NOT NULL,
    display_name TEXT NOT NULL,
    discovery_url TEXT,
    -- Manual endpoints for a custom connection without discovery:
    -- {authorization, token, userinfo, jwks}. NULL for a builtin kind
    -- and for a discovered custom one.
    endpoints JSONB,
    client_id TEXT NOT NULL,
    client_secret TEXT NOT NULL CHECK (client_secret LIKE 'enc:%'),
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- The custom connection's claim mapping: which id_token or userinfo
    -- claim answers the account's email and names. {} keeps the defaults.
    attribute_mapping JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled BOOLEAN NOT NULL DEFAULT FALSE CHECK (enabled IN (TRUE, FALSE)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    UNIQUE (provider)
) USING heap;

CREATE TRIGGER trg_oauth_connections_updated_at BEFORE UPDATE ON public.oauth_connections FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();
CREATE TRIGGER trg_oauth_connections_deleted_record AFTER DELETE ON public.oauth_connections FOR EACH ROW EXECUTE FUNCTION fn_soft_delete();

-- --------------------------------------------------------
-- Table: public.oauth_linked_accounts — one provider identity bound to
-- an account. The binding is unique per connection and provider account
-- id: the same external identity cannot ride two rows under one
-- connection, while the same account may hold several connections.
-- The tokens the provider minted rest sealed like the secret above; an
-- empty value is the provider that answered no tokens for the scopes
-- asked.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oauth_linked_accounts (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    connection_id UUID NOT NULL REFERENCES public.oauth_connections(id) ON DELETE CASCADE,
    provider_account_id TEXT NOT NULL,
    email TEXT NOT NULL,
    email_verified BOOLEAN NOT NULL DEFAULT FALSE CHECK (email_verified IN (TRUE, FALSE)),
    profile JSONB NOT NULL DEFAULT '{}'::jsonb,
    access_token TEXT NOT NULL DEFAULT '' CHECK (access_token = '' OR access_token LIKE 'enc:%'),
    refresh_token TEXT NOT NULL DEFAULT '' CHECK (refresh_token = '' OR refresh_token LIKE 'enc:%'),
    -- When the stored access token dies, named by the provider's
    -- expires_in at the write that stored it. NULL when the provider
    -- answered no expiry: the token's age is then unknown, and the read
    -- that needs a live token refreshes first.
    access_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    UNIQUE (connection_id, provider_account_id)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_oauth_linked_accounts_user_id ON public.oauth_linked_accounts USING btree (user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_linked_accounts_email ON public.oauth_linked_accounts USING btree (lower(email));

CREATE TRIGGER trg_oauth_linked_accounts_updated_at BEFORE UPDATE ON public.oauth_linked_accounts FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();
CREATE TRIGGER trg_oauth_linked_accounts_deleted_record AFTER DELETE ON public.oauth_linked_accounts FOR EACH ROW EXECUTE FUNCTION fn_soft_delete();

-- --------------------------------------------------------
-- Table: public.oauth_flows — one row per authorization-code ceremony.
-- The state and the flow token rest hashed (the raw values travel with
-- the browser), the PKCE verifier rests sealed, and the stage carries
-- the flow's position: pending (the callback has not resolved it),
-- resolved (the provider answered an identity; the resolution at the
-- continue decides the account), verify_email (the provider's address
-- needs the email code), require_names (the provider answered no given
-- or family names), and completed (spent). Several live flows per
-- account are legitimate — a flow names no account until the resolution
-- picks one, so there is no uniqueness on the holder. The resolved
-- identity and the tokens the provider minted rest on the row until the
-- resolution binds them; the tokens are sealed like every other secret.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.oauth_flows (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    connection_id UUID NOT NULL REFERENCES public.oauth_connections(id) ON DELETE CASCADE,
    state_hash TEXT NOT NULL UNIQUE,
    -- The flow token the SPA carries after the callback: NULL while the
    -- browser is out walking (the state is the only handle then), written
    -- hashed by the consume that resolves the identity.
    flow_token_hash TEXT UNIQUE,
    nonce TEXT NOT NULL DEFAULT '',
    code_verifier TEXT NOT NULL DEFAULT '' CHECK (code_verifier = '' OR code_verifier LIKE 'enc:%'),
    stage TEXT NOT NULL CHECK (stage IN ('pending', 'resolved', 'verify_email', 'require_names', 'completed')),
    user_id UUID REFERENCES public.users(id) ON DELETE SET NULL,
    email TEXT NOT NULL DEFAULT '',
    -- The email code's hash while the verify_email stage stands, and the
    -- wrong-answer count the same three-strikes rule the other bridges
    -- keep.
    email_code_hash TEXT NOT NULL DEFAULT '',
    wrong_codes INT NOT NULL DEFAULT 0,
    -- The identity the callback resolved, held on the row until the
    -- resolution binds it.
    provider_account_id TEXT NOT NULL DEFAULT '',
    email_verified BOOLEAN NOT NULL DEFAULT FALSE CHECK (email_verified IN (TRUE, FALSE)),
    given_name TEXT NOT NULL DEFAULT '',
    family_name TEXT NOT NULL DEFAULT '',
    -- The mapped username and the picture address the resolution carries
    -- on to the account: the username is read at the first sign-in only,
    -- the picture refreshed on every one.
    username TEXT NOT NULL DEFAULT '',
    picture TEXT NOT NULL DEFAULT '',
    profile JSONB NOT NULL DEFAULT '{}',
    access_token TEXT NOT NULL DEFAULT '' CHECK (access_token = '' OR access_token LIKE 'enc:%'),
    refresh_token TEXT NOT NULL DEFAULT '' CHECK (refresh_token = '' OR refresh_token LIKE 'enc:%'),
    -- When the resolved access token dies, named by the provider's
    -- expires_in at the callback that wrote it: the value rides the
    -- resolution onto the binding beside the tokens themselves.
    access_expires_at TIMESTAMPTZ,
    redirect_to TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > CURRENT_TIMESTAMP)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_oauth_flows_expires_at ON public.oauth_flows USING btree (expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_flows_user_id ON public.oauth_flows USING btree (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_oauth_flows_user_id;
DROP INDEX IF EXISTS idx_oauth_flows_expires_at;
DROP INDEX IF EXISTS idx_oauth_linked_accounts_email;
DROP INDEX IF EXISTS idx_oauth_linked_accounts_user_id;

DROP TRIGGER IF EXISTS trg_oauth_linked_accounts_deleted_record ON public.oauth_linked_accounts;
DROP TRIGGER IF EXISTS trg_oauth_linked_accounts_updated_at ON public.oauth_linked_accounts;
DROP TRIGGER IF EXISTS trg_oauth_connections_deleted_record ON public.oauth_connections;
DROP TRIGGER IF EXISTS trg_oauth_connections_updated_at ON public.oauth_connections;

DROP TABLE IF EXISTS public.oauth_flows;
DROP TABLE IF EXISTS public.oauth_linked_accounts;
DROP TABLE IF EXISTS public.oauth_connections;

-- +goose StatementEnd
