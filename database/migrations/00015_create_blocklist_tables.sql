-- The sign-up blocklist: the identifiers no open-mode sign-up may claim
-- while access.blocklist_enabled is on. An entry is one email address or
-- one `@domain` entry, stored lowercased; the match is case-insensitive and
-- the blocklist service owns the grammar. A blocked exact address also
-- blocks the subaddressed variants of it.

-- +goose Up
CREATE TABLE public.blocklist_entries (
    id uuid PRIMARY KEY,
    pattern text NOT NULL UNIQUE,
    created_by uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT blocklist_entries_pattern_shape CHECK (pattern ~ '^([a-z0-9._%+=-]+(@[a-z0-9.-]+)?|@[a-z0-9.-]+)$')
);

-- +goose Down
DROP TABLE public.blocklist_entries;
