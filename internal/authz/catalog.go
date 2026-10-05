// Package authz is the application's permission catalog: the resources the
// binary protects, the system roles the seed creates, and the construction
// of the framework's authorization engine over them.
//
// The slugs are declared in code and nowhere else. A permission the runtime
// can invent is a permission nobody audited; a code-declared catalog is
// reviewable in a diff, seeds itself, and is served to clients read-only so
// a frontend builds its UI from the same strings the server enforces. Roles
// and their assignment are the part an administrator manages; the catalog
// underneath them is not.
//
// The grammar, the matcher, and the catalog type live in framework/authz;
// this package is the binding that feeds it Saka's resources.
package authz

import "github.com/riipandi/saka/framework/authz"

// The resources the application protects, in catalog order. The list is the
// single source a seed and the API's catalog endpoint both read, so a
// resource added here appears everywhere on the next run.
var resources = []authz.Resource{
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

// catalog is the constructed engine value every reader shares. It is built
// at package initialization from the table above; a duplicate slug panics
// there, which is the build-time guarantee the old function form carried.
var catalog = authz.NewCatalog(resources)

// Catalog holds every permission the application enforces, in catalog order.
func Catalog() []authz.Permission {
	return catalog.Permissions()
}

// CatalogBySlug indexes the catalog by slug.
func CatalogBySlug() map[string]authz.Permission {
	return catalog.BySlug()
}

// AllSlugs lists every catalog slug, in catalog order. It is the grant set
// of the administrator role and the seed's input.
func AllSlugs() []string {
	return catalog.AllSlugs()
}

// The role names the seed creates and the guard reads. A system role is a
// row the application depends on: the API refuses to delete or rename one,
// because the surface that mints tokens assumes the administrator role
// exists.
const (
	// AdministratorRole is the system role that stands above the permission
	// catalog. It carries every permission, and it is what the guard's
	// admin rule reads from a caller's claims.
	AdministratorRole = "administrator"
)

// SystemRoles are the roles the seed creates, with the permissions each
// holds. The administrator holds the whole catalog; a deployment that wants
// a narrower standing role mints it as a custom one through the API.
var SystemRoles = []struct {
	Name        string
	Slug        string
	Description string
	Permissions []string
}{
	{
		Name:        "Administrator",
		Slug:        AdministratorRole,
		Description: "Full access to every administrative surface. The role the bootstrap grants.",
		Permissions: AllSlugs(),
	},
}
