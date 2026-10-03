package seeders

import (
	"context"
	"fmt"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/internal/datastore"
)

// AuthorizationSeederName is the name this seeder reports under. The
// "Seeder" suffix keeps it distinct from the entities it writes, which
// appear next to it on the same report line.
const AuthorizationSeederName = "AuthorizationSeeder"

// AdministratorRoleName is the role slug the seed's default account is
// granted, exported for the report a test asserts against.
const AdministratorRoleName = authz.AdministratorRole

// Authorization returns the seeder for the permission catalog and the
// system roles.
//
// The catalog is the code's own list, mirrored into the permissions table
// so roles can reference the slugs by key; the system roles are the rows
// the application depends on, and the administrator carries the whole
// catalog. It runs before the user seeder, whose default account takes the
// administrator role the moment it exists.
func Authorization() Seeder {
	return Seeder{
		Name:  AuthorizationSeederName,
		Apply: applyAuthorization,
	}
}

// applyAuthorization writes the catalog's permissions, the system roles,
// and the role-permission junctions. Every write is guarded by its natural
// key — the slug — so a second run keeps the existing rows and reports them
// as skipped.
func applyAuthorization(
	ctx context.Context,
	q datastore.Querier,
	dryRun bool,
) (created, skipped []string, err error) {
	for _, permission := range authz.Catalog() {
		inserted, insertErr := insertPermission(ctx, q, permission, dryRun)
		if insertErr != nil {
			return nil, nil, insertErr
		}
		if inserted {
			created = append(created, permission.Slug)
		} else {
			skipped = append(skipped, permission.Slug)
		}
	}

	for _, role := range authz.SystemRoles {
		roleID, insertErr := insertSystemRole(ctx, q, role.Name, role.Slug, role.Description, "system", dryRun)
		if insertErr != nil {
			return nil, nil, insertErr
		}
		granted, grantErr := grantRolePermissions(ctx, q, roleID, role.Permissions, dryRun)
		if grantErr != nil {
			return nil, nil, grantErr
		}
		if granted > 0 {
			created = append(created, role.Slug+" ("+fmt.Sprint(granted)+" permissions)")
		} else {
			skipped = append(skipped, role.Slug)
		}
	}

	for _, role := range scenarioRoles {
		if dryRun {
			if slugExists(ctx, q, entity.TableRoles, "slug", role.slug) {
				skipped = append(skipped, role.slug)
			} else {
				created = append(created, role.slug)
			}
			continue
		}
		roleID, insertErr := insertSystemRole(ctx, q, role.name, role.slug, role.description, "custom", dryRun)
		if insertErr != nil {
			return nil, nil, insertErr
		}
		granted, grantErr := grantRolePermissions(ctx, q, roleID, role.permissions, dryRun)
		if grantErr != nil {
			return nil, nil, grantErr
		}
		if granted > 0 {
			created = append(created, role.slug+" ("+fmt.Sprint(granted)+" permissions)")
		} else {
			skipped = append(skipped, role.slug)
		}
	}
	return created, skipped, nil
}

// scenarioRoles are the custom roles a development database carries, each
// with the permission set that makes its name honest. They give the claims
// pipeline more than the administrator role to carry, so a multi-role token
// is a thing a local test reads, not one it imagines.
//
// The set is seeded additively and never rewritten: a run that finds the
// role reports it as skipped and leaves the administrator's edits alone,
// the same courtesy the junction rows get.
var scenarioRoles = []scenarioRole{
	{
		name:        "Editor",
		slug:        "editor",
		description: "Reads accounts, groups, and notifications",
		permissions: []string{
			"user:*:read",
			"user:*:list",
			"user_group:*:read",
			"user_group:*:list",
			"notification:*:read",
			"notification:*:list",
		},
	},
	{
		name:        "Moderator",
		slug:        "moderator",
		description: "Bans and unbans accounts, reads the audit trail",
		permissions: []string{
			"user:*:ban",
			"user:*:unban",
			"audit_log:*:read",
			"audit_log:*:list",
			"audit_log:*:filter_options",
		},
	},
	{
		name:        "Service",
		slug:        "service",
		description: "Manages the machine credentials an integration presents",
		permissions: []string{
			"api_key:*:read",
			"api_key:*:list",
			"api_key:*:create",
			"api_key:*:revoke",
		},
	},
}

