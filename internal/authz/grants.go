package authz

import "strings"

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

// AllSlugs lists every catalog slug, in catalog order. It is the grant set
// of the administrator role and the seed's input.
func AllSlugs() []string {
	permissions := Catalog()
	out := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		out = append(out, permission.Slug)
	}
	return out
}

// Match reports whether a held permission satisfies a requirement.
//
// The requirement names one instance; the grant may name the wildcard. The
// segments compare exactly except the middle one, where `*` in the grant
// stands for any instance. A grant the catalog does not declare still
// matches — the catalog is the seed's input, not the matcher's gate —
// because a slug minted beside a feature must keep working after a catalog
// entry is renamed away under it.
func Match(requirement, grant string) bool {
	required := strings.Split(requirement, ":")
	held := strings.Split(grant, ":")
	if len(required) != 3 || len(held) != 3 {
		return false
	}
	for _, part := range append(required, held...) {
		if part == "" {
			return false
		}
	}
	if required[0] != held[0] || required[2] != held[2] {
		return false
	}
	return held[1] == Wildcard || held[1] == required[1]
}

// Grants reports whether the held set satisfies the requirement: one grant
// in the set matches it.
func Grants(held []string, requirement string) bool {
	for _, grant := range held {
		if Match(requirement, grant) {
			return true
		}
	}
	return false
}

// Effective merges the grants a set of roles carries with the ones granted
// directly, in a stable order: roles first, direct grants after, duplicates
// collapsed. It is the shape an access token carries, and the shape the
// service that loads an account's grants returns.
func Effective(roleGrants, directGrants []string) []string {
	seen := make(map[string]struct{}, len(roleGrants)+len(directGrants))
	out := make([]string, 0, len(roleGrants)+len(directGrants))
	for _, grant := range append(append([]string{}, roleGrants...), directGrants...) {
		if _, dup := seen[grant]; dup {
			continue
		}
		seen[grant] = struct{}{}
		out = append(out, grant)
	}
	return out
}

// ValidSlug reports whether the string is a well-formed permission slug:
// three colon-separated segments over the slug alphabet, with a wildcard
// only in the middle position. It is the gate a role edit passes before a
// grant is written, so a malformed slug cannot enter the set a token later
// carries verbatim.
func ValidSlug(slug string) bool {
	parts := strings.Split(slug, ":")
	if len(parts) != 3 {
		return false
	}
	for i, part := range parts {
		if part == "" {
			return false
		}
		if part == Wildcard {
			// The wildcard is the middle segment's alone.
			if i != 1 {
				return false
			}
			continue
		}
		for _, r := range part {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			default:
				return false
			}
		}
	}
	return true
}
