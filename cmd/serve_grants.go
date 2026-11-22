package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/framework/datastore"
	appauthz "github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/internal/database/entity"
)

// checkAdministratorGrants warns when the administrator role's junction
// lags the catalog the binary carries. A catalog addition reaches an
// existing database only when a seeder runs, so a deployment seeded before
// it keeps minting tokens whose permissions claim hides every surface the
// new slugs protect — the guard answers on role membership, the frontend
// gates on the claim, and nothing names the gap. The check is a read: it
// never writes, and the warning carries the repair command.
func checkAdministratorGrants(ctx context.Context, db datastore.Querier, log *slog.Logger) error {
	granted, err := administratorGrants(ctx, db)
	if err != nil {
		return err
	}

	missing := missingCatalogSlugs(granted)
	if len(missing) == 0 {
		return nil
	}
	log.WarnContext(ctx,
		"the administrator grant lags the permission catalog",
		"missing", strings.Join(missing, ", "),
		"count", len(missing),
		"repair", "run `saka migrate:seed`; every sign-in minted before it carries the stale claim set")
	return nil
}

// administratorGrants reads the permission slugs the administrator role's
// junction carries. A role the database does not hold — a database the
// system seed never ran against — answers an empty set quietly, because
// the initialize command owns that state and its own refusal for it.
func administratorGrants(ctx context.Context, db datastore.Querier) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1")
	sb.From(entity.TableRoles)
	sb.Where(sb.Equal("slug", appauthz.AdministratorRole))

	query, args := sb.Build()
	var one int
	err := db.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, datastore.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("serve: read the administrator role: %w", err)
	}

	sb = sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("p.slug")
	sb.From(entity.TableRolePermissions + " rp")
	sb.Join(entity.TableRoles + " r ON r.id = rp.role_id")
	sb.Join(entity.TablePermissions + " p ON p.id = rp.permission_id")
	sb.Where(sb.Equal("r.slug", appauthz.AdministratorRole))

	query, args = sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("serve: read the administrator junction: %w", err)
	}
	defer rows.Close()

	granted := []string{}
	for rows.Next() {
		var slug string
		if scanErr := rows.Scan(&slug); scanErr != nil {
			return nil, fmt.Errorf("serve: read the administrator junction: %w", scanErr)
		}
		granted = append(granted, slug)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("serve: read the administrator junction: %w", rowsErr)
	}
	return granted, nil
}

// missingCatalogSlugs answers the catalog slugs the granted set does not
// carry, in catalog order — the list the warning names.
func missingCatalogSlugs(granted []string) []string {
	grantedSet := make(map[string]struct{}, len(granted))
	for _, slug := range granted {
		grantedSet[slug] = struct{}{}
	}

	missing := []string{}
	for _, slug := range appauthz.AllSlugs() {
		if _, ok := grantedSet[slug]; !ok {
			missing = append(missing, slug)
		}
	}
	return missing
}