// scenarioRole is one custom role the seeder writes: the display name, the
// stable slug, and the permission slugs its set carries.
type scenarioRole struct {
	name        string
	slug        string
	description string
	permissions []string
}

// insertPermission writes one catalog permission and answers whether this
// run created it. Under a dry run it answers the same without writing.
func insertPermission(ctx context.Context, q datastore.Querier, permission authz.Permission, dryRun bool) (bool, error) {
	if dryRun {
		return !slugExists(ctx, q, entity.TablePermissions, "slug", permission.Slug), nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TablePermissions)
	ib.Cols("slug", "description")
	ib.Values(permission.Slug, permission.Description)
	ib.SQL("ON CONFLICT (slug) DO NOTHING")

	query, args := ib.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("insert permission %s: %w", permission.Slug, err)
	}
	return tag.RowsAffected() > 0, nil
}

// insertSystemRole writes one role row of either kind and answers its
// identifier. The description is nullable in the table, so an empty one
// reads as NULL.
func insertSystemRole(ctx context.Context, q datastore.Querier, name, slug, description, roleType string, dryRun bool) (string, error) {
	if dryRun {
		return slug, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableRoles)
	ib.Cols("name", "slug", "description", "type")
	ib.Values(name, slug, nullable(description), roleType)
	ib.SQL("ON CONFLICT (slug) DO NOTHING")

	query, args := ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return "", fmt.Errorf("insert role %s: %w", slug, err)
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TableRoles)
	sb.Where(sb.Equal("slug", slug))

	query, args = sb.Build()
	var id string
	if err := q.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		return "", fmt.Errorf("read role %s: %w", slug, err)
	}
	return id, nil
}

// grantRolePermissions writes the junction rows one role's set names,
// resolving the slugs to their rows in one query. The junction's primary
// key deduplicates, so a second run inserts nothing and reports the role as
// skipped. It answers how many rows this run wrote.
func grantRolePermissions(ctx context.Context, q datastore.Querier, roleID string, slugs []string, dryRun bool) (int, error) {
	if len(slugs) == 0 {
		return 0, nil
	}
	if dryRun {
		return 0, nil
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TablePermissions)
	sb.Where(sb.In("slug", toSeedAny(slugs)...))

	query, args := sb.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("read permissions for role %s: %w", roleID, err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return 0, fmt.Errorf("read permissions for role %s: %w", roleID, scanErr)
		}
		ids = append(ids, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return 0, fmt.Errorf("read permissions for role %s: %w", roleID, rowsErr)
	}
	if len(ids) != len(slugs) {
		return 0, fmt.Errorf("role %s: %d of %d catalog slugs are seeded", roleID, len(ids), len(slugs))
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableRolePermissions)
	ib.Cols("role_id", "permission_id")
	for _, id := range ids {
		ib.Values(roleID, id)
	}
	ib.SQL("ON CONFLICT DO NOTHING")

	query, args = ib.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("grant permissions to role %s: %w", roleID, err)
	}
	return int(tag.RowsAffected()), nil
}

// slugExists answers whether the natural key is already in the table — the
// check a dry run reports from.
func slugExists(ctx context.Context, q datastore.Querier, table, column, value string) bool {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1")
	sb.From(table)
	sb.Where(sb.Equal(column, value))

	query, args := sb.Build()
	var one int
	return q.QueryRow(ctx, query, args...).Scan(&one) == nil
}

// nullable hands the writer a pointer for a column that may be NULL.
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// toSeedAny is the slugs' slice, for the IN clause's variadic form.
func toSeedAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
