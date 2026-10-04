package main

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/database"
	"github.com/riipandi/saka/pkg/printext"
)

// openMigrator opens the single-connection migration handle and loads the
// migrations embedded in the binary. The returned close function releases the
// handle and is always non-nil on success. The DSN is returned so the caller can
// name the database it is about to change.
func openMigrator(
	ctx context.Context,
	cfg config.Config,
	opts database.MigratorOptions,
) (*database.Migrator, string, func(), error) {
	dsn, err := requireDatabaseURL(cfg)
	if err != nil {
		return nil, "", nil, err
	}

	db, err := datastore.OpenMigrationDB(ctx, databaseOptions(ctx, cfg))
	if err != nil {
		return nil, "", nil, err
	}

	migrator, err := database.NewMigrator(ctx, db, opts)
	if err != nil {
		_ = db.Close()
		return nil, "", nil, err
	}
	return migrator, dsn, func() { _ = db.Close() }, nil
}

// databaseOptions is the pool and probe configuration a command opens a handle
// with, so every command reaches the database on the terms the file describes
// and a retry is logged through the process logger.
func databaseOptions(ctx context.Context, cfg config.Config) datastore.PostgresOptions {
	return datastore.PostgresOptions{
		DSN:                  cfg.Database.URL,
		ApplicationName:      config.AppIdentifier,
		MaxConns:             cfg.Database.MaxConns,
		MinConns:             cfg.Database.MinConns,
		MaxConnLifetime:      cfg.Database.MaxConnLifetime,
		MaxConnIdleTime:      cfg.Database.MaxConnIdleTime,
		ConnectTimeout:       cfg.Database.ConnectTimeout,
		ConnectAttempts:      cfg.Database.ConnectAttempts,
		ConnectRetryInterval: cfg.Database.ConnectRetryInterval,
		SearchPath:           cfg.Database.SearchPath,
		Timezone:             cfg.App.Timezone,
		Logger:               probeLogger(ctx),
	}
}

// printPending reports the migrations the database has not applied yet. Each
// line carries a "-" in the time column and no duration, because nothing has run.
func printPending(p printext.Palette, pending []database.MigrationStatus) error {
	if len(pending) == 0 {
		return printStatusLine(p, "no pending migrations")
	}
	rows := make([]migrationRow, 0, len(pending))
	for _, status := range pending {
		rows = append(rows, migrationRow{
			Version: status.Version,
			State:   statePending,
			Name:    status.Name,
		})
	}
	if err := printMigrationRows(p, migrationStateWidth(statePending), rows); err != nil {
		return err
	}
	return printSummary(p, len(pending), "pending", "migration", 0)
}

// printRollback reports the migrations a rollback would consume, newest first.
// The state column reads "rollback", not "rolled back": a dry run describes work
// that has not happened.
func printRollback(p printext.Palette, selected []database.MigrationStatus) error {
	rows := make([]migrationRow, 0, len(selected))
	for _, status := range selected {
		rows = append(rows, migrationRow{
			Version: status.Version,
			State:   stateRollback,
			Name:    status.Name,
		})
	}
	if err := printMigrationRows(p, migrationStateWidth(stateRollback), rows); err != nil {
		return err
	}
	return printSummary(p, len(selected), "to roll back", "migration", 0)
}

// migrationTimestamp is how applied times are rendered. The migration handle
// pins the session to UTC, so the values carry no zone.
const migrationTimestamp = "2006-01-02 15:04:05"

// migrationTimestampWidth is the width that layout always occupies, so a column
// stays aligned whether or not a migration has run.
const migrationTimestampWidth = len(migrationTimestamp)

// printStatus reports every embedded migration, the database version, and when
// the migrations last ran. A time comes from the tstamp column goose writes
// when it records a migration, so it is the moment that migration last ran,
// not when its file changed.
//
// No duration is shown: the recorded time says when a migration ran, not how
// long it took, and this command does not run anything to find out.
func printStatus(p printext.Palette, statuses []database.MigrationStatus, version int64) error {
	applied := 0
	rows := make([]migrationRow, 0, len(statuses))
	for _, status := range statuses {
		state := statePending
		if status.Applied {
			state = string(database.ProgressApplied)
			applied++
		}
		rows = append(rows, migrationRow{
			Version: status.Version,
			State:   state,
			At:      status.AppliedAt,
			Name:    status.Name,
		})
	}

	// One state column for the whole list, sized from the words this list
	// actually uses.
	if err := printMigrationRows(p, migrationStateWidth(statePending, string(database.ProgressApplied)), rows); err != nil {
		return err
	}

	if err := p.Printf("\nversion %05d; %s\n",
		version, p.Paint(versionCountStyle(applied, len(statuses)),
			fmt.Sprintf("%d of %d applied", applied, len(statuses)))); err != nil {
		return err
	}

	last, ok := lastRun(statuses)
	if !ok {
		return printStatusLine(p, "no migrations applied yet")
	}
	return p.Printf("last run %s UTC (%s)\n",
		p.Dim(last.AppliedAt.UTC().Format(migrationTimestamp)), last.Name)
}

