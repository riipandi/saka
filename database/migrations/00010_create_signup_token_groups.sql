-- +goose Up
-- The groups an issued signup token puts its sign-ups into. A token naming
-- no group creates groupless accounts; the rows are the token's to cascade
-- away with it.
CREATE TABLE IF NOT EXISTS public.signup_tokens_user_groups (
    signup_token_id UUID NOT NULL,
    user_group_id UUID NOT NULL,
    PRIMARY KEY (signup_token_id, user_group_id),
    FOREIGN KEY (signup_token_id) REFERENCES public.signup_tokens(id) ON DELETE CASCADE,
    FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE
) USING heap;

CREATE INDEX IF NOT EXISTS idx_signup_tokens_user_groups_user_group_id
    ON public.signup_tokens_user_groups USING btree (user_group_id);

-- +goose Down
DROP TABLE IF EXISTS public.signup_tokens_user_groups;
