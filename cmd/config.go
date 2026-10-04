package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rodaine/table"
	"github.com/urfave/cli/v3"

	fconfig "github.com/riipandi/saka/framework/config"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/pkg/envfile"
	"github.com/riipandi/saka/pkg/printext"
)

// ErrDatabaseURLUnset is returned when no DSN is available. It names the
// variable and the way to supply it, because the message is what a user reads
// when a command refuses to run.
var ErrDatabaseURLUnset = fmt.Errorf("%s is not set; set it in the config file or the environment",
	envfile.DatabaseURL)

// # Resolving the configuration

// configState holds what resolving the configuration produced: the result, and
// the failure when there is none.
//
// Resolution is deferred rather than returned from the root Before. The config
// file is required, and a broken one must not stop a command that bootstraps it:
// `config:generate` has to run before a file exists, and `key:generate` has to
// run before a referenced variable does. The failure is kept and reported by the
// command that actually needs the configuration.
type configState struct {
	cfg config.Config
	err error
}

// configFrom returns the configuration resolved for this run. A command that
// needs one key uses this, so a key it never reads cannot block it.
func configFrom(ctx context.Context) (config.Config, error) {
	state, ok := ctx.Value(configKey{}).(configState)
	if !ok {
		return config.Config{}, errors.New("config: not resolved for this command")
	}
	return state.cfg, state.err
}

