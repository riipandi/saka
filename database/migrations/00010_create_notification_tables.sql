-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.notifications (admin-authored notifications)
--
-- category: 'system' is a targeted notice about an account;
-- 'announcement' is a broadcast. topic names an announcement's
-- subject and is absent on a system notice. The audience is one
-- of three kinds: 'global' (every account), 'users' (the named
-- accounts, listed in notification_users), or 'user_groups'
-- (the members of the named groups, listed in
-- notification_user_groups). A cancelled row stops being
-- visible without being deleted, so an administrator can still
-- read what was withdrawn.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.notifications (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    category TEXT NOT NULL CHECK (category IN ('system', 'announcement')),
    topic TEXT CHECK (topic IS NULL OR char_length(topic) BETWEEN 1 AND 64),
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    audience_kind TEXT NOT NULL CHECK (audience_kind IN ('global', 'users', 'user_groups')),
    -- A system notice targets accounts, so its audience is never global.
    CONSTRAINT uq_notifications_system_audience
        CHECK (category <> 'system' OR audience_kind <> 'global'),
    created_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    cancelled_at TIMESTAMPTZ DEFAULT NULL,
    email_sent_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL
) USING heap;

CREATE TRIGGER trg_notifications_updated_at
    BEFORE UPDATE ON public.notifications FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

CREATE INDEX IF NOT EXISTS idx_notifications_created_at
    ON public.notifications USING btree (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_category
    ON public.notifications USING btree (category);

-- --------------------------------------------------------
-- Table: public.notification_users (named accounts of a 'users' audience)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.notification_users (
    notification_id UUID NOT NULL REFERENCES public.notifications(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    PRIMARY KEY (notification_id, user_id)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_notification_users_user_id
    ON public.notification_users USING btree (user_id);

-- --------------------------------------------------------
-- Table: public.notification_user_groups (groups of a 'user_groups' audience)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.notification_user_groups (
    notification_id UUID NOT NULL REFERENCES public.notifications(id) ON DELETE CASCADE,
    user_group_id UUID NOT NULL REFERENCES public.user_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (notification_id, user_group_id)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_notification_user_groups_user_group_id
    ON public.notification_user_groups USING btree (user_group_id);

-- --------------------------------------------------------
-- Table: public.notification_reads (per-account read receipts)
--
-- One row per (notification, account) the moment the account
-- marks it read. Absent means unread; the receipts are the
-- whole read state, so a global announcement needs no fanout
-- rows at creation.
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.notification_reads (
    notification_id UUID NOT NULL REFERENCES public.notifications(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    read_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (notification_id, user_id)
) USING heap;

CREATE INDEX IF NOT EXISTS idx_notification_reads_user_id
    ON public.notification_reads USING btree (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.notification_reads;
DROP TABLE IF EXISTS public.notification_user_groups;
DROP TABLE IF EXISTS public.notification_users;
DROP TABLE IF EXISTS public.notifications;
-- +goose StatementEnd
