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

-- Pending-table reads beyond the claim: countPending and the ListTasks admin
-- page both filter by queue.
CREATE INDEX IF NOT EXISTS idx_queue_tasks_queue
    ON public.queue_tasks (queue);

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

-- Dead-letter reads (countDead / deadRows / listDead) all filter on
-- succeeded = false (plus queue for the per-queue forms); without this index
-- every admin render of the archive is a sequential scan.
CREATE INDEX IF NOT EXISTS idx_queue_tasks_completed_dead
    ON public.queue_tasks_completed (queue) WHERE succeeded = false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_queue_tasks_completed_dead;
DROP INDEX IF EXISTS idx_queue_tasks_completed_expires;
DROP INDEX IF EXISTS idx_queue_tasks_queue;
DROP INDEX IF EXISTS idx_queue_tasks_fetch;

DROP TABLE IF EXISTS public.queue_tasks_completed;
DROP TABLE IF EXISTS public.queue_tasks;

-- +goose StatementEnd
