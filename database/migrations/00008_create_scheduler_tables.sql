-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.queue_tasks
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.queue_tasks (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    queue TEXT NOT NULL,
    task BYTEA NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    priority INTEGER NOT NULL DEFAULT 0,
    wait_until TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
    last_executed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (attempts >= 0),
    CHECK (priority >= 0)
);

CREATE INDEX IF NOT EXISTS idx_queue_tasks_fetch
    ON public.queue_tasks (priority DESC, wait_until ASC NULLS FIRST, id ASC);

-- --------------------------------------------------------
-- Table: public.queue_tasks_completed
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.queue_tasks_completed (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    queue TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_duration_micro BIGINT NOT NULL,
    succeeded BOOLEAN NOT NULL DEFAULT FALSE,
    task BYTEA,
    error TEXT,
    expires_at TIMESTAMPTZ,
    last_executed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (attempts >= 0)
);

CREATE INDEX IF NOT EXISTS idx_queue_tasks_completed_expires ON public.queue_tasks_completed (expires_at) WHERE expires_at IS NOT NULL;

-- --------------------------------------------------------
-- Table: public.scheduler_jobs
--
-- The scheduler's durable state, one row per registered job. The row is the
-- claim: a fire locks it FOR UPDATE, and only the process that moves
-- next_due forward enqueues the task — so every replica may fire the cron
-- time, exactly one of them wins the tick.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.scheduler_jobs (
    name TEXT NOT NULL PRIMARY KEY,
    spec TEXT NOT NULL,
    next_due TIMESTAMPTZ NOT NULL,
    last_fired TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT scheduler_job_name_length CHECK (char_length(name) > 0 AND char_length(name) < 128),
    CONSTRAINT scheduler_job_spec_length CHECK (char_length(spec) > 0 AND char_length(spec) < 512)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_queue_tasks_completed_expires;
DROP INDEX IF EXISTS idx_queue_tasks_fetch;

DROP TABLE IF EXISTS public.scheduler_jobs;
DROP TABLE IF EXISTS public.queue_tasks_completed;
DROP TABLE IF EXISTS public.queue_tasks;

-- +goose StatementEnd
