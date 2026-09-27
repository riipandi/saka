package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/huandu/go-sqlbuilder"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/tango/internal/authz"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/printext"
)

var authPromoteCmd = &cli.Command{
	Name:  "auth:promote",
	Usage: "Grant the administrator role to an account",
	Description: `Grants the administrator role — the standing grant every
administrative surface answers to — to one account, looked up by --email or
--username.

This is the bootstrap path: a fresh database has no administrator until the
seed creates its default account or this command names one. The grant is
idempotent — an account that already holds the role is reported as skipped,
and nothing else changes.

The database must be migrated first; run migrate:up.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "email",
			Usage: "The account's address",
		},
		&cli.StringFlag{
			Name:  "username",
			Usage: "The account's username",
		},
		&cli.BoolFlag{
			Name:  "force",
			Usage: "Skip the confirmation prompt",
		},
	},
	Action: runAuthPromote,
}

// runAuthPromote grants the administrator role to one account.
//
// The role is the whole of the account's elevation, so the command takes a
// confirmation like the other writes: a misnamed account is one Enter away
// from being an administrator.
func runAuthPromote(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	email := cmd.String("email")
	username := cmd.String("username")
	if (email == "") == (username == "") {
		return errors.New("exactly one of --email or --username is required")
	}

	pool, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Shutdown(context.Background())

	p := printext.NewPalette(cmd.Root().Writer)
	identity := email
	column := "email"
	if username != "" {
		identity = username
		column = "username"
	}
	if reportErr := reportTarget(p, databaseOptions(ctx, cfg).DSN); reportErr != nil {
		return reportErr
	}

	proceed, err := confirm(p, cmd, terminalCheck(cmd), fmt.Sprintf("grant %s to %s?", authz.AdministratorRole, identity))
	if err != nil {
		return err
	}
	if !proceed {
		return printStatusLine(p, "nothing granted")
	}

	userID, lookupErr := findAccountID(ctx, pool, column, identity)
	if lookupErr != nil {
		if errors.Is(lookupErr, datastore.ErrNoRows) {
			return fmt.Errorf("no account with %s %q", column, identity)
		}
		return lookupErr
	}

	roleID, roleErr := administratorRoleID(ctx, pool)
	if roleErr != nil {
		return roleErr
	}

	granted, grantErr := ensureRoleGrant(ctx, pool, userID, roleID)
	if grantErr != nil {
		return grantErr
	}
	if granted {
		_, err = fmt.Fprintf(p.Writer(), "granted %s to %s\n", authz.AdministratorRole, identity)
		if err != nil {
			return err
		}
		return printStatusLine(p, "granted")
	}
	_, err = fmt.Fprintf(p.Writer(), "%s already holds %s\n", identity, authz.AdministratorRole)
	if err != nil {
		return err
	}
	return printStatusLine(p, "skipped")
}

// findAccountID reads the identifier the account's row carries, by the
// column the flag named.
func findAccountID(ctx context.Context, pool *datastore.Postgres, column, value string) (string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("u.id")
	sb.From(user.UserTable + " u")
	sb.Where(sb.Equal("u."+column, value))

	query, args := sb.Build()
	var id string
	if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// administratorRoleID reads the row the administrator role's slug names. A
// missing row means the seed never ran — the instruction says so, because a
// promotion without the role would be a silent nothing.
func administratorRoleID(ctx context.Context, pool *datastore.Postgres) (string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("r.id")
	sb.From(authz.RolesTable + " r")
	sb.Where(sb.Equal("r.slug", authz.AdministratorRole))

	query, args := sb.Build()
	var id string
	if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		if errors.Is(err, datastore.ErrNoRows) {
			return "", errors.New("the administrator role does not exist yet; run migrate:seed")
		}
		return "", err
	}
	return id, nil
}

// ensureRoleGrant grants the role unless an active grant already carries it,
// answering whether this run wrote the row. It is the seed's grant rule and
// the command's, spelled once: an active grant is unique, a revoked one is
// history, and a re-grant opens a fresh row.
func ensureRoleGrant(ctx context.Context, pool *datastore.Postgres, userID, roleID string) (bool, error) {
	active := sqlbuilder.PostgreSQL.NewSelectBuilder()
	active.Select("1")
	active.From(authz.UserRolesTable)
	active.Where(active.Equal("user_id", userID), active.Equal("role_id", roleID), active.IsNull("revoked_at"))

	query, args := active.Build()
	var one int
	if pool.QueryRow(ctx, query, args...).Scan(&one) == nil {
		return false, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(authz.UserRolesTable)
	ib.Cols("user_id", "role_id")
	ib.Values(userID, roleID)

	query, args = ib.Build()
	if _, err := pool.Exec(ctx, query, args...); err != nil {
		return false, fmt.Errorf("grant role: %w", err)
	}
	return true, nil
}
