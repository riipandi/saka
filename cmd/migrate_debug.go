//go:build debug

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/migration"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/database"
	"github.com/riipandi/saka/internal/database/seeders"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/printext"
)

var migrateCreateCmd = &cli.Command{
	Name:     "migrate:create",
	Category: "Development commands",
	Usage:    "Create a new migration file",
	Description: `Writes a new migration skeleton with the next free version and a
normalized name. The name is refused when another migration already
uses it, whatever its version, so no two files can share a name.

The file starts with empty Up and Down blocks; migrate:up reports such
a migration as "empty" until statements are added. A new file only
reaches the migrator after a rebuild, because migrations are embedded
in the binary.`,
	Arguments: []cli.Argument{
		&cli.StringArg{
			Name:      "name",
			UsageText: "<MIGRATION_NAME>",
			Required:  true,
		},
	},
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "dir",
			Usage: "Directory to write into",
			Value: database.MigrationsPath,
		},
	},
	Action: runMigrateCreate,
}

// runMigrateCreate writes one migration file and reports where it landed.
func runMigrateCreate(_ context.Context, cmd *cli.Command) error {
	created, err := migration.CreateMigration(migration.CreateOptions{
		Dir:  cmd.String("dir"),
		Name: cmd.StringArg("name"),
	})
	if err != nil {
		return err
	}

	p := printext.NewPalette(cmd.Root().Writer)
	return p.Printf("%s %s (version %s)\n",
		created.Path,
		p.Green("created"),
		p.Dim(fmt.Sprintf("%0*d", migration.MigrationPrefixWidth, created.Version)))
}

var migrateResetCmd = &cli.Command{
	Name:     "migrate:reset",
	Category: "Development commands",
	Usage:    "Rollback all migrations",
	Description: `Rolls back every applied migration, newest first. Pass --up to
re-apply them afterwards, which rebuilds the schema from scratch.
Pass --seed to run the seeders after the re-apply, so one command
leaves a fresh schema with its initial data; --seed needs --up,
because seeding writes rows onto the schema the re-apply builds.`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "up",
			Usage: "Re-apply all migrations after the rollback (fresh schema)",
		},
		&cli.BoolFlag{
			Name:  "seed",
			Usage: "Run the seeders after the re-apply (requires --up)",
		},
		&cli.BoolFlag{
			Name:  "dry-run",
			Usage: "Print what would be rolled back without changing anything",
		},
		&cli.BoolFlag{
			Name:  "force",
			Usage: "Skip the confirmation prompt",
		},
	},
	Action: runMigrateReset,
}

var migrateSeedCmd = &cli.Command{
	Name:     "migrate:seed",
	Category: "Development commands",
	Usage:    "Seed the database with initial data",
	Description: `Creates the default records a fresh database needs. Every seeder is
idempotent, so running this command twice changes nothing the second
time: an existing record is reported as skipped.

Seeding writes data, so it asks for confirmation. It runs in one
transaction, so a seeder that fails leaves nothing behind. --dry-run
reports what would be created without writing anything.

The database must be migrated first; run migrate:up.`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "dry-run",
			Usage: "Report what would be seeded without changing anything",
		},
		&cli.BoolFlag{
			Name:  "force",
			Usage: "Skip the confirmation prompt",
		},
	},
	Action: runMigrateSeed,
}

// runMigrateSeed applies every seeder and reports what each one created.
func runMigrateSeed(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	dsn, err := requireDatabaseURL(cfg)
	if err != nil {
		return err
	}

	if err := requireMigrated(ctx, cfg); err != nil {
		return err
	}

	pool, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Shutdown(context.Background())

	p := printext.NewPalette(cmd.Root().Writer)
	if err := reportTarget(p, dsn); err != nil {
		return err
	}

	return seedDatabase(ctx, cmd, pool, p, developmentSeeders(cfg))
}

// developmentSeeders resolves the seeder list a development seed runs: every
// seeder, plus the JWKS provisioning when the environment carries an auth
// secret to seal the private half with.
func developmentSeeders(cfg config.Config) []seeders.Seeder {
	cipher, err := crypto.NewAuthCipher(cfg.Auth.SecretKey)
	if err != nil {
		return seeders.All()
	}
	signingAlgorithm := cfg.Auth.JWTAlgorithm
	if signingAlgorithm == "" || config.IsHMACAlgorithm(signingAlgorithm) {
		signingAlgorithm = crypto.DefaultSignatureAlgorithm
	}
	return seeders.SeedJWKS(cipher, signingAlgorithm)
}