// versionCountStyle marks how much of the schema is in place: everything applied
// is a pass, anything less is a state to notice.
func versionCountStyle(applied, total int) printext.Colour {
	if applied == total {
		return printext.Green
	}
	return printext.Yellow
}

// lastRun returns the migration that ran most recently. Comparing timestamps

// lastRun returns the migration that ran most recently. Comparing timestamps
// rather than taking the highest version keeps the answer right when
// --allow-out-of-order applied an older version last.
func lastRun(statuses []database.MigrationStatus) (database.MigrationStatus, bool) {
	var (
		last database.MigrationStatus
		ok   bool
	)
	for _, status := range statuses {
		if !status.Applied {
			continue
		}
		if !ok || status.AppliedAt.After(last.AppliedAt) {
			last, ok = status, true
		}
	}
	return last, ok
}

// confirm asks the question and reports whether the user agreed. A
// non-interactive run agrees without asking so that CI and `task db:migrate`
// never block; an interactive run asks unless --force is passed.
func confirm(p printext.Palette, cmd *cli.Command, interactive bool, question string) (bool, error) {
	if cmd.Bool("force") || !interactive {
		return true, nil
	}

	if err := p.Printf("%s [y/N] ", p.Yellow(question)); err != nil {
		return false, err
	}

	in := cmd.Root().Reader
	if in == nil {
		in = os.Stdin
	}
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && answer == "" {
		if _, writeErr := fmt.Fprintln(p.Writer()); writeErr != nil {
			return false, writeErr
		}
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// runMigrateUp applies the pending migrations. --dry-run lists them instead and
// changes nothing.
func runMigrateUp(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	p := printext.NewPalette(cmd.Root().Writer)
	report := newReporter(p, migrationStateWidth(string(database.ProgressApplied)))

	migrator, dsn, closeDB, err := openMigrator(ctx, cfg,
		database.MigratorOptions{Progress: report.progress})
	if err != nil {
		return err
	}
	defer closeDB()

	if err = reportTarget(p, dsn); err != nil {
		return err
	}

	pending, err := migrator.Pending(ctx)
	if err != nil {
		return err
	}
	if cmd.Bool("dry-run") {
		return printPending(p, pending)
	}

	// --to caps what this run applies; migration version 0 is goose's sentinel,
	// never a target, so an unset --to means "everything".
	target := migrator.HighestVersion()
	if requested := cmd.Uint64("to"); requested > 0 {
		if requested > math.MaxInt64 {
			return fmt.Errorf("database: --to=%d exceeds the maximum migration version", requested)
		}
		target = int64(requested)
	}
	selected := pendingUpTo(pending, target)

	if len(selected) == 0 {
		return printNothingToApply(p, pending, target)
	}

	apply, err := confirm(p, cmd, terminalCheck(cmd),
		fmt.Sprintf("apply %d pending %s?", len(selected), printext.Plural(len(selected), "migration")))
	if err != nil {
		return err
	}
	if !apply {
		return printStatusLine(p, "%d pending %s left unapplied",
			len(selected), printext.Plural(len(selected), "migration"))
	}

	results, err := migrator.UpTo(ctx, target)
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

// runMigrateDown rolls back the most recent migrations. --dry-run lists them
// instead and changes nothing.
func runMigrateDown(ctx context.Context, cmd *cli.Command) error {
	count := cmd.Int("count")
	if count <= 0 {
		return fmt.Errorf("database: --count must be greater than zero, got %d", count)
	}

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

	if err = reportTarget(p, dsn); err != nil {
		return err
	}

	applied, err := migrator.Applied(ctx)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		return printStatusLine(p, "no applied migrations")
	}

	// A count above what the database has rolls back everything, which is the
	// only sensible reading.
	count = min(count, len(applied))
	if cmd.Bool("dry-run") {
		return printRollback(p, applied[:count])
	}

	proceed, err := confirm(p, cmd, terminalCheck(cmd),
		fmt.Sprintf("roll back %d %s?", count, printext.Plural(count, "migration")))
	if err != nil {
		return err
	}
	if !proceed {
		return printStatusLine(p, "%d %s left applied", count, printext.Plural(count, "migration"))
	}

	results, err := migrator.Down(ctx, count)
	if err != nil {
		if writeErr := report.failed(); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err := report.failed(); err != nil {
		return err
	}

	// A rollback deleted rows from the version table but left its identity
	// sequence where it was. Rewinding it after every rollback keeps the recorded
	// ids dense instead of leaving a widening gap behind each cycle.
	if err := migrator.ResetIdentity(ctx); err != nil {
		return err
	}
	return printSummary(p, len(results), "rolled back", "migration", report.elapsed())
}

// runMigrateStatus lists every embedded migration and whether the database has
// it.
func runMigrateStatus(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	migrator, dsn, closeDB, err := openMigrator(ctx, cfg, database.MigratorOptions{})
	if err != nil {
		return err
	}
	defer closeDB()

	p := printext.NewPalette(cmd.Root().Writer)
	if err = reportTarget(p, dsn); err != nil {
		return err
	}

	statuses, err := migrator.Status(ctx)
	if err != nil {
		return err
	}
	version, err := migrator.Version(ctx)
	if err != nil {
		return err
	}
	return printStatus(p, statuses, version)
}

// runMigrateVersion prints the version the database sits on, which is the
// highest applied migration. A database that has never been migrated reports 0.
//
// This command prints the bare number and nothing else: scripts read it
// directly, so a target header would break `VERSION=$(saka migrate:version)`.
// Every other migrate:* command announces the database it works on.
func runMigrateVersion(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	migrator, _, closeDB, err := openMigrator(ctx, cfg, database.MigratorOptions{})
	if err != nil {
		return err
	}
	defer closeDB()

	version, err := migrator.Version(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.Root().Writer, version)
	return err
}

// pendingUpTo keeps the migrations that a run targeting version would apply.
func pendingUpTo(pending []database.MigrationStatus, version int64) []database.MigrationStatus {
	selected := make([]database.MigrationStatus, 0, len(pending))
	for _, status := range pending {
		if status.Version <= version {
			selected = append(selected, status)
		}
	}
	return selected
}

// printNothingToApply explains why a run applied nothing. An empty result under
// --to is not the same as an up-to-date database, and saying "no pending
// migrations" then would be wrong.
func printNothingToApply(p printext.Palette, pending []database.MigrationStatus, target int64) error {
	if len(pending) == 0 {
		return printStatusLine(p, "no pending migrations")
	}
	return printStatusLine(p, "nothing to apply up to version %05d; %s",
		target, p.Yellow(fmt.Sprintf("%d %s pending above it", len(pending), printext.Plural(len(pending), "migration"))))
}

// isTerminal reports whether the confirmation prompt has a user behind it.
// os.ModeCharDevice is not enough: /dev/null is a character device too, and a
// piped run must never block waiting for an answer.
func isTerminal(cmd *cli.Command) bool {
	in := cmd.Root().Reader
	if in == nil {
		in = os.Stdin
	}
	file, ok := in.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

// terminalCheck is the seam the tests replace to exercise both branches of the
// confirmation prompt, which otherwise needs a real terminal.
var terminalCheck = isTerminal

// requireMigrated refuses to seed a database whose schema is not current.
//
// A seeder writes the columns it knows about, so a missing table or a table
// from an older migration would fail halfway through with a database error that
// does not say what to do. Checking the migration state first turns that into
// one instruction, and it catches the case a table check misses: a database
// rolled back below the version a seeder needs.
//
// The check opens the single-connection migration handle rather than reusing
// the pool, because goose reads its version table through database/sql. Asking
// goose is deliberate: "pending" is the engine's own answer, so the check
// cannot drift from what migrate:up would do.
func requireMigrated(ctx context.Context, cfg config.Config) error {
	db, err := datastore.OpenMigrationDB(ctx, databaseOptions(ctx, cfg))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	migrator, err := database.NewMigrator(ctx, db, database.MigratorOptions{})
	if err != nil {
		return err
	}

	pending, err := migrator.Pending(ctx)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("database: %d %s pending; run migrate:up first",
			len(pending), printext.Plural(len(pending), "migration"))
	}
	return nil
}
