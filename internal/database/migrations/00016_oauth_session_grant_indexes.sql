-- +goose Up
-- +goose StatementBegin

-- The consent-revocation and end-session paths delete the pointer rows a
-- grant issued: one delete names the grants by the document's grant_id and
-- takes the code and refresh pointers with it. Without an index the delete
-- scans the whole table — every client's protocol state — and the cost
-- grows with the deployment's token population, not with the consent being
-- withdrawn. The partial index carries the two pointer kinds alone.
CREATE INDEX IF NOT EXISTS idx_oauth_sessions_grant_pointer
    ON public.oauth_sessions ((request_data ->> 'grant_id'))
    WHERE kind IN ('authcode', 'refresh');

-- The grant rows themselves are named by client and subject in the same
-- delete; the existing client_subject index serves the authn session's
-- document shape, not the grant's top-level sub member.
CREATE INDEX IF NOT EXISTS idx_oauth_sessions_grant_subject
    ON public.oauth_sessions (client_id, (request_data ->> 'sub'))
    WHERE kind = 'grant';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_oauth_sessions_grant_subject;
DROP INDEX IF EXISTS idx_oauth_sessions_grant_pointer;
-- +goose StatementEnd
