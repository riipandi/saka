-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.scheduler_jobs
--
-- The scheduler's durable state, one row per registered job. The id is the
-- wire identity (`scd_…`); the name stays the registry's key. The row is the
-- claim: a fire locks it FOR UPDATE, and only the process that moves
-- next_due forward enqueues the task — so every replica may fire the cron
-- time, exactly one of them wins the tick.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.scheduler_jobs (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL UNIQUE,
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

DROP TABLE IF EXISTS public.scheduler_jobs;

-- +goose StatementEnd
