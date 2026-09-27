// Package authz is the authorization vocabulary: the permission slugs the
// application speaks, the slug grammar, and the match a grant is judged with.
// The slugs are declared in code and nowhere else. A permission the runtime
// can invent is a permission nobody audited; a code-declared catalog is
// reviewable in a diff, seeds itself, and is served to clients read-only so
// a frontend builds its UI from the same strings the server enforces. Roles
// and their assignment are the part an administrator manages; the catalog
// underneath them is not.
//
// The model is Unkey's: a permission is a flat string, a role is a named set
// of permissions, and an account's effective grants are its roles' permissions
// plus the permissions granted to it directly. There is no role inheritance —
// a role that wants another's permissions names them, because a hierarchy's
// answer is always reconstructible from the flat set and its cost (cycle
// detection, closure tables) is not.
package authz

import "fmt"

// Slug grammar: `resource:id:action`, three colon-separated parts.
//
// `resource` names a kind of thing the application protects (`user`,
// `notification`); `id` names the instance the permission is about, `*` for
// every instance of the kind; `action` names the operation (`read`,
// `create`). The wildcard is allowed in the id position only: a permission
// that names every action (`user:*:*`) or every resource (`*:*:read`) is not
// a grant anyone should be able to write by accident, and the catalog does
// not carry one.

// Wildcard is the resource-id segment that stands for every instance of a
// resource kind.
const Wildcard = "*"

// The authorization tables, named where the whole application reads them:
// the grants an access token carries are loaded from here by several
// features, so the names live beside the vocabulary rather than inside one
// feature's schema file.
const (
	RolesTable           = "public.roles"
	PermissionsTable     = "public.permissions"
	RolePermissionsTable = "public.role_permissions"
	UserRolesTable       = "public.user_roles"
	UserPermissionsTable = "public.user_permissions"
)

// Permission is one catalog entry: the slug the server enforces and the
// client renders, and the description a UI shows beside it.
type Permission struct {
	// Slug is the full wire form, `resource:id:action`.
	Slug string
	// Description is the human-readable summary.
	Description string
}

// Resource is a protected kind. It exists so the catalog is written as a
// table of resources and their actions instead of forty loose string
// constants, and so a resource added later is one line here.
type Resource struct {
	// Name is the slug's first segment.
	Name string
	// Actions are the operations the application protects on the kind. An
	// action that is meaningless on the kind does not belong here.
	Actions []string
	// Description is the kind's human-readable summary, reused in the
	// per-action entries' descriptions.
	Description string
}

// Slug builds the permission slug for one action on the resource kind. Every
// catalog permission names the wildcard instance — the per-instance grants a
// feature needs are minted where the instance is known, with the same
// grammar, not by extending this catalog.
func (r Resource) Slug(action string) string {
	return r.Name + ":" + Wildcard + ":" + action
}

// All returns one Permission per action, in the order the resource names
// them.
func (r Resource) All() []Permission {
	out := make([]Permission, 0, len(r.Actions))
	for _, action := range r.Actions {
		out = append(out, Permission{
			Slug:        r.Slug(action),
			Description: r.Description + " — " + action,
		})
	}
	return out
}

// The resources the application protects, in catalog order. The list is the
// single source a seed and the API's catalog endpoint both read, so a
// resource added here appears everywhere on the next run.
var resources = []Resource{
	{
		Name:        "user",
		Actions:     []string{"read", "list", "create", "update", "delete", "ban", "unban", "assign_role", "revoke_role", "grant_permission", "revoke_permission"},
		Description: "an account",
	},
	{
		Name:        "user_group",
		Actions:     []string{"read", "list", "create", "update", "delete", "set_members"},
		Description: "a group of accounts",
	},
	{
		Name:        "role",
		Actions:     []string{"read", "list", "create", "update", "delete"},
		Description: "a named set of permissions",
	},
	{
		Name:        "permission",
		Actions:     []string{"read", "list"},
		Description: "the permission catalog",
	},
	{
		Name:        "session",
		Actions:     []string{"read", "list", "revoke", "impersonate"},
		Description: "a signed-in session",
	},
	{
		Name:        "api_key",
		Actions:     []string{"read", "list", "create", "update", "revoke"},
		Description: "a machine credential",
	},
	{
		Name:        "signup_token",
		Actions:     []string{"create", "list", "delete"},
		Description: "a signup token",
	},
	{
		Name:        "audit_log",
		Actions:     []string{"read", "list", "filter_options"},
		Description: "the audit trail",
	},
	{
		Name:        "notification",
		Actions:     []string{"create", "read", "list", "cancel"},
		Description: "a notification",
	},
	{
		Name:        "file",
		Actions:     []string{"read", "list", "create", "update", "delete"},
		Description: "a stored file",
	},
}

// Catalog holds every permission the application enforces, in catalog order.
// It is derived from the resources above, so an action added to a resource
// appears here without a second edit.
func Catalog() []Permission {
	out := make([]Permission, 0, 64)
	for _, resource := range resources {
		out = append(out, resource.All()...)
	}
	return out
}

// CatalogBySlug indexes the catalog by slug. A slug declared twice is a
// defect caught here, at build time, rather than a seed that silently
// deduplicates it.
func CatalogBySlug() map[string]Permission {
	out := make(map[string]Permission, 64)
	for _, permission := range Catalog() {
		if _, dup := out[permission.Slug]; dup {
			panic(fmt.Sprintf("authz: duplicate catalog slug %q", permission.Slug))
		}
		out[permission.Slug] = permission
	}
	return out
}

// IsCataloged reports whether the slug is one the catalog declares.
func IsCataloged(slug string) bool {
	_, ok := CatalogBySlug()[slug]
	return ok
}
