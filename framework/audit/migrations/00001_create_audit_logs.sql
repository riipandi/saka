-- +goose Up
-- +goose StatementBegin

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
    -- The account the action is about, when there is one. No foreign key on
    -- purpose: an audit record must survive its subject's deletion with the
    -- identifier intact — a log that loses who it was about when the account
    -- goes away is not an audit trail.
    user_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
) USING heap;

CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON public.audit_logs USING btree (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_event ON public.audit_logs USING btree (event);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON public.audit_logs USING btree (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_audit_logs_user_id;
DROP INDEX IF EXISTS idx_audit_logs_event;
DROP INDEX IF EXISTS idx_audit_logs_created_at;

DROP TABLE IF EXISTS public.audit_logs;

DROP TYPE IF EXISTS public.audit_action_status;
DROP TYPE IF EXISTS public.audit_event_trigger;

-- +goose StatementEnd
