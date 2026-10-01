-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- Webhook delivery model: signed endpoints (secret sealed with the
-- canonical enc: prefix), immutable delivery body bytes, and per-attempt
-- rows. Signing uses HMAC-SHA256 over "<timestamp>.<body>".
-- ============================================================================

-- --------------------------------------------------------
-- Table: public.webhook_endpoints
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.webhook_endpoints (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL, -- Example: user.login-xxxxxxxxxx
    description TEXT,
    endpoint TEXT,
    method TEXT NOT NULL CHECK (method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE')),
    headers JSONB DEFAULT NULL, -- Example: {"k1":"v1","k2":"v2"}
    enabled BOOLEAN NOT NULL DEFAULT TRUE CHECK (enabled IN (TRUE, FALSE)),
    secret_enc TEXT, -- AES-256-GCM ciphertext (crypto.Cipher wire format), never plaintext
    event_types TEXT[] NOT NULL DEFAULT '{}', -- Subscribed event names; empty or '*' means every event
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL,
    CONSTRAINT webhook_endpoints_name_key UNIQUE (name),
    -- Webhook name only allows alphanumeric characters, dash, and underscores (3-100 chars)
    CONSTRAINT chk_webhook_name_format CHECK (name IS NULL OR name ~ '^[a-zA-Z0-9_-]{3,100}$'),
    CONSTRAINT chk_webhook_secret_enc CHECK (secret_enc LIKE 'enc:%')
) USING heap;

CREATE TRIGGER trg_webhook_endpoints_updated_at BEFORE UPDATE ON public.webhook_endpoints FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

-- (name needs no index: the UNIQUE constraint carries every lookup)
CREATE INDEX IF NOT EXISTS idx_webhook_endpoints_enabled ON public.webhook_endpoints (enabled);
CREATE INDEX IF NOT EXISTS idx_webhook_endpoints_created_at ON public.webhook_endpoints (created_at);
CREATE INDEX IF NOT EXISTS idx_webhook_endpoints_event_types ON public.webhook_endpoints USING GIN (event_types);

-- --------------------------------------------------------
-- Table: public.webhook_deliveries — one row per event per endpoint;
-- body holds the exact canonical bytes that every retry re-signs.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.webhook_deliveries (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    webhook_id UUID REFERENCES public.webhook_endpoints(id) ON DELETE SET NULL,
    event TEXT NOT NULL,
    body BYTEA NOT NULL, -- the exact canonical bytes; retries sign and deliver these
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at TIMESTAMPTZ DEFAULT NULL
) USING heap;

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_webhook_id ON public.webhook_deliveries (webhook_id);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_event ON public.webhook_deliveries (event);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status ON public.webhook_deliveries (status);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_created_at_desc ON public.webhook_deliveries (created_at DESC);

-- --------------------------------------------------------
-- Table: public.webhook_delivery_attempts — redacted response
-- metadata per attempt; no request snapshot.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.webhook_delivery_attempts (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    delivery_id UUID NOT NULL REFERENCES public.webhook_deliveries(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    response_status INTEGER DEFAULT NULL,
    error TEXT DEFAULT NULL,
    duration_ms INTEGER DEFAULT NULL CHECK (duration_ms IS NULL OR duration_ms >= 0),
    response JSONB DEFAULT NULL, -- redacted response metadata only
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
) USING heap;

-- The attempt number is single-use state per delivery: a lost worker's reclaim
-- must not write a second row for the number an earlier worker already recorded.
-- The unique index is the storage of that rule.
CREATE UNIQUE INDEX IF NOT EXISTS uq_webhook_delivery_attempts_number ON public.webhook_delivery_attempts (delivery_id, attempt_number);
-- The retention sweep reads the attempts by age alone; without this index the
-- daily prune is a sequential scan of the whole table.
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_attempts_created_at ON public.webhook_delivery_attempts (created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_webhook_delivery_attempts_created_at;
DROP INDEX IF EXISTS uq_webhook_delivery_attempts_number;
DROP TABLE IF EXISTS public.webhook_delivery_attempts;

DROP INDEX IF EXISTS idx_webhook_deliveries_created_at_desc;
DROP INDEX IF EXISTS idx_webhook_deliveries_status;
DROP INDEX IF EXISTS idx_webhook_deliveries_event;
DROP INDEX IF EXISTS idx_webhook_deliveries_webhook_id;
DROP TABLE IF EXISTS public.webhook_deliveries;

DROP TRIGGER IF EXISTS trg_webhook_endpoints_updated_at ON public.webhook_endpoints;

DROP INDEX IF EXISTS idx_webhook_endpoints_event_types;
DROP INDEX IF EXISTS idx_webhook_endpoints_created_at;
DROP INDEX IF EXISTS idx_webhook_endpoints_enabled;

DROP TABLE IF EXISTS public.webhook_endpoints;

-- +goose StatementEnd
