// Package authz is the authorization engine: the permission slug grammar,
// the match a grant is judged with, and the catalog a deployment constructs
// from its own resource table.
//
// The model is Unkey's: a permission is a flat string, a role is a named set
// of permissions, and an account's effective grants are its roles' permissions
// plus the permissions granted to it directly. There is no role inheritance —
// a role that wants another's permissions names them, because a hierarchy's
// answer is always reconstructible from the flat set and its cost (cycle
// detection, closure tables) is not.
//
// The engine carries no resource table of its own. The resources a binary
// protects are the binary's vocabulary; they come in through NewCatalog, and
// the grammar and the matcher work with or without one.
package authz

import "fmt"

// Slug grammar: `resource:id:action`, three colon-separated parts.
//
// `resource` names a kind of thing the application protects (`user`,
// `notification`); `id` names the instance the permission is about, `*` for
// every instance of the kind; `action` names the operation (`read`,
// `create`). The wildcard is allowed in the id position only: a permission
// that names every action (`user:*:*`) or every resource (`*:*:read`) is not
// a grant anyone should be able to write by accident, and a catalog does not
// carry one.

// Wildcard is the resource-id segment that stands for every instance of a
// resource kind.
const Wildcard = "*"

// Permission is one catalog entry: the slug the server enforces and the
// client renders, and the description a UI shows beside it.
type Permission struct {
	// Slug is the full wire form, `resource:id:action`.
	Slug string
	// Description is the human-readable summary.
	Description string
}

// Resource is a protected kind. It exists so a catalog is written as a table
// of resources and their actions instead of forty loose string constants,
// and so a resource added later is one line there.
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
// grammar, not by extending the catalog.
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

// Catalog holds every permission a deployment enforces, in the order its
// resources declare. It is constructed once from the resource table; the
// derived index and slug list are built with it, so an action added to a
// resource appears everywhere without a second edit.
//
// A slug declared twice is a defect caught at construction — the panic names
// it — rather than a seed that silently deduplicates it.
type Catalog struct {
	order  []Permission
	bySlug map[string]Permission
}

// NewCatalog derives the permission set from the resources, in resource and
// action order.
func NewCatalog(resources []Resource) *Catalog {
	order := make([]Permission, 0, 64)
	bySlug := make(map[string]Permission, 64)
	for _, resource := range resources {
		for _, permission := range resource.All() {
			if _, dup := bySlug[permission.Slug]; dup {
				panic(fmt.Sprintf("authz: duplicate catalog slug %q", permission.Slug))
			}
			order = append(order, permission)
			bySlug[permission.Slug] = permission
		}
	}
	return &Catalog{order: order, bySlug: bySlug}
}

// Permissions lists every catalog permission, in catalog order.
func (c *Catalog) Permissions() []Permission {
	return c.order
}

// BySlug indexes the catalog by slug.
func (c *Catalog) BySlug() map[string]Permission {
	return c.bySlug
}

// AllSlugs lists every catalog slug, in catalog order. It is the grant set
// a standing role that holds everything is built from, and a seed's input.
func (c *Catalog) AllSlugs() []string {
	out := make([]string, 0, len(c.order))
	for _, permission := range c.order {
		out = append(out, permission.Slug)
	}
	return out
}
