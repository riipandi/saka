package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/dustin/go-humanize"

	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/database"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/pkg/printext"
)

// backupDir is where a dump lands when --output is not given. It sits inside the
// data directory, which git already ignores and compose already mounts.
const backupDir = "backup"

// backupFilePattern names a generated dump. Minute granularity keeps the name
// readable, and UTC keeps it consistent with every other timestamp the CLI
// prints.
const backupFilePattern = "saka-20060102_1504.sql"

var dbExportCmd = &cli.Command{
	Name:     "db:export",
	Category: "Database operation",
	Usage:    "Export the application database to a SQL file",
	Description: `Writes a plain SQL dump: the schema as DDL, then the rows as one COPY
block per table. The file is text, so it can be read, diffed, and
reviewed.

The default output is storage/backup/saka-<YYYYMMDD_hhmm>.sql, in
UTC. Pass --output to write somewhere else. A generated name that
already exists is not replaced unless --overwrite is given, so a
second export inside the same minute cannot lose the first dump.

--compression picks the container the dump is stored in: none (the
default), gzip, zlib, or zip. The dump is written plain first and
compressed after it is complete, and the suffix is added to the name
(.sql.gz, .sql.zz, .sql.zip).

db:import reads the file back. It detects the container from the
file's own bytes, so a renamed dump still loads, and it loads the
COPY blocks while ignoring the DDL. The target database gets its
schema from migrate:up, which keeps the migrations the single source
of truth for the schema.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "output",
			Usage: "Write the dump to this path",
		},
		&cli.StringFlag{
			Name:  "compression",
			Usage: "Compress the dump: none, gzip, zlib, or zip",
			Value: string(database.CompressionNone),
		},
		&cli.BoolFlag{
			Name:  "schema-only",
			Usage: "Export the schema without any rows",
		},
		&cli.BoolFlag{
			Name:  "data-only",
			Usage: "Export the rows without any DDL",
		},
		&cli.BoolFlag{
			Name:  "overwrite",
			Usage: "Replace the generated dump file when it already exists",
		},
	},
	Action: runDBExport,
}

var dbImportCmd = &cli.Command{
	Name:      "db:import",
	Category:  "Database operation",
	Usage:     "Import a database backup file",
	ArgsUsage: "<file>",
	Description: `Loads the COPY blocks of a dump written by db:export into the
application database. DDL in the file is ignored: run migrate:up
first, so the schema comes from the migrations and nothing else.

The container is detected from the file's own bytes, so a dump named
.sql.gz, .sql.zz, .sql.zip, or plain .sql all load the same way. A
format this tool does not write, such as bzip2 or zstd, is refused by
name rather than loaded as garbage.

A restore is destructive, so it asks before it starts: it replaces
rows in the tables the dump carries, and --truncate empties every
table first. Pass --force to skip the prompt, or --dry-run to report
what would be loaded without touching the database. The whole load
runs in one transaction, so a failure half way leaves the database
untouched.`,
	Arguments: []cli.Argument{
		&cli.StringArg{
			Name:      "file",
			UsageText: "backup file to load",
			Required:  true,
		},
	},
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "truncate",
			Usage: "Empty every table in the dump before loading it",
		},
		&cli.BoolFlag{
			Name:  "force",
			Usage: "Skip the confirmation prompt",
		},
		&cli.BoolFlag{
			Name:  "dry-run",
			Usage: "Report what would be loaded without changing the database",
		},
	},
	Action: runDBImport,
}

// defaultExportPath is where a dump is written when --output is not given.
func defaultExportPath(cfg config.Config) string {
	stamp := time.Now().UTC().Format(backupFilePattern)
	return filepath.Join(dataDir(cfg), backupDir, stamp)
}

// runDBExport writes a SQL dump of the application schemas.
//
// The dump is a plain SQL file: DDL first, then one COPY block per table. It is
// meant to be read, diffed, and reviewed, which is why the format is not a
// binary archive.
func runDBExport(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	p := printext.NewPalette(cmd.Root().Writer)

	opts := database.DumpOptions{
		SchemaOnly: cmd.Bool("schema-only"),
		DataOnly:   cmd.Bool("data-only"),
	}
	if opts.SchemaOnly && opts.DataOnly {
		return fmt.Errorf("--schema-only and --data-only are mutually exclusive")
	}

	format, err := database.ParseCompression(cmd.String("compression"))
	if err != nil {
		return err
	}

	dsn, err := requireDatabaseURL(cfg)
	if err != nil {
		return err
	}

	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Shutdown(context.Background())

	if err = printDatabase(p, dsn); err != nil {
		return err
	}

	path, generated := cmd.String("output"), false
	if path == "" {
		path, generated = defaultExportPath(cfg), true
	}
	// The dump is written plain and compressed after it is complete, so the file
	// that finally exists is the compressed one. The overwrite guard and the
	// consent prompt are therefore about that final name, not the plain one.
	target := path + format.Ext()

	file, err := createExportFile(path, generated, cmd.Bool("overwrite"), target)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	started := time.Now()
	progress := withSpinner(p.Writer())
	stats, err := database.NewExporter(store, opts).Dump(ctx, file, progress.step)
	progress.finish()
	if err != nil {
		return err
	}
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("database: write dump: %w", closeErr)
	}

	written := path
	if format.IsCompressed() {
		written, err = compressDump(p, path, format)
		if err != nil {
			return err
		}
	}

	return printExportSummary(p, written, stats, time.Since(started))
}

// compressDump compresses a finished dump in place and returns the file it
// wrote.
//
// The plain file is removed only after the compressed one is complete, so an
// interrupted run leaves a usable dump behind instead of nothing.
func compressDump(p printext.Palette, path string, format database.Compression) (string, error) {
	target := path + format.Ext()

	spin := newSpinner(p.Writer())
	if spin != nil {
		spin.Suffix = " compressing"
		spin.Start()
	}
	err := database.CompressFile(path, target, format, filepath.Base(path))
	if spin != nil {
		spin.Stop()
	}
	if err != nil {
		// The plain dump is still there and still valid, so the failure is
		// reported against it rather than discarding the work.
		return "", fmt.Errorf("%w (plain dump kept at %s)", err, path)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("database: remove uncompressed dump: %w", err)
	}
	return target, nil
}

// ErrDumpFileExists is returned when a dump would overwrite an existing file.
var ErrDumpFileExists = errors.New("database: dump file already exists")

// dumpFileMode is the permission a dump is created with: owner read and write,
// nothing for anyone else.
//
// A dump is the whole database in one file — every row of every table, which
// includes the credential columns and the personal data the schema holds. A
// world-readable dump is the same exposure as a world-readable password file,
// so it is created owner-only rather than at whatever the umask makes of
// 0o666, the rule pkg/envfile already applies to a file of secrets.
const dumpFileMode fs.FileMode = 0o600

// createExportFile opens the dump for writing.
//
// A generated path gets its directory created, because the default location
// lives under the data directory and that may not exist on a fresh checkout or
// after a cleanup. The directory is created whether or not the file is replaced:
// a first run with --overwrite must not fail for a reason that has nothing to do
// with overwriting.
//
// The name carries a timestamp to the minute, so a second export within the same
// minute lands on the same path. Rather than lose the first dump, the run stops
// and says so, unless --overwrite asks for it.
//
// final is the name the run will leave behind once the dump is compressed, which
// is what an existing file has to be compared against. The guard is on that
// name, not on the temporary plain one: with --compression set, the plain path is
// an intermediate that is removed again, so a stale plain file must not stop a
// run whose compressed output does not exist yet.
//
// A path the user typed is used as it is: it is truncated like any other file
// the user named, and its directory is not created, so a typo stays visible.
// Its permission is not the user's to choose here — a dump is a file of secrets
// whoever named it.
func createExportFile(path string, generated, force bool, final string) (*os.File, error) {
	if !generated {
		return openDump(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("database: create backup directory: %w", err)
	}

	if force {
		return openDump(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	}

	if _, err := os.Stat(final); err == nil {
		return nil, fmt.Errorf("%w: %s; pass --output to pick another name, or --overwrite to replace it",
			ErrDumpFileExists, final)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("database: check dump file: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, dumpFileMode)
	if errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("%w: %s; pass --output to pick another name, or --overwrite to replace it",
			ErrDumpFileExists, path)
	}
	if err != nil {
		return nil, fmt.Errorf("database: create dump file: %w", err)
	}
	return file, nil
}

// openDump opens an existing-or-new dump for writing and applies the dump
// permission to it.
//
// The chmod is what makes the permission hold on a replacement. O_CREATE's mode
// applies only when the file is created, so truncating a dump that was left
// world-readable — by an older version, by a copy, by an editor — would keep it
// that way. A dump is a file of secrets however it got there.
func openDump(path string, flag int) (*os.File, error) {
	file, err := os.OpenFile(path, flag, dumpFileMode)
	if err != nil {
		return nil, fmt.Errorf("database: create dump file: %w", err)
	}
	if err := file.Chmod(dumpFileMode); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("database: set dump permissions: %w", err)
	}
	return file, nil
}

// printExportSummary reports what was written and where, as a small aligned
// block. One labelled line per fact beats a sentence: a reader scans the labels
// instead of parsing prose, and a value can be read without the words around it.
//
// The path is the answer the reader is looking for, so it is left in the default
// colour against its dim label.
func printExportSummary(
	p printext.Palette,
	path string,
	stats database.DumpStats,
	elapsed time.Duration,
) error {
	fields := []field{{"written", path}}
	if size := fileSize(path); size != "" {
		fields = append(fields, field{"size", p.Green(size)})
	}
	if stats.Statements > 0 {
		fields = append(fields, field{"schema", p.Green(fmt.Sprintf("%d %s",
			stats.Statements, printext.Plural(stats.Statements, "statement")))})
	}
	if stats.Tables > 0 {
		fields = append(fields, field{"data", p.Green(fmt.Sprintf("%d %s, %d %s",
			stats.Tables, printext.Plural(stats.Tables, "table"),
			stats.Rows, printext.Plural(int(stats.Rows), "row")))})
	}
	fields = append(fields, field{"duration", p.Dim(printext.Duration(elapsed))})
	return printFields(p, fields)
}

// fileSize renders the size of a written dump. An unreadable path yields an
// empty string rather than an error: the file was just written and named in the
// line above, so a stat that fails is not worth failing the command over.
//
// A negative size cannot occur for a regular file, but the conversion is guarded
// anyway rather than casting a signed value straight to unsigned.
func fileSize(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if size := info.Size(); size >= 0 {
		return humanize.Bytes(uint64(size))
	}
	return ""
}

// printImportSummary reports what was loaded and from where.
func printImportSummary(p printext.Palette, path string, stats database.DumpStats, elapsed time.Duration) error {
	fields := []field{{"loaded", path}}
	if size := fileSize(path); size != "" {
		fields = append(fields, field{"size", p.Green(size)})
	}
	fields = append(fields,
		field{"data", p.Green(fmt.Sprintf("%d %s, %d %s",
			stats.Tables, printext.Plural(stats.Tables, "table"),
			stats.Rows, printext.Plural(int(stats.Rows), "row")))},
		field{"duration", p.Dim(printext.Duration(elapsed))})
	return printFields(p, fields)
}

// field is one labelled value in a report.
type field struct {
	label string
	value string
}

// reportLabelWidth is the minimum width every label in a report block is padded
// to. A block can be printed in two parts — the target before the work, the
// result after it — and the two calls must agree on the column, so the width is
// pinned rather than derived from whichever fields a call happens to hold.
const reportLabelWidth = len("database")

// printFields writes labelled values with their labels padded to one width, so
// the values line up in a column and the block scans vertically.
//
// The label is padded before it is coloured: an escape code is invisible but not
// zero-width to fmt, so padding a painted string would break the column.
func printFields(p printext.Palette, fields []field) error {
	width := reportLabelWidth
	for _, f := range fields {
		width = max(width, len(f.label))
	}
	for _, f := range fields {
		label := printext.PadRight(f.label+":", width+1)
		if err := p.Printf("%s  %s\n", p.Dim(label), f.value); err != nil {
			return err
		}
	}
	return nil
}

// printDatabase writes the database a command works on as the opening line of
// its report block.
//
// It is printed before any work starts, so a run against the wrong server is
// visible immediately instead of after the fact. The credentials are never
// printed, and a DSN that cannot be parsed prints no line at all.
func printDatabase(p printext.Palette, dsn string) error {
	target := config.RedactDSN(dsn)
	if target == "" {
		return nil
	}
	return printFields(p, []field{{label: "database", value: p.Dim(target)}})
}

// runDBImport loads a dump into the application database.
//
// The file is a required positional argument rather than a flag: a restore always
// has a source, and naming it on the command line is what a reader expects.
func runDBImport(ctx context.Context, cmd *cli.Command) error {
	p := printext.NewPalette(cmd.Root().Writer)
	path := cmd.StringArg("file")

	// The container is detected before anything else, so an unsupported file is
	// refused by name and a dry run never needs a database.
	reader, format, err := database.OpenDump(path)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}

	dryRun := cmd.Bool("dry-run")
	truncate := cmd.Bool("truncate")

	// A dry run reads the file and stops. It opens no database, so it cannot
	// change one, and it is the way to see the size of a restore before
	// agreeing to it.
	if dryRun {
		if err = printDatabaseHeader(p, cfg, format); err != nil {
			return err
		}
		var stats database.DumpStats
		stats, err = database.InspectDump(reader)
		if err != nil {
			return err
		}
		return printDryRunSummary(p, path, stats, truncate)
	}

	dsn, err := requireDatabaseURL(cfg)
	if err != nil {
		return err
	}

	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Shutdown(context.Background())

	if err = printDatabase(p, dsn); err != nil {
		return err
	}
	if format.IsCompressed() {
		if err = printFields(p, []field{{label: "format", value: p.Dim(string(format))}}); err != nil {
			return err
		}
	}

	// A restore is destructive: it writes rows into tables that may already hold
	// data, and --truncate empties every table first. It is confirmed like every
	// other destructive command, and --force is what a script passes.
	if !cmd.Bool("force") {
		question := "replace the rows in the tables this dump carries?"
		if truncate {
			question = "empty every table in the dump before loading it?"
		}
		proceed, promptErr := confirm(p, cmd, terminalCheck(cmd), question)
		if promptErr != nil {
			return promptErr
		}
		if !proceed {
			return p.Printf("import cancelled\n")
		}
	}

	started := time.Now()
	progress := withSpinner(p.Writer())
	stats, err := database.NewRestorer(store, truncate).Restore(ctx, reader, progress.step)
	progress.finish()
	if err != nil {
		return err
	}

	return printImportSummary(p, path, stats, time.Since(started))
}

// printDatabaseHeader writes the target line for a dry run, which never opens a
// connection. The database name comes from the configuration, so the run still
// says where a real restore would land.
func printDatabaseHeader(p printext.Palette, cfg config.Config, format database.Compression) error {
	dsn, err := requireDatabaseURL(cfg)
	if err != nil {
		return err
	}
	if err := printDatabase(p, dsn); err != nil {
		return err
	}
	if format.IsCompressed() {
		return printFields(p, []field{{label: "format", value: p.Dim(string(format))}})
	}
	return nil
}

// printDryRunSummary reports what a restore would load, and says nothing was
// loaded. The work is the answer, so it is reported with the same labels a real
// run uses. The format is already named on the header line, so it is not
// repeated here.
func printDryRunSummary(
	p printext.Palette,
	path string,
	stats database.DumpStats,
	truncate bool,
) error {
	fields := []field{{"loaded", p.Dim(path)}}
	fields = append(fields, field{"data", p.Green(fmt.Sprintf("%d %s, %d %s",
		stats.Tables, printext.Plural(stats.Tables, "table"),
		stats.Rows, printext.Plural(int(stats.Rows), "row")))})
	if truncate {
		fields = append(fields, field{"truncate", p.Yellow("yes")})
	}
	if err := printFields(p, fields); err != nil {
		return err
	}
	return printStatusLine(p, "nothing loaded (dry run)")
}