// fullConfigFrom returns the configuration after checking it as a whole.
//
// A command that uses many keys calls this instead of configFrom: it turns a
// broken configuration into one error before the command starts, rather than a
// failure at whichever key it happens to reach first.
func fullConfigFrom(ctx context.Context) (config.Config, error) {
	cfg, err := configFrom(ctx)
	if err != nil {
		return config.Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// initConfig resolves the configuration once, before any subcommand runs, and
// puts it on the context. It runs on the root command, so its result is
// available to every action in the chain.
//
// A failure is stored, not returned. The command that needs the configuration
// reports it; a command that bootstraps the file runs without it.
//
// The logger and observer states are installed here too, so a command can log
// or report telemetry without resolving the configuration again. They are only
// *state*: the logger and the providers are built on first use, because the
// bootstrap commands have no configuration to build them from.
func initConfig(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	cfg, err := config.Resolve(cmd)
	ctx = context.WithValue(ctx, configKey{}, configState{cfg: cfg, err: err})
	return installObserver(installLogger(ctx, cmd), cmd), nil
}

// openStore opens the pool a command works through. The caller owns the handle
// and must close it.
//
// Every pool setting comes from cfg, so a command cannot open a pool that
// disagrees with the configuration. The probe retries on the configured terms:
// a command that runs from a container's boot sequence races the database the
// same way serve does.
func openStore(ctx context.Context, cfg config.Config) (*datastore.Postgres, error) {
	store, err := datastore.NewPostgres(ctx, databaseOptions(ctx, cfg))
	if err != nil {
		return nil, err
	}
	return store, nil
}

// probeLogger is the logger a connection probe reports a retry through. It is
// the process logger when the command has one — built here if the command never
// asked for it, because a retry an operator cannot see looks like a hang — and
// nil when the logger itself cannot be built, which leaves the probe silent
// rather than failing a command over its logging.
func probeLogger(ctx context.Context) *slog.Logger {
	log, err := loggerFrom(ctx)
	if err != nil || log == nil {
		return nil
	}
	return log.Slog()
}

// requireDatabaseURL reports the missing DSN with the message every command
// uses, so a caller that only needs the connection string does not repeat it.
func requireDatabaseURL(cfg config.Config) (string, error) {
	if cfg.Database.URL == "" {
		return "", ErrDatabaseURLUnset
	}
	return cfg.Database.URL, nil
}

// dataDir resolves the application data directory from the configuration. It is
// Storage.LocalPath, the one path the local driver writes to; the layer already
// applied the precedence, so there is nothing to re-check here.
func dataDir(cfg config.Config) string {
	if cfg.Storage.LocalPath == "" {
		return config.DefaultDataDir
	}
	return cfg.Storage.LocalPath
}

// configPath reports the file the configuration came from, so a command can name
// it. It is the path the layer resolved, not a second guess at it.
func configPath(cmd *cli.Command) string {
	return fconfig.ConfigPath(
		fconfig.Options{ConfigFile: cmd.String(config.FlagConfigFile)}, os.Environ())
}

// configKey is the context key under which the resolved configuration is stored.
// It is a private struct type so no other package can collide with it.
type configKey struct{}

// # The config commands

// configGenerateCmd writes the file every other command reads. It is the first
// thing a fresh checkout runs, so it needs no configuration of its own.
var configGenerateCmd = &cli.Command{
	Name:  "config:generate",
	Usage: "Write a starter config file",
	Description: `Writes a config file listing every configuration key with its default,
so a fresh checkout has one to edit.

Each secret is written as an env: directive naming the variable that holds it,
never as a literal, so the file is safe to keep. Generate the values with
key:generate, then set DATABASE_URL.

The file is not overwritten. Pass --overwrite to replace it, or --output to
write somewhere else.

With --env-example the command also writes .env.example, a dotenv example
carrying the variables the config file's directives reference, each with its
development default as the value.

config.schema.json is written beside the config file every run: the file
references it from its "$schema" key, so the pair travels together.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "output",
			Usage: "Write to this path instead of the default config file",
		},
		&cli.BoolFlag{
			Name:  "env-example",
			Usage: "Also write .env.example with the development defaults",
		},
		&cli.BoolFlag{
			Name:  "overwrite",
			Usage: "Replace existing config, schema, and example env file",
		},
	},
	Action: runConfigGenerate,
}

// envExampleName is the dotenv file --env-example writes. It is the file
// a developer copies to .env.local and fills in, so the name is the
// convention, not a flag.
const envExampleName = ".env.example"

// configFileMode is the permission of a generated config file. It is readable
// because the file holds directives, never a secret value.
const configFileMode fs.FileMode = 0o644

// runConfigGenerate writes the sample config and reports where it landed.
// The schema is written beside it every run — the file references it from its
// "$schema" key, so a config without its schema loses editor completion — and
// with --env-example the dotenv example joins them. The same --overwrite guard
// covers all three, so a run that would clobber any is told to say so rather
// than silently replacing a file the user edited.
func runConfigGenerate(_ context.Context, cmd *cli.Command) error {
	p := printext.NewPalette(cmd.Root().Writer)

	path := cmd.String("output")
	if path == "" {
		path = configPath(cmd)
	}

	overwrite := cmd.Bool("overwrite")
	if err := mayWriteConfig(path, overwrite); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	schemaPath := filepath.Join(dir, config.SchemaFileName)
	if err := mayWriteConfig(schemaPath, overwrite); err != nil {
		return err
	}

	envPath := ""
	if cmd.Bool("env-example") {
		envPath = filepath.Join(dir, envExampleName)
		if err := mayWriteConfig(envPath, overwrite); err != nil {
			return err
		}
	}

	sample, err := config.Sample()
	if err != nil {
		return err
	}
	schema, err := config.SchemaDoc()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, sample, configFileMode); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := os.WriteFile(schemaPath, schema, configFileMode); err != nil {
		return fmt.Errorf("config: write %s: %w", schemaPath, err)
	}

	if envPath != "" {
		example, err := config.EnvExample()
		if err != nil {
			return err
		}
		if err := os.WriteFile(envPath, example, configFileMode); err != nil {
			return fmt.Errorf("config: write %s: %w", envPath, err)
		}
		return printConfigWritten(p, path, schemaPath, envPath)
	}
	return printConfigWritten(p, path, schemaPath)
}

// mayWriteConfig refuses to replace an existing file unless --overwrite was
// passed. It is a guard rather than a prompt: a config file is edited by hand,
// so losing it silently is worse than being told to pass a flag.
func mayWriteConfig(path string, overwrite bool) error {
	_, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("config: inspect %s: %w", path, err)
	case overwrite:
		return nil
	default:
		return fmt.Errorf("%s already exists; pass --overwrite to replace it", path)
	}
}

// printConfigWritten reports the files and the step that follows them, as the
// same labelled block every other command prints. The schema is always the
// second line: it is written every run, beside the config it describes.
//
// The key count and the size are left out: a generated file always carries every
// key, so both numbers are the same on every run and say nothing about this one.
func printConfigWritten(p printext.Palette, path, schemaPath string, more ...string) error {
	written := append([]string{path, schemaPath}, more...)
	fields := make([]field, 0, len(written))
	for _, w := range written {
		fields = append(fields, field{"written", w})
	}
	if err := printFields(p, fields); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(p.Writer(), "next step: run %s, then set %s\n",
		p.Dim("key:generate --env-file=.env.local"), p.Dim(fconfig.EnvName("database.url"))); err != nil {
		return err
	}
	return printStatusLine(p, "config file ready")
}

var configValidateCmd = &cli.Command{
	Name:  "config:validate",
	Usage: "Check the configuration",
	Description: `Checks the resolved configuration and reports every problem it finds, not
just the first, so a config file with several mistakes is fixed in one pass.

It resolves the same way a running command does: the config file, the
environment its directives reference, and the command-line flags. Pass
--config-file to check a file somewhere else.

Nothing is written, so it is safe to run in CI.`,
	Action: runConfigValidate,
}

// runConfigValidate checks the whole configuration and reports each problem.
func runConfigValidate(ctx context.Context, cmd *cli.Command) error {
	p := printext.NewPalette(cmd.Root().Writer)
	started := time.Now()

	cfg, err := fullConfigFrom(ctx)
	if err != nil {
		return err
	}
	return printConfigValid(p, configPath(cmd), cfg, time.Since(started))
}

// printConfigValid reports the file that was checked, the resolved database
// target, and how many keys resolved.
func printConfigValid(p printext.Palette, path string, cfg config.Config, elapsed time.Duration) error {
	keys := config.Keys()
	if err := p.Printf("%s\n", p.Dim(path)); err != nil {
		return err
	}
	if cfg.Database.URL != "" {
		if err := p.Printf("%s\n", p.Dim("database: "+config.RedactDSN(cfg.Database.URL))); err != nil {
			return err
		}
	}
	return printStatusLine(p, "%s %s",
		p.Green(fmt.Sprintf("%d %s", len(keys), printext.Plural(len(keys), "key"))),
		p.Dim("valid in "+printext.Duration(elapsed)))
}

// # config:print

var configPrintCmd = &cli.Command{
	Name:  "config:print",
	Usage: "Print the resolved configuration",
	Description: `Prints every configuration key with the value it actually resolved to: the
built-in default, the config file, or a command-line flag, whichever won.

A secret is never printed in full. Each one is masked to its first and
last few characters, so a value can be traced to the key that produced
it, and the database connection string is reduced to host:port/database.
Nothing usable is shown, so the output is safe to share.

--source adds the layer each value came from (default, config-file, or flag),
which is how a value that is not what you expected is traced to its source.

Nothing is written, so it is safe to run in CI.`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "source",
			Usage: "Show the layer each value came from",
		},
	},
	Action: runConfigPrint,
}

// runConfigPrint prints the resolved configuration.
//
// It resolves without validating, unlike config:validate. A configuration that
// is incomplete is exactly what a user is trying to inspect, so a missing DSN
// must not stop the report: the empty value is the answer.
func runConfigPrint(ctx context.Context, cmd *cli.Command) error {
	p := printext.NewPalette(cmd.Root().Writer)

	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}
	return printConfigTable(p, cfg, cmd.Bool("source"))
}

// printConfigTable renders the resolved configuration as a table of key and
// value, in key order so two runs are diffable.
//
// The value is read from a masked copy: a secret is shown by its first and last
// few characters, which is enough to trace a value to the key that produced it
// without printing anything usable. Nothing reaches the table straight from cfg,
// so a secret cannot appear even by mistake.
func printConfigTable(p printext.Palette, cfg config.Config, showSource bool) error {
	values := config.Values(cfg.Masked())
	headers := []any{"KEY", "VALUE"}
	if showSource {
		headers = append(headers, "SOURCE")
	}

	tbl := table.New(headers...).
		WithWriter(trimmedWriter{p.Writer()}).
		WithHeaderFormatter(func(format string, vals ...any) string {
			// The padding is trimmed before the header is painted, because a
			// colour reset is invisible but not zero-width: trimming after it
			// would leave the padding inside the escape, where it survives into
			// a redirected file.
			line := strings.TrimRight(fmt.Sprintf(format, vals...), " \n")
			return p.Dim(line) + "\n"
		}).
		WithFirstColumnFormatter(func(format string, vals ...any) string {
			return p.Dim(fmt.Sprintf(format, vals...))
		})

	for _, key := range config.Keys() {
		row := []any{key, renderValue(values[key])}
		if showSource {
			row = append(row, sourceLabel(cfg, key))
		}
		tbl.AddRow(row...)
	}
	tbl.Print()
	return nil
}

// trimmedWriter drops the spaces a table pads a line with.
//
// Every cell is padded to its column width, including the last one, so each line
// ends in whitespace. It is invisible on a terminal but it lands in a redirected
// file and in a diff, so it is removed here rather than left to the reader.
type trimmedWriter struct {
	w io.Writer
}

func (t trimmedWriter) Write(p []byte) (int, error) {
	if _, err := t.w.Write(trimTrailingSpaces(p)); err != nil {
		return 0, err
	}
	// Report the bytes the caller handed over, not the bytes written: a short
	// write would make fmt report an error for output that arrived intact.
	return len(p), nil
}

// trimTrailingSpaces trims every line of p, keeping the line endings.
func trimTrailingSpaces(p []byte) []byte {
	lines := bytes.Split(p, []byte("\n"))
	for i, line := range lines {
		lines[i] = []byte(trimLineEnd(string(line)))
	}
	return bytes.Join(lines, []byte("\n"))
}

// trimLineEnd removes the padding at the end of a line.
//
// A row ends in plain spaces, which this removes. A coloured header ends in the
// colour reset instead, so the formatter trims it before painting: an escape code
// is invisible but not zero-width, and trimming after the reset would leave the
// padding inside the escape.
func trimLineEnd(line string) string {
	return strings.TrimRight(line, " ")
}

// sourceLabel names the layer a value came from. An empty origin is a key no
// source set, which is what an unresolved directive leaves behind.
func sourceLabel(cfg config.Config, key string) string {
	if origin := cfg.Origin(key); origin != "" {
		return origin
	}
	return "-"
}

// renderValue renders a resolved value for the table. A nil is written as
// "null" rather than "<nil>", and an empty string stays visibly empty so a
// reader can tell it apart from a missing row.
func renderValue(value any) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%v", value)
}
