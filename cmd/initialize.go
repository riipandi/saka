package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/database/seeders"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/password"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/printext"
)

var initializeCmd = &cli.Command{
	Name:      "initialize",
	Usage:     "Prepare a fresh database: the system seed and the first administrator",
	ArgsUsage: "",
	Category:  "Deployment commands",
	Description: `Applies the system seed — the permission catalog, the signing
key pair the application signs its tokens with, and the settings
catalog the product flows read — and creates the first
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
		// The refusal is the run's outcome, not a crash: it answers in the
		// shape every outcome answers in — one status line, the reasons
		// indented beneath it — and exits nonzero without a second print
		// of the message the CLI's error path would add.
		if _, err = fmt.Fprintf(p.Writer(),
			"status: refused\n  this database already holds %d %s — initialize never touches a running installation\n  to recover administrator access: saka admin:reset-password <email>\n",
			accounts, printext.Plural(int(accounts), "account")); err != nil {
			return err
		}
		return cli.Exit("", 1)
	}

	hash, err := crypto.NewPasswordHasher().Hash(creds)
	if err != nil {
		return fmt.Errorf("initialize: hash credential: %w", err)
	}

	// The signing key pair is provisioned in the same transaction as the
	// system seed: a deployment that runs initialize is one that can sign
	// in, and signing needs a key the database holds. A run without the
	// auth secret cannot seal the private half and stops here — there is
	// nothing to bootstrap without one. The seal key is the auth half
	// (AUTH_SECRET_KEY, derived), so rotating it later is what invalidates
	// the pair, and rotating the application secret never touches signing.
	if cfg.Auth.SecretKey == "" {
		return fmt.Errorf("initialize: AUTH_SECRET_KEY is required (app=%q unresolved=%v)", cfg.App.SecretKey, cfg.Unresolved())
	}
	cipher, err := crypto.NewAuthCipher(cfg.Auth.SecretKey)
	if err != nil {
		return fmt.Errorf("initialize: AUTH_SECRET_KEY: %w", err)
	}
	signingAlgorithm := cfg.Auth.JWTAlgorithm
	if signingAlgorithm == "" || config.IsHMACAlgorithm(signingAlgorithm) {
		signingAlgorithm = crypto.DefaultSignatureAlgorithm
	}

	recorder := audit.NewRecorder(slog.New(slog.DiscardHandler))
	var results []seeders.Result
	var provisionedKid string
	err = pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		var seedErr error
		results, seedErr = seeders.Run(ctx, tx, false, seeders.System(cipher, signingAlgorithm)...)
		if seedErr != nil {
			return seedErr
		}
		// The seeder reports the kid among its created rows when it
		// provisioned; the audit record names it so a rotation's history
		// starts at the first key.
		for _, result := range results {
			if result.Name == seeders.JWKSKeyPairSeederName && len(result.Created) > 0 {
				provisionedKid = result.Created[0]
			}
		}
		if provisionedKid != "" {
			recorder.Record(ctx, tx, audit.Entry{
				Event:  audit.EventJwksProvisioned,
				Status: audit.StatusSuccess,
				Payload: map[string]string{
					"kid":       provisionedKid,
					"algorithm": signingAlgorithm,
					"source":    "initialize",
				},
			})
		}

		repo := user.NewRepository()
		now := time.Now().UTC()
		id, createErr := repo.CreateUser(ctx, tx, user.UserSchema{
			Username: username,
			Email:    email,
			// The first administrator's address is operator-set, never
			// mail-verified; the account must open the deployment it
			// bootstraps, so it ships verified like every seeder account.
			EmailVerifiedAt: &now,
			DisplayName:     "Administrator",
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

	// One summary line per seeder: a deployment command reports what it
	// changed at the granularity an operator acts on, and the per-record
	// detail stays the development seed's output.
	for _, result := range results {
		if printErr := printSeedSummary(p, result); printErr != nil {
			return printErr
		}
	}
	if _, err = fmt.Fprintf(p.Writer(), "administrator: %s <%s>\n", username, email); err != nil {
		return err
	}
	if generated {
		if _, err = fmt.Fprintf(p.Writer(), "password: %s\n", creds); err != nil {
			return err
		}
		if _, err = fmt.Fprintf(p.Writer(), "  the generated credential — store it now; it is not recoverable\n"); err != nil {
			return err
		}
	}
	return printStatusLine(p, "initialized")
}

// printSeedSummary writes one line per seeder — the name and the two counts
// — so an initialize reports the system seed without the per-record noise
// the development seed's report carries.
func printSeedSummary(p printext.Palette, result seeders.Result) error {
	name := strings.ToLower(strings.TrimSuffix(result.Name, "Seeder"))
	return p.Printf("%s%s: %s, %s\n",
		progressIndent, name,
		p.Green(fmt.Sprintf("%d created", len(result.Created))),
		p.Green(fmt.Sprintf("%d skipped", len(result.Skipped))))
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
	sb.From(entity.TableUsers)

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
	ib.InsertInto(entity.TableUserRoles)
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
	sb.From(entity.TableRoles + " r")
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
