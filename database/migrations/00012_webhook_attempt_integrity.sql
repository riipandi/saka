-- +goose Up
-- +goose StatementBegin

-- The attempt number is single-use state per delivery: a lost worker's
-- reclaim must not write a second row for the number an earlier worker
-- already recorded. The unique index is the storage of that rule.
CREATE UNIQUE INDEX IF NOT EXISTS uq_webhook_delivery_attempts_number
    ON public.webhook_delivery_attempts (delivery_id, attempt_number);

-- The retention sweep reads the attempts by age alone; without this index
-- the daily prune is a sequential scan of the whole table.
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_attempts_created_at
    ON public.webhook_delivery_attempts (created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_webhook_delivery_attempts_created_at;
DROP INDEX IF EXISTS uq_webhook_delivery_attempts_number;

-- +goose StatementEnd