// seedDatabase applies every seeder and reports what each one created.
// `migrate:seed` and the seed half of `migrate:reset --up --seed` answer
// through it: the flags the two commands share — --dry-run, --force — mean
// the same thing on both, so the reading of them lives here once.
//
// The JWKS provisioning rides the development seed when the environment
// carries an application secret to seal with: a seeded database is one a
// developer signs in against, and sign-in needs a signing key.
func seedDatabase(ctx context.Context, cmd *cli.Command, pool *datastore.Postgres, p printext.Palette, list []seeders.Seeder) error {
	dryRun := cmd.Bool("dry-run")

	// A dry run writes nothing, so it needs no confirmation and no
	// transaction: there is nothing to roll back.
	if dryRun {
		started := time.Now()
		results, err := seeders.Run(ctx, pool, true, list...)
		if err != nil {
			return err
		}
		return printSeedResults(p, results, true, time.Since(started))
	}

	proceed, err := confirm(p, cmd, terminalCheck(cmd), "seed the database?")
	if err != nil {
		return err
	}
	if !proceed {
		return printStatusLine(p, "nothing seeded")
	}

	// The seeder reports nothing until it finishes, so the spinner is the only
	// sign the command is alive. It starts after the prompt, so it never
	// animates while the command waits for an answer.
	spin := newSpinner(p.Writer())
	if spin != nil {
		spin.Suffix = " seeding"
		spin.Start()
	}

	started := time.Now()
	var results []seeders.Result
	err = pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		var seedErr error
		results, seedErr = seeders.Run(ctx, tx, false, list...)
		return seedErr
	})

	// The spinner must be stopped before the first result line is written.
	// Otherwise its next frame lands at the start of that line and the two run
	// together on the terminal.
	if spin != nil {
		spin.Stop()
	}
	if err != nil {
		return err
	}
	return printSeedResults(p, results, false, time.Since(started))
}

// printSeedResults reports one line per record and a summary. A dry run uses
// future tense, so its output cannot be mistaken for a report of work done.
// printSeedResults writes one line per record the seed touched, then the
// outcome line at column zero. It lives outside the debug-tagged file
// because `initialize` — a release command — answers the same way.
func printSeedResults(p printext.Palette, results []seeders.Result, dryRun bool, elapsed time.Duration) error {
	var created, skipped int
	for _, result := range results {
		for _, key := range result.Created {
			created++
			if err := printSeedLine(p, result.Name, key, "created", "would create", dryRun); err != nil {
				return err
			}
		}
		for _, key := range result.Skipped {
			skipped++
			if err := printSeedLine(p, result.Name, key, "skipped", "would skip", dryRun); err != nil {
				return err
			}
		}
	}

	if created+skipped > 0 {
		if err := p.Printf("\n"); err != nil {
			return err
		}
	}
	if dryRun {
		return printStatusLine(p, "%s, %s %s",
			p.Yellow(fmt.Sprintf("%d to create", created)),
			p.Yellow(fmt.Sprintf("%d to skip", skipped)),
			p.Dim("in "+printext.Duration(elapsed)))
	}
	return printStatusLine(p, "%s, %s %s",
		p.Green(fmt.Sprintf("%d created", created)),
		p.Green(fmt.Sprintf("%d skipped", skipped)),
		p.Dim("in "+printext.Duration(elapsed)))
}

// printSeedLine writes one record. The seeder name comes first so the output
// sorts and greps by what was seeded. Both wordings are passed in rather than
// derived: "create" and "skip" do not share a past-tense rule.
//
// A record that was created is a success and one that was skipped already
// existed, so the two are told apart by colour.
func printSeedLine(p printext.Palette, seeder, key, verb, dryRunVerb string, dryRun bool) error {
	if dryRun {
		return p.Printf("%s%s %s %s\n", progressIndent, seeder, key, p.Yellow(dryRunVerb))
	}
	style := printext.Yellow
	if verb == "created" {
		style = printext.Green
	}
	return p.Printf("%s%s %s %s\n", progressIndent, seeder, key, p.Paint(style, verb))
}

// restart begins a new phase of the same command. It resets the clock and the
// state column, so the second half of `migrate:reset --up` is timed on its own
// and uses its own width.
//
// The reporter is reset in place rather than replaced: the migrator holds a
// method value taken from this reporter, so a new instance would never be called
// and the second half would silently keep the first half's width and clock.
//
// It lives in this file because `migrate:reset` is debug-only, and a release
// build would compile it into an unused method.
func (r *reporter) restart(stateWidth int) {
	r.stop()
	r.started = time.Now()
	r.stateWidth = stateWidth
}

