-- +goose Up
-- +goose StatementBegin
-- The pairing table arrived in 00002 with a plaintext code column; the
-- feature ships now, and its rule is that the code lives only as a
-- hash. The rename carries the old shape to the rule, and the decided
-- stamp and the expiry check join it.
ALTER TABLE public.device_login_requests RENAME COLUMN code TO user_code_hash;
ALTER TABLE public.device_login_requests
    ADD COLUMN IF NOT EXISTS decided_at TIMESTAMPTZ DEFAULT NULL,
    ADD CONSTRAINT chk_device_login_requests_expiry CHECK (expires_at > created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.device_login_requests DROP CONSTRAINT IF EXISTS chk_device_login_requests_expiry;
ALTER TABLE public.device_login_requests DROP COLUMN IF EXISTS decided_at;
ALTER TABLE public.device_login_requests RENAME COLUMN user_code_hash TO code;
-- +goose StatementEnd
