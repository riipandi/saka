//go:build debug

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/riipandi/tango/database"
	"github.com/riipandi/tango/database/seeders"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/printext"
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
	created, err := database.CreateMigration(database.CreateOptions{
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
		p.Dim(fmt.Sprintf("%0*d", database.MigrationPrefixWidth, created.Version)))
}

var migrateResetCmd = &cli.Command{
	Name:     "migrate:reset",
	Category: "Development commands",
	Usage:    "Rollback all migrations",
	Description: `Rolls back every applied migration, newest first. Pass --up to
re-apply them afterwards, which rebuilds the schema from scratch.`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "up",
			Usage: "Re-apply all migrations after the rollback (fresh schema)",
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

	dryRun := cmd.Bool("dry-run")

	// A dry run writes nothing, so it needs no confirmation and no
	// transaction: there is nothing to roll back.
	if dryRun {
		started := time.Now()
		results, err := seeders.Run(ctx, pool, true, seeders.All()...)
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
		results, seedErr = seeders.Run(ctx, tx, false, seeders.All()...)
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
	report := newReporter(p, migrationStateWidth(string(database.ProgressRolledBack)))

	migrator, dsn, closeDB, err := openMigrator(ctx, cfg,
		database.MigratorOptions{Progress: report.progress})
	if err != nil {
		return err
	}
	defer closeDB()

	reapply := cmd.Bool("up")

	applied, err := migrator.Applied(ctx)
	if err != nil {
		return err
	}

	// Nothing to roll back. Without --up that is the whole answer, because
	// there is no up half to run either.
	if len(applied) == 0 && !reapply {
		return printStatusLine(p, "no applied migrations")
	}

	if cmd.Bool("dry-run") {
		if err = reportTarget(p, dsn); err != nil {
			return err
		}
		return planReset(ctx, migrator, p, applied, reapply)
	}

	// A fresh database has no rollback to do, so --up is a plain apply.
	if len(applied) == 0 {
		if err = reportTarget(p, dsn); err != nil {
			return err
		}
		return applyResetUp(ctx, cmd, migrator, p, report)
	}

	question := fmt.Sprintf("roll back all %d %s?", len(applied), printext.Plural(len(applied), "migration"))
	if reapply {
		question = fmt.Sprintf("roll back all %d %s and re-apply them?",
			len(applied), printext.Plural(len(applied), "migration"))
	}
	proceed, err := confirm(p, cmd, terminalCheck(cmd), question)
	if err != nil {
		return err
	}
	if !proceed {
		return printStatusLine(p, "%d %s left applied",
			len(applied), printext.Plural(len(applied), "migration"))
	}

	if err = reportTarget(p, dsn); err != nil {
		return err
	}

	rolled, err := migrator.Down(ctx, len(applied))
	if err != nil {
		if writeErr := report.failed(); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err := report.failed(); err != nil {
		return err
	}

	// The rollback emptied the version table down to its sentinel but left the
	// identity sequence where it was, so rewind it before the re-apply records
	// anything. Without this every reset pushes the next id further away.
	if err := migrator.ResetIdentity(ctx); err != nil {
		return err
	}

	if err := printSummary(p, len(rolled), "rolled back", "migration", report.elapsed()); err != nil {
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
	report.restart(migrationStateWidth(string(database.ProgressApplied)))
	reapplied, err := migrator.Up(ctx)
	if err != nil {
		if writeErr := report.failed(); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err := report.failed(); err != nil {
		return err
	}
	return printSummary(p, len(reapplied), "applied", "migration", report.elapsed())
}

// applyResetUp runs the --up half on a database with nothing applied. It asks
// the same question migrate:up asks, because it does the same work.
func applyResetUp(
	ctx context.Context,
	cmd *cli.Command,
	migrator *database.Migrator,
	p printext.Palette,
	report *reporter,
) error {
	pending, err := migrator.Pending(ctx)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return printStatusLine(p, "no pending migrations")
	}

	proceed, err := confirm(p, cmd, terminalCheck(cmd),
		fmt.Sprintf("apply all %d pending %s?", len(pending), printext.Plural(len(pending), "migration")))
	if err != nil {
		return err
	}
	if !proceed {
		return printStatusLine(p, "%d pending %s left unapplied",
			len(pending), printext.Plural(len(pending), "migration"))
	}

	// This half only applies, so it needs the apply column width, not the
	// rollback width the reporter was built with for the reset half.
	report.restart(migrationStateWidth(string(database.ProgressApplied)))

	results, err := migrator.Up(ctx)
	if err != nil {
		if writeErr := report.failed(); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err := report.failed(); err != nil {
		return err
	}
	return printSummary(p, len(results), "applied", "migration", report.elapsed())
}

// planReset prints both halves of a reset without touching the database. The
// rollback half is skipped when the database has nothing applied.
func planReset(
	ctx context.Context,
	migrator *database.Migrator,
	p printext.Palette,
	applied []database.MigrationStatus,
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
func printValidation(p printext.Palette, report database.ValidationReport, elapsed time.Duration) error {
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
