package user

import (
	"context"
	"fmt"
	"slices"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/internal/datastore"
)

// LoadGrants reads one account's effective grants: the slugs of the roles
// the account actively holds, and the permissions those roles carry plus the
// ones granted to the account directly.
//
// It is the one door every token mint walks through — the sign-ins, the
// refresh, the delegation, the machine credential — because the claims a
// token carries must describe the same account the same way wherever the
// token was minted. Only an active grant counts: a revoked one is history,
// and history does not authorize.
//
// The three reads are reads: no transaction holds them together, so a grant
// that changes between them answers in the next token. An account with no
// grants answers two empty slices, which is a state, not an error.
// LoadGrants reads one account's effective grants: the slugs of the roles
// the account actively holds, and the permissions those roles carry plus the
// ones granted to the account directly.
//
// It is the one door every token mint walks through — the sign-ins, the
// refresh, the delegation, the machine credential — because the claims a
// token carries must describe the same account the same way wherever the
// token was minted. Only an active grant counts: a revoked one is history,
// and history does not authorize.
//
// The three reads are reads: no transaction holds them together, so a grant
// that changes between them answers in the next token. An account with no
// grants answers two empty slices, which is a state, not an error.
func LoadGrants(ctx context.Context, db datastore.Querier, userID uuid.UUID) (roles, permissions []string, err error) {
	roles, err = loadRoleSlugs(ctx, db, userID)
	if err != nil {
		return nil, nil, err
	}

	roleCarried, err := loadRolePermissions(ctx, db, userID)
	if err != nil {
		return nil, nil, err
	}
	direct, err := loadDirectPermissions(ctx, db, userID)
	if err != nil {
		return nil, nil, err
	}
	return roles, mergePermissions(roleCarried, direct), nil
}

// loadRoleSlugs answers the slugs of the account's active role grants.
func loadRoleSlugs(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("r.slug")
	sb.From(authz.UserRolesTable + " ur")
	sb.Join(authz.RolesTable + " r ON r.id = ur.role_id")
	sb.Where(sb.Equal("ur.user_id", userID.String()), sb.IsNull("ur.revoked_at"))
	sb.OrderBy("r.slug")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("user: load roles: %w", err)
	}
	return scanSlugs(rows, "user: load roles")
}

// loadRolePermissions answers the permission slugs the account's active
// roles carry, duplicates included — the merge collapses them.
func loadRolePermissions(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("p.slug")
	sb.From(authz.UserRolesTable + " ur")
	sb.Join(authz.RolePermissionsTable + " rp ON rp.role_id = ur.role_id")
	sb.Join(authz.PermissionsTable + " p ON p.id = rp.permission_id")
	sb.Where(sb.Equal("ur.user_id", userID.String()), sb.IsNull("ur.revoked_at"))

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("user: load role permissions: %w", err)
	}
	return scanSlugs(rows, "user: load role permissions")
}

// loadDirectPermissions answers the permission slugs granted to the account
// itself, outside any role.
func loadDirectPermissions(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("p.slug")
	sb.From(authz.UserPermissionsTable + " up")
	sb.Join(authz.PermissionsTable + " p ON p.id = up.permission_id")
	sb.Where(sb.Equal("up.user_id", userID.String()), sb.IsNull("up.revoked_at"))

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("user: load direct permissions: %w", err)
	}
	return scanSlugs(rows, "user: load direct permissions")
}

// scanSlugs drains one slug-per-row result.
func scanSlugs(rows pgx.Rows, where string) ([]string, error) {
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		out = append(out, slug)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	return out, nil
}

// mergePermissions collapses the role-carried and directly granted slugs
// into one deduplicated, ordered set — the list a token carries.
func mergePermissions(roleCarried, direct []string) []string {
	seen := make(map[string]struct{}, len(roleCarried)+len(direct))
	out := make([]string, 0, len(roleCarried)+len(direct))
	for _, slug := range append(roleCarried, direct...) {
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		out = append(out, slug)
	}
	slices.Sort(out)
	return out
}
