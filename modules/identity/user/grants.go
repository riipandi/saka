package user

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/saka/framework/datastore"
	appauthz "github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/internal/database/entity"
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
	sb.From(entity.TableUserRoles + " ur")
	sb.Join(entity.TableRoles + " r ON r.id = ur.role_id")
	sb.Where(sb.Equal("ur.user_id", userID.String()), sb.IsNull("ur.revoked_at"))
	sb.OrderBy("r.slug")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("user: load roles: %w", err)
	}
	return scanSlugs(rows, "user: load roles")
}

// loadRoleSlugsOfUsers answers one page's role slugs in a single read —
// the batch the list procedure's views fill through, the same live grants
// (`revoked_at IS NULL`) the mint walks through. An account with no grants
// answers an empty slice, the state the wire's repeated field renders as
// absent.
func loadRoleSlugsOfUsers(ctx context.Context, db datastore.Querier, userIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := make(map[uuid.UUID][]string, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("ur.user_id", "r.slug")
	sb.From(entity.TableUserRoles + " ur")
	sb.Join(entity.TableRoles + " r ON r.id = ur.role_id")
	sb.Where(sb.IsNull("ur.revoked_at"), sb.In("ur.user_id", idsAsAny(userIDs)...))
	sb.OrderBy("r.slug")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("user: load roles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var userID uuid.UUID
		var slug string
		if err := rows.Scan(&userID, &slug); err != nil {
			return nil, fmt.Errorf("user: load roles: %w", err)
		}
		out[userID] = append(out[userID], slug)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("user: load roles: %w", err)
	}
	return out, nil
}

// liveAdministratorPredicate is the standing an administrator deletion
// turns on: the role's active grant, an account that can still sign in
// (not disabled, no ban standing), and — for the "another one exists"
// reading — anyone but the account the question is asked about. The check
// rides the delete's own transaction, so the answer and the write see the
// same grants.
func liveAdministratorPredicate(sb *sqlbuilder.SelectBuilder, except *uuid.UUID) {
	sb.Join(entity.TableUsers + " u ON u.id = ur.user_id")
	sb.Where(sb.Equal("r.slug", appauthz.AdministratorRole), sb.IsNull("ur.revoked_at"), sb.Equal("u.disabled", false))
	sb.Where(
		"NOT EXISTS (SELECT 1 FROM " + entity.TableAccountRestrictions + " ar WHERE ar.user_id = u.id AND ar.kind = 'ban' AND ar.lifted_at IS NULL AND (ar.expires_at IS NULL OR ar.expires_at > now()))",
	)
	if except != nil {
		sb.Where(sb.NotEqual("ur.user_id", except.String()))
	}
}

// HoldsLiveAdministratorRole answers whether the account carries the
// administrator standing — the fact the deletion guard refuses on.
func HoldsLiveAdministratorRole(ctx context.Context, db datastore.Querier, userID uuid.UUID) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1")
	sb.From(entity.TableUserRoles + " ur")
	sb.Join(entity.TableRoles + " r ON r.id = ur.role_id")
	liveAdministratorPredicate(sb, nil)
	sb.Where(sb.Equal("ur.user_id", userID.String()))

	query, args := sb.Build()
	var one int
	err := db.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, datastore.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("user: the administrator standing could not be read: %w", err)
	}
	return true, nil
}

// HoldsOtherLiveAdministratorRole answers whether the deployment holds an
// administrator besides the account named — the fact the last
// administrator's guard refuses on. A role grant whose account cannot sign
// in (disabled, banned) is not an administrator anyone can reach, so it
// does not count.
func HoldsOtherLiveAdministratorRole(ctx context.Context, db datastore.Querier, exceptUserID uuid.UUID) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1")
	sb.From(entity.TableUserRoles + " ur")
	sb.Join(entity.TableRoles + " r ON r.id = ur.role_id")
	liveAdministratorPredicate(sb, &exceptUserID)

	query, args := sb.Build()
	var one int
	err := db.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, datastore.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("user: the other administrator could not be read: %w", err)
	}
	return true, nil
}

func idsAsAny(ids []uuid.UUID) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// loadRolePermissions answers the permission slugs the account's active
// roles carry, duplicates included — the merge collapses them.
func loadRolePermissions(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("p.slug")
	sb.From(entity.TableUserRoles + " ur")
	sb.Join(entity.TableRolePermissions + " rp ON rp.role_id = ur.role_id")
	sb.Join(entity.TablePermissions + " p ON p.id = rp.permission_id")
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
	sb.From(entity.TableUserPermissions + " up")
	sb.Join(entity.TablePermissions + " p ON p.id = up.permission_id")
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