// runMigrateReset rolls back every applied migration and, with --up, applies
// them again. On a database with nothing applied, --up alone applies the
// migrations, so resetting a fresh database still builds the schema.
// --dry-run lists both halves without touching the database.
func runMigrateReset(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	p := printext.NewPalette(cmd.Root().Writer)
	report := newReporter(p, migrationStateWidth(string(migration.ProgressRolledBack)))

	migrators, dsn, closeDB, err := openMigrators(ctx, cfg,
		migration.MigratorOptions{Progress: report.progress})
	if err != nil {
		return err
	}
	defer closeDB()

	reapply := cmd.Bool("up")
	seed := cmd.Bool("seed")

	// Seeding writes rows onto the schema the re-apply builds; without the up
	// half there is no rebuilt schema to hold them, so the pair is refused
	// rather than half-served.
	if seed && !reapply {
		return fmt.Errorf("the --seed flag needs --up: seeding writes rows onto the schema the re-apply builds")
	}

	totalApplied := 0
	appliedBySet := make([][]migration.MigrationStatus, len(migrators))
	for i, migrator := range migrators {
		applied, err := migrator.Applied(ctx)
		if err != nil {
			return err
		}
		appliedBySet[i] = applied
		totalApplied += len(applied)
	}

	// Nothing to roll back. Without --up that is the whole answer, because
	// there is no up half to run either.
	if totalApplied == 0 && !reapply {
		return printStatusLine(p, "no applied migrations")
	}

	if cmd.Bool("dry-run") {
		if err = reportTarget(p, dsn); err != nil {
			return err
		}
		// The rollback half consumes the composition in reverse.
		for i := len(migrators) - 1; i >= 0; i-- {
			if err = planReset(ctx, migrators[i], p, appliedBySet[i], reapply); err != nil {
				return err
			}
		}
		if !seed {
			return nil
		}
		return p.Printf("%s%s\n", progressIndent, p.Dim("would seed the database"))
	}

	// A fresh database has no rollback to do, so --up is a plain apply.
	if totalApplied == 0 {
		if err = reportTarget(p, dsn); err != nil {
			return err
		}
		var didApply bool
		for _, migrator := range migrators {
			didApply, err = applyResetUp(ctx, cmd, migrator, p, report)
			if err != nil {
				return err
			}
			if !didApply {
				break
			}
		}
		// A refused apply or a database with nothing pending left the schema
		// where it was, so the seed half has nothing it can rely on.
		if !seed || !didApply {
			return nil
		}
		return seedAfterReset(ctx, cmd, p)
	}

	question := fmt.Sprintf("roll back all %d %s?", totalApplied, printext.Plural(totalApplied, "migration"))
	if reapply {
		question = fmt.Sprintf("roll back all %d %s and re-apply them?",
			totalApplied, printext.Plural(totalApplied, "migration"))
	}
	proceed, err := confirm(p, cmd, terminalCheck(cmd), question)
	if err != nil {
		return err
	}
	if !proceed {
		return printStatusLine(p, "%d %s left applied",
			totalApplied, printext.Plural(totalApplied, "migration"))
	}

	if err = reportTarget(p, dsn); err != nil {
		return err
	}

	rolled := 0
	for i := len(migrators) - 1; i >= 0; i-- {
		if len(appliedBySet[i]) == 0 {
			continue
		}
		results, err := migrators[i].Down(ctx, len(appliedBySet[i]))
		if err != nil {
			if writeErr := report.failed(); writeErr != nil {
				return writeErr
			}
			return err
		}
		rolled += len(results)

		// The rollback emptied the version table down to its sentinel but left
		// the identity sequence where it was, so rewind it before the re-apply
		// records anything. Without this every reset pushes the next id further
		// away.
		if err := migrators[i].ResetIdentity(ctx); err != nil {
			return err
		}
	}
	if err := report.failed(); err != nil {
		return err
	}

	if err := printSummary(p, rolled, "rolled back", "migration", report.elapsed()); err != nil {
		return err
	}

	if !reapply {
		return nil
	}

	// A blank line separates the two halves, so the rollback report and the
	// re-apply report do not read as one run.
	if err := p.Printf("\n"); err != nil {
		return err
	}

	// The re-apply is timed on its own and uses its own state column, so its
	// summary reports the up half rather than the whole reset. The reporter is
	// restarted in place because the migrator holds its progress callback.
	report.restart(migrationStateWidth(string(migration.ProgressApplied)))
	reapplied := 0
	for _, migrator := range migrators {
		applied, err := migrator.Up(ctx)
		if err != nil {
			if writeErr := report.failed(); writeErr != nil {
				return writeErr
			}
			return err
		}
		reapplied += len(applied)
	}
	if err := report.failed(); err != nil {
		return err
	}
	if err := printSummary(p, reapplied, "applied", "migration", report.elapsed()); err != nil {
		return err
	}
	if !seed {
		return nil
	}
	return seedAfterReset(ctx, cmd, p)
}

