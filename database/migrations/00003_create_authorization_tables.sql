-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- ENUM for role types (default: custom)
-- ============================================================================
DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'role_type') THEN
    CREATE TYPE role_type AS ENUM ('system', 'custom');
END IF; END$$;

-- ============================================================================
-- Roles and permissions.
--
-- A permission's slug is declared in code (`internal/authz`) and mirrored
-- here so roles can reference it; the API serves the catalog read-only. A
-- role of type `system` is seeded and may not be deleted or renamed from
-- the API; the administrator role is one of them.
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.roles (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL UNIQUE CHECK (char_length(name) > 0),
    slug TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9_-]+$'),
    description TEXT,
    type role_type NOT NULL DEFAULT 'custom',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL
) USING heap;

CREATE TABLE IF NOT EXISTS public.permissions (
    id UUID NOT NULL PRIMARY KEY DEFAULT uuidv7(),
    slug TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9_.*:-]+$'),
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT NULL
) USING heap;

-- ============================================================================
-- Grants.
--
-- A grant row carries its own life: assigned_at opens it, revoked_at ends
-- it, and both stay so an assignment's history survives a revoke. The
-- primary key therefore includes assigned_at — a re-grant after a revoke
-- opens a new row — while the partial unique index holds the live rule:
-- one active grant per pair.
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.role_permissions (
    role_id UUID NOT NULL REFERENCES public.roles(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES public.permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
) USING heap;

CREATE TABLE IF NOT EXISTS public.user_roles (
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES public.roles(id) ON DELETE CASCADE,
    granted_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ DEFAULT NULL,
    revoked_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, role_id, assigned_at)
) USING heap;

CREATE TABLE IF NOT EXISTS public.user_permissions (
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES public.permissions(id) ON DELETE CASCADE,
    granted_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ DEFAULT NULL,
    revoked_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, permission_id, assigned_at)
) USING heap;

-- ============================================================================
-- Indexes for RBAC tables
-- ============================================================================
CREATE INDEX IF NOT EXISTS idx_role_permissions_permission ON public.role_permissions(permission_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_user ON public.user_roles(user_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_role ON public.user_roles(role_id);
CREATE INDEX IF NOT EXISTS idx_user_permissions_user ON public.user_permissions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_permissions_permission ON public.user_permissions(permission_id);

-- The live rule: one active grant per pair. A revoked grant does not count,
-- which is what lets the composite primary key admit a re-grant.
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_roles_active ON public.user_roles(user_id, role_id) WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_permissions_active ON public.user_permissions(user_id, permission_id) WHERE revoked_at IS NULL;

-- ============================================================================
-- updated_at triggers for roles and permissions
-- ============================================================================
CREATE TRIGGER trg_roles_updated_at BEFORE UPDATE ON public.roles FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();
CREATE TRIGGER trg_permissions_updated_at BEFORE UPDATE ON public.permissions FOR EACH ROW EXECUTE FUNCTION fn_updated_at_value();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_permissions_updated_at ON public.permissions;
DROP TRIGGER IF EXISTS trg_roles_updated_at ON public.roles;

DROP INDEX IF EXISTS uq_user_permissions_active;
DROP INDEX IF EXISTS uq_user_roles_active;
DROP INDEX IF EXISTS idx_user_permissions_permission;
DROP INDEX IF EXISTS idx_user_permissions_user;
DROP INDEX IF EXISTS idx_user_roles_role;
DROP INDEX IF EXISTS idx_user_roles_user;
DROP INDEX IF EXISTS idx_role_permissions_permission;

DROP TABLE IF EXISTS public.user_permissions;
DROP TABLE IF EXISTS public.user_roles;
DROP TABLE IF EXISTS public.role_permissions;
DROP TABLE IF EXISTS public.permissions;
DROP TABLE IF EXISTS public.roles;
DROP TYPE IF EXISTS role_type;

-- +goose StatementEnd
