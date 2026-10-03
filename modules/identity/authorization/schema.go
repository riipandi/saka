package authorization

import (
	"fmt"
	"time"

	"uuid"

	"go.jetify.com/typeid"

	"github.com/riipandi/saka/internal/authz"
)

// ResourceRole is the resource type an audit record names when the change is
// about a role. A role is not an account, so the record's user_id stays
// empty and the role is named in resource_type and resource_id.
const ResourceRole = "role"

// RoleIDPrefix is the TypeID prefix of a role's identifier. The id leaves
// the server in an API response, so the reader of a log line can tell what
// it names without a lookup.
type RoleIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (RoleIDPrefix) Prefix() string { return "role" }

// RoleID is the typed identifier of one row of RoleTable, in its wire form.
// The column stays a UUID; the conversion lives here and nowhere else.
type RoleID = typeid.TypeID[RoleIDPrefix]

// PermissionIDPrefix is the TypeID prefix of a permission catalog row. The
// slug is what grants and claims carry; the identifier only names the
// catalog entry on the wire.
type PermissionIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (PermissionIDPrefix) Prefix() string { return "perm" }

// PermID is the typed identifier of one row of PermissionsTable.
type PermID = typeid.TypeID[PermissionIDPrefix]

// IDFromUUID wraps the row's UUID into the wire form.
func IDFromUUID(raw uuid.UUID) (RoleID, error) {
	return typeid.FromUUID[RoleID](raw.String())
}

// FormatID renders the wire form of a row's UUID. Rows read from the
// database always carry a valid UUID, so the render cannot fail; an invalid
// one answers the empty string, which no consumer should mistake for an id.
func FormatID(raw uuid.UUID) string {
	id, err := IDFromUUID(raw)
	if err != nil {
		return ""
	}
	return id.String()
}

// ParseID reads the wire form back. It is the boundary a request crosses: an
// identifier that arrives without the prefix names no role, the not-found
// the caller refuses.
func ParseID(wire string) (RoleID, error) {
	parsed, err := typeid.Parse[RoleID](wire)
	if err != nil {
		return RoleID{}, fmt.Errorf("authorization: %w", err)
	}
	return parsed, nil
}

// UUIDFromWire reads the row's UUID out of the wire form. A malformed
// identifier is the caller's not-found, never a 500.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	id, err := ParseID(wire)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("authorization: %w", err)
	}
	parsed, err := uuid.Parse(id.UUID())
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("authorization: %w", err)
	}
	return parsed, nil
}

// Role types as the rows spell them. The column is the `role_type` enum the
// migration declares; the strings are the values it admits.
const (
	RoleTypeSystem = "system"
	RoleTypeCustom = "custom"
)

// RoleSchema is one row of RoleTable.
type RoleSchema struct {
	ID          RoleID
	Name        string
	Slug        string
	Description *string
	Type        string
	CreatedAt   time.Time
	UpdatedAt   *time.Time
}

// RoleRow is one row of the list answer: the schema plus the size of the
// permission set it carries.
type RoleRow struct {
	RoleSchema
	PermissionCount int
}

// RoleDetail is one role as the detail procedures answer it: the row and
// the permission slugs it holds, ordered.
type RoleDetail struct {
	RoleSchema
	Permissions []string
}

// CatalogEntry is one permission as the code declares it: the slug and the
// description.
type CatalogEntry = authz.Permission

// PermissionSchema is one row of the permission catalog as the list answer
// carries it: the row's identifier, the slug, and the description.
type PermissionSchema struct {
	ID          PermID
	Slug        string
	Description string
}