// applyResetUp runs the --up half on a database with nothing applied. It asks
// the same question migrate:up asks, because it does the same work. The bool
// reports whether migrations were applied: a refusal and a database with
// nothing pending leave the schema where it was, so a --seed half that
// followed would have nothing to stand on.
func applyResetUp(
	ctx context.Context,
	cmd *cli.Command,
	migrator *migration.Migrator,
	p printext.Palette,
	report *reporter,
) (bool, error) {
	pending, err := migrator.Pending(ctx)
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		return false, printStatusLine(p, "no pending migrations")
	}

	proceed, err := confirm(p, cmd, terminalCheck(cmd),
		fmt.Sprintf("apply all %d pending %s?", len(pending), printext.Plural(len(pending), "migration")))
	if err != nil {
		return false, err
	}
	if !proceed {
		return false, printStatusLine(p, "%d pending %s left unapplied",
			len(pending), printext.Plural(len(pending), "migration"))
	}

	// This half only applies, so it needs the apply column width, not the
	// rollback width the reporter was built with for the reset half.
	report.restart(migrationStateWidth(string(migration.ProgressApplied)))

	results, err := migrator.Up(ctx)
	if err != nil {
		if writeErr := report.failed(); writeErr != nil {
			return false, writeErr
		}
		return false, err
	}
	if err := report.failed(); err != nil {
		return false, err
	}
	return true, printSummary(p, len(results), "applied", "migration", report.elapsed())
}

// seedAfterReset runs the seed half of `migrate:reset --up --seed`. It
// re-opens the store the seed command uses — the migrator holds the schema
// half's pinned connection, closed by the reset's own defer — and answers
// through the same seeding body migrate:seed serves, so both commands
// confirm, report, and fail the same way.
func seedAfterReset(ctx context.Context, cmd *cli.Command, p printext.Palette) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}
	if err = requireMigrated(ctx, cfg); err != nil {
		return err
	}

	pool, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Shutdown(context.Background())

	return seedDatabase(ctx, cmd, pool, p, developmentSeeders(cfg))
}

// planReset prints both halves of a reset without touching the database. The
// rollback half is skipped when the database has nothing applied.
func planReset(
	ctx context.Context,
	migrator *migration.Migrator,
	p printext.Palette,
	applied []migration.MigrationStatus,
	reapply bool,
) error {
	if len(applied) > 0 {
		if err := printRollback(p, applied); err != nil {
			return err
		}
	}
	if !reapply {
		return nil
	}
	if len(applied) > 0 {
		if err := p.Printf("\n"); err != nil {
			return err
		}
	}

	pending, err := migrator.Pending(ctx)
	if err != nil {
		return err
	}
	return printPending(p, pending)
}

// runMigrateValidate checks the embedded migrations and reports every problem.
// It never connects to a database, so it works on a machine without Postgres.
func runMigrateValidate(_ context.Context, cmd *cli.Command) error {
	started := time.Now()
	return printValidation(printext.NewPalette(cmd.Root().Writer), migrationCheck(), time.Since(started))
}

// migrationCheck is the seam the tests replace to exercise the failing path,
// which the embedded migrations cannot produce.
var migrationCheck = database.Validate

// printValidation reports the outcome and returns an error when the migrations
// are not valid, so the process exits non-zero in CI.
func printValidation(p printext.Palette, report migration.ValidationReport, elapsed time.Duration) error {
	for _, issue := range report.Issues {
		if err := p.Printf("%s\n", p.Red(issue.String())); err != nil {
			return err
		}
	}
	if !report.OK() {
		return fmt.Errorf("database: %d %s in %d %s",
			len(report.Issues), printext.Plural(len(report.Issues), "problem"),
			report.Checked, printext.Plural(report.Checked, "migration file"))
	}

	return printStatusLine(p, "%s %s",
		p.Green(fmt.Sprintf("%d %s", report.Checked, printext.Plural(report.Checked, "migration file"))),
		p.Dim("valid in "+printext.Duration(elapsed)))
}

var migrateValidateCmd = &cli.Command{
	Name:     "migrate:validate",
	Category: "Development commands",
	Usage:    "Check the migration files",
	Description: `Checks the migrations embedded in this binary for the mistakes goose
rejects while applying: unparsable names, duplicate or missing
versions, malformed annotations, and files without a Down block.

The check reads nothing outside the binary and needs no database, so
it runs before a connection exists. It does not parse SQL.`,
	Action: runMigrateValidate,
}
