package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/tango/database/seeders"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/authz"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/password"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/printext"
)

var initializeCmd = &cli.Command{
	Name:      "initialize",
	Usage:     "Prepare a fresh database: the system seed and the first administrator",
	ArgsUsage: "",
	Category:  "Deployment commands",
	Description: `Applies the system seed — the permission catalog and the system
roles the application's own surfaces depend on — and creates the first
administrator account.

The command is the deployment's one-time bootstrap: it refuses to run
against a database that already holds an account, so a running
installation is never touched. Recovering an administrator's access is
admin:reset-password's job, not a second initialize.

Every flag is optional: the address defaults to admin@example.com, the
username to "admin", and the password to one the command generates and
prints exactly once after the run — an unattended install reads it from
the command's own output. A password passed with --admin-password must
satisfy the credential policy and is never echoed back.

The database must be migrated first; run migrate:up.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "admin-email",
			Usage: "The administrator's address",
			Value: "admin@example.com",
		},
		&cli.StringFlag{
			Name:  "admin-password",
			Usage: "The administrator's credential (generated and printed once when absent)",
		},
		&cli.StringFlag{
			Name:  "admin-username",
			Usage: "The administrator's handle",
			Value: "admin",
		},
	},
	Action: runInitialize,
}

// runInitialize applies the system seed and opens the first account.
//
// The refusal is the safeguard: an installation is recognized by its own
// state — one live account is enough — and a refusal names the recovery
// path instead of overwriting anything. There is no override flag on
// purpose; a database whose account rows exist is not this command's to
// change.
func runInitialize(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	if err = requireMigrated(ctx, cfg); err != nil {
		return err
	}

	email := cmd.String("admin-email")
	creds := cmd.String("admin-password")
	username := cmd.String("admin-username")
	if username == "" {
		return errors.New("--admin-username must not be empty")
	}
	if !emailPattern.MatchString(email) {
		return fmt.Errorf("initialize: %q is not an address the account table accepts", email)
	}

	generated := false
	if creds == "" {
		var genErr error
		creds, genErr = generateCredential()
		if genErr != nil {
			return fmt.Errorf("initialize: generate credential: %w", genErr)
		}
		generated = true
	}
	if valErr := password.Validate(creds); valErr != nil {
		return valErr
	}

	pool, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Shutdown(context.Background())

	p := printext.NewPalette(cmd.Root().Writer)
	if reportErr := reportTarget(p, databaseOptions(ctx, cfg).DSN); reportErr != nil {
		return reportErr
	}

	accounts, err := countAccounts(ctx, pool)
	if err != nil {
		return err
	}
	if accounts > 0 {
		return fmt.Errorf("this database already holds %d %s; initialize refuses to touch it — use admin:reset-password to recover administrator access",
			accounts, printext.Plural(int(accounts), "account"))
	}

	hash, err := crypto.NewPasswordHasher().Hash(creds)
	if err != nil {
		return fmt.Errorf("initialize: hash credential: %w", err)
	}

	recorder := audit.NewRecorder(slog.New(slog.DiscardHandler))
	var results []seeders.Result
	err = pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		var seedErr error
		results, seedErr = seeders.Run(ctx, tx, false, seeders.System()...)
		if seedErr != nil {
			return seedErr
		}

		repo := user.NewRepository()
		id, createErr := repo.CreateUser(ctx, tx, user.UserSchema{
			Username:    username,
			Email:       email,
			DisplayName: "Administrator",
			Timezone:    user.DefaultTimezone,
		})
		var pgErr *pgconn.PgError
		if errors.As(createErr, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("initialize: the %q account already exists", username)
		}
		if createErr != nil {
			return createErr
		}
		if passErr := repo.CreatePassword(ctx, tx, id, hash); passErr != nil {
			return passErr
		}
		if grantErr := grantAdministrator(ctx, tx, id.String()); grantErr != nil {
			return grantErr
		}
		recorder.Record(ctx, tx, audit.Entry{
			Event:  audit.EventAccountCreated,
			Status: audit.StatusSuccess,
			UserID: id.String(),
			Payload: map[string]string{
				"username": username,
				"source":   "initialize",
			},
		})
		return nil
	})
	if err != nil {
		return err
	}

	if printErr := printSeedResults(p, results, false, 0); printErr != nil {
		return printErr
	}
	_, err = fmt.Fprintf(p.Writer(), "administrator %s <%s> created\n", username, email)
	if err != nil {
		return err
	}
	if generated {
		_, err = fmt.Fprintf(p.Writer(), "\n%s\n\n(the generated credential — store it now; it is not recoverable)\n", creds)
		if err != nil {
			return err
		}
	}
	return printStatusLine(p, "initialized")
}

// emailPattern is the address shape the users table's CHECK carries, so a
// malformed --admin-email is refused with the reason named rather than as
// the constraint's raw failure.
var emailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)

// countAccounts answers how many live accounts the database holds. The soft
// delete archives removed rows out of the users table, so the count is the
// live installation and nothing else.
func countAccounts(ctx context.Context, pool *datastore.Postgres) (int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(user.UserTable)

	query, args := sb.Build()
	var total int64
	if err := pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("initialize: read accounts: %w", err)
	}
	return total, nil
}

// grantAdministrator opens the role grant that makes an account the
// administrator. An active grant is unique, a revoked one is history, and a
// re-grant opens a fresh row — the seed's rule, spelled once here for the
// two commands that mint or recover an administrator.
func grantAdministrator(ctx context.Context, db datastore.Querier, userID string) error {
	roleID, err := administratorRoleID(ctx, db)
	if err != nil {
		return err
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(authz.UserRolesTable)
	ib.Cols("user_id", "role_id")
	ib.Values(userID, roleID)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil // the account already holds the role
		}
		return fmt.Errorf("initialize: grant role: %w", err)
	}
	return nil
}

// administratorRoleID reads the row the administrator role's slug names. A
// missing row means the system seed never ran — the instruction says so,
// because a grant without the role would be a silent nothing.
func administratorRoleID(ctx context.Context, db datastore.Querier) (string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("r.id")
	sb.From(authz.RolesTable + " r")
	sb.Where(sb.Equal("r.slug", authz.AdministratorRole))

	query, args := sb.Build()
	var id string
	if err := db.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		if errors.Is(err, datastore.ErrNoRows) {
			return "", errors.New("the administrator role does not exist; the system seed did not run")
		}
		return "", err
	}
	return id, nil
}
