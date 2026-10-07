package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	fconfig "github.com/riipandi/saka/framework/config"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/pkg/envfile"
)

// testRoot builds the root command a test runs a subcommand through.
//
// It mirrors the real root command: the config flags are registered, and
// initConfig runs as Before, so a test exercises the same precedence chain the
// binary does. Only the flag names differ from cli_debug.go, which registers
// exactly these two.
func testRoot(out *bytes.Buffer, stdin string, commands ...*cli.Command) *cli.Command {
	return &cli.Command{
		Name:     "saka",
		Writer:   out,
		Reader:   strings.NewReader(stdin),
		Commands: commands,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: config.FlagEnvFile, Usage: "Load environment variables from a file"},
			&cli.StringFlag{Name: config.FlagConfigFile, Usage: "Load configuration from a JSON file"},
		},
		// cli.Exit calls os.Exit, so the error is trapped instead.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Before:         initConfig,
	}
}

// useConfig points the config layer at a file body for this test.
//
// The file is the source of truth, so every command test needs one. It is named
// through CONFIG_FILE rather than --config-file so a test does not have to know
// where the flag belongs in the argument list.
func useConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "app.config.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	t.Setenv(fconfig.FileEnv, path)
	return path
}

// configFor writes the config file a command test resolves against. The DSN is
// left to the environment, so a test points at its own database by setting
// DATABASE_URL; dataDir, when not empty, is written into the file because the
// data directory has no flag and no variable of its own.
//
// Only database.url is declared, because no command test validates the whole
// configuration: a command that needs one key must not be blocked by a key it
// never reads, and this is the file that proves it.
func configFor(t *testing.T, dataDir string) {
	t.Helper()

	body := `{"database": {"url": "env:DATABASE_URL"}`
	if dataDir != "" {
		body += `, "storage": {"local_path": ` + strconv.Quote(dataDir) + `}`
	}
	useConfig(t, body+"}")
}

// testSecret is a 64-character hex string, the shape of AUTH_SECRET_KEY.
const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// # config:generate, config:validate, and config:print

// runConfigGenerate executes config:generate with args and returns stdout.
func runConfigGenerateCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	root := testRoot(&out, "", configGenerateCmd)
	err := root.Run(context.Background(), append([]string{"saka", configGenerateCmd.Name}, args...))
	return out.String(), err
}

// runConfigValidate executes config:validate with args and returns stdout.
func runConfigValidateCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	root := testRoot(&out, "", configValidateCmd)
	err := root.Run(context.Background(), append([]string{"saka", configValidateCmd.Name}, args...))
	return out.String(), err
}

// runConfigPrint executes config:print with args and returns stdout.
func runConfigPrintCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	root := testRoot(&out, "", configPrintCmd)
	err := root.Run(context.Background(), append([]string{"saka", configPrintCmd.Name}, args...))
	return out.String(), err
}

func TestConfigGenerateWritesEveryKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.config.json")

	out, err := runConfigGenerateCmd(t, "--output="+path)
	require.NoError(t, err)
	assert.Contains(t, out, "written:   "+path)
	assert.Contains(t, out, "next step: run key:generate")
	assert.Contains(t, out, "status: config file ready")

	written, err := os.ReadFile(path)
	require.NoError(t, err)

	// The file must load back as a valid configuration, which is the only
	// statement that matters about a generated file.
	cfg, err := config.Load(config.Options{
		ConfigFile: path,
		Environ: []string{
			"DATABASE_URL=postgresql://user:pass@localhost:5432/saka?sslmode=disable",
			"AUTH_SECRET_KEY=" + testSecret,
			"APP_SECRET_KEY=" + testSecret,
			"PUBLIC_BASE_URL=http://localhost:3000",
		},
	})
	require.NoError(t, err)
	require.NoError(t, cfg.Validate(), string(written))
}

func TestConfigGenerateNeverWritesALiteralSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.config.json")
	_, err := runConfigGenerateCmd(t, "--output="+path)
	require.NoError(t, err)

	written, err := os.ReadFile(path)
	require.NoError(t, err)

	// Each secret must be a directive. A literal here would be committed by
	// accident, because the file is meant to be readable.
	for _, key := range []string{"app.secret_key", "auth.secret_key", "database.url"} {
		assert.Contains(t, string(written), `"`+lastSegment(key)+`": "env:`+fconfig.EnvName(key)+`"`)
	}
}

// lastSegment returns the field name of a config key.
func lastSegment(key string) string {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '.' {
			return key[i+1:]
		}
	}
	return key
}

func TestConfigGenerateRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.config.json")
	require.NoError(t, os.WriteFile(path, []byte("keep me"), 0o600))

	_, err := runConfigGenerateCmd(t, "--output="+path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--overwrite")

	// The existing file must be untouched: losing a hand-edited config to a
	// mistyped command is the failure this guard exists for.
	kept, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(kept))
}

func TestConfigGenerateOverwriteReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.config.json")
	require.NoError(t, os.WriteFile(path, []byte("replace me"), 0o600))

	_, err := runConfigGenerateCmd(t, "--output="+path, "--overwrite")
	require.NoError(t, err)

	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(written), `"port": "env:SERVER_PORT"`)
	assert.NotContains(t, string(written), "replace me")
}

func TestConfigGenerateDefaultsToTheConfigFile(t *testing.T) {
	// With no --output the file lands where the loader looks for it, which is
	// what makes the command the first step of a fresh checkout.
	dir := t.TempDir()
	t.Setenv(fconfig.FileEnv, filepath.Join(dir, fconfig.DefaultConfigFile))

	out, err := runConfigGenerateCmd(t)
	require.NoError(t, err)
	assert.Contains(t, out, fconfig.DefaultConfigFile)

	_, statErr := os.Stat(filepath.Join(dir, fconfig.DefaultConfigFile))
	require.NoError(t, statErr)
}

func TestConfigValidateAcceptsAGoodFile(t *testing.T) {
	useConfig(t, `{
	  "database": {"url": "env:DATABASE_URL"},
	  "auth": {"secret_key": "env:AUTH_SECRET_KEY"}
	}`)
	t.Setenv("DATABASE_URL", "postgresql://user:pass@localhost:5432/saka?sslmode=disable")
	t.Setenv("AUTH_SECRET_KEY", testSecret)
	t.Setenv("PUBLIC_BASE_URL", "http://localhost:3000")

	out, err := runConfigValidateCmd(t)
	require.NoError(t, err)
	assert.Contains(t, out, "valid")
	assert.Contains(t, out, "status:")
	assert.Contains(t, out, "localhost:5432/saka")
}

func TestConfigValidateReportsEveryProblem(t *testing.T) {
	// Two mistakes at once: both must be named, so one run fixes the file.
	useConfig(t, `{
	  "database": {"url": "env:DATABASE_URL"},
	  "server": {"port": 0},
	  "log": {"level": "loud"}
	}`)
	t.Setenv("DATABASE_URL", "postgresql://user:pass@localhost:5432/saka?sslmode=disable")

	_, err := runConfigValidateCmd(t)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "server.port")
	assert.Contains(t, err.Error(), "log.level")
}

func TestConfigValidateReportsAnUnresolvedVariable(t *testing.T) {
	useConfig(t, `{"database": {"url": "env:NOT_SET_ANYWHERE"}}`)

	_, err := runConfigValidateCmd(t)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "NOT_SET_ANYWHERE")
}

func TestConfigValidateReportsAMissingFile(t *testing.T) {
	t.Setenv(fconfig.FileEnv, filepath.Join(t.TempDir(), "absent.json"))

	_, err := runConfigValidateCmd(t)
	require.ErrorIs(t, err, fconfig.ErrNoConfigFile)
}

func TestConfigValidateUsesTheConfigFileFlag(t *testing.T) {
	// --config-file names the file to check, so a file outside the working
	// directory can be validated before it is deployed.
	path := writeTestConfigFile(t, `{
	  "database": {"url": "env:DATABASE_URL"},
	  "auth": {"secret_key": "env:AUTH_SECRET_KEY"}
	}`)
	t.Setenv("DATABASE_URL", "postgresql://user:pass@localhost:5432/saka?sslmode=disable")
	t.Setenv("AUTH_SECRET_KEY", testSecret)
	t.Setenv("PUBLIC_BASE_URL", "http://localhost:3000")

	out, err := runConfigValidateCmd(t, "--config-file="+path)
	require.NoError(t, err)
	assert.Contains(t, out, path)
}

// writeTestConfigFile writes a config file and returns its path.
func writeTestConfigFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "app.config.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// # config:print

// printedValue reads the VALUE column of one key out of a rendered table. An
// empty value has no trailing space, so a line that is only the key counts as an
// empty value rather than as a missing row.
func printedValue(t *testing.T, out, key string) string {
	t.Helper()

	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimRight(line, " ")
		name, rest, _ := strings.Cut(line, " ")
		if name != key {
			continue
		}
		return strings.TrimSpace(rest)
	}
	t.Fatalf("%s is not in the table:\n%s", key, out)
	return ""
}

func TestConfigPrintShowsResolvedValues(t *testing.T) {
	useConfig(t, `{"database": {"url": "env:DATABASE_URL"}, "server": {"port": 7777}}`)
	t.Setenv(envfile.DatabaseURL, "postgresql://user:pass@localhost:5432/saka?sslmode=disable")

	out, err := runConfigPrintCmd(t)
	require.NoError(t, err)

	assert.Contains(t, out, "KEY")
	assert.Contains(t, out, "VALUE")

	// A value the file set and one that fell back to the default, so the table
	// is shown to report the resolved state rather than only the file.
	assert.Equal(t, "7777", printedValue(t, out, "server.port"))
	assert.Equal(t, "development", printedValue(t, out, "app.mode"))
	assert.Equal(t, "587", printedValue(t, out, "mailer.smtp_port"))
}

func TestConfigPrintMasksSecrets(t *testing.T) {
	// The point of the command: a report that can be shared. A secret is shown
	// by its ends so it can be traced to a key, never in full.
	useConfig(t, `{
		"app": {"secret_key": "env:APP_SECRET_KEY"},
		"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
		"database": {"url": "env:DATABASE_URL"},
		"mailer": {"smtp_username": "bot", "smtp_password": "hunter2"}
	}`)
	t.Setenv("APP_SECRET_KEY", testSecret)
	t.Setenv("AUTH_SECRET_KEY", testSecret)
	t.Setenv(envfile.DatabaseURL, "postgresql://user:sup3rs3cret@localhost:5432/saka?sslmode=disable")

	out, err := runConfigPrintCmd(t)
	require.NoError(t, err)

	assert.NotContains(t, out, testSecret, "a secret value must never be printed in full")
	assert.NotContains(t, out, "sup3rs3cret", "a DSN password must never be printed")
	assert.NotContains(t, out, "hunter2", "an SMTP password must never be printed")

	// A long secret keeps its ends, which is what makes two keys tellable apart.
	assert.Equal(t, "0123****cdef", printedValue(t, out, "app.secret_key"))
	assert.Equal(t, "0123****cdef", printedValue(t, out, "auth.secret_key"))

	// A value too short to mask safely is hidden completely: keeping eight of
	// its twelve characters would leave most of the password readable.
	assert.Equal(t, "[redacted]", printedValue(t, out, "mailer.smtp_password"))

	// The DSN is reduced rather than masked, so the target is still readable.
	assert.Equal(t, "localhost:5432/saka", printedValue(t, out, "database.url"))

	// A username is not a secret, so it stays readable.
	assert.Equal(t, "bot", printedValue(t, out, "mailer.smtp_username"))
}

func TestConfigPrintMasksTheAuthSecret(t *testing.T) {
	// The auth secret is the case the masking exists for: it is the signing
	// and sealing key material, so a print must never echo it.
	const secret = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"
	useConfig(t, `{
		"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
		"database": {"url": "env:DATABASE_URL"}
	}`)
	t.Setenv("AUTH_SECRET_KEY", secret)
	t.Setenv(envfile.DatabaseURL, "postgresql://user:pass@localhost:5432/saka?sslmode=disable")

	out, err := runConfigPrintCmd(t)
	require.NoError(t, err)

	masked := printedValue(t, out, "auth.secret_key")
	assert.Equal(t, secret[:4]+"****"+secret[len(secret)-4:], masked)
	assert.NotContains(t, out, secret)
}

func TestConfigPrintMasksAnEmptySecretAsEmpty(t *testing.T) {
	// An unset secret must read as unset, not as a masked value: the difference
	// is the whole answer when a key is missing.
	useConfig(t, `{"database": {"url": "env:DATABASE_URL"}}`)
	t.Setenv(envfile.DatabaseURL, "postgresql://user:pass@localhost:5432/saka?sslmode=disable")

	out, err := runConfigPrintCmd(t)
	require.NoError(t, err)

	assert.Equal(t, "", printedValue(t, out, "app.secret_key"))
	assert.Equal(t, "", printedValue(t, out, "auth.secret_key"))
}

func TestConfigPrintCoversEveryKey(t *testing.T) {
	useConfig(t, `{"database": {"url": "env:DATABASE_URL"}}`)
	t.Setenv(envfile.DatabaseURL, "postgresql://user:pass@localhost:5432/saka?sslmode=disable")

	out, err := runConfigPrintCmd(t)
	require.NoError(t, err)

	for _, key := range config.Keys() {
		assert.Contains(t, out, key, "every key must be printed")
	}
}

func TestConfigPrintSourceFlag(t *testing.T) {
	useConfig(t, `{"database": {"url": "env:DATABASE_URL"}, "server": {"port": 7777}}`)
	t.Setenv(envfile.DatabaseURL, "postgresql://user:pass@localhost:5432/saka?sslmode=disable")

	out, err := runConfigPrintCmd(t, "--source")
	require.NoError(t, err)

	assert.Contains(t, out, "SOURCE")
	assert.Contains(t, out, "config-file")
	assert.Contains(t, out, "default")
}

func TestConfigPrintRunsWithoutValidation(t *testing.T) {
	// A configuration a user is inspecting is often one that does not validate
	// yet, so an unset DSN must not stop the report: the empty value is the
	// answer. This is what separates config:print from config:validate.
	t.Chdir(t.TempDir())
	useConfig(t, `{"server": {"port": 7777}}`)

	out, err := runConfigPrintCmd(t)
	require.NoError(t, err)

	assert.Equal(t, "7777", printedValue(t, out, "server.port"))
	assert.Equal(t, "", printedValue(t, out, "database.url"))
}

func TestConfigPrintHasNoTrailingWhitespace(t *testing.T) {
	// Every cell is padded to its column width, including the last one, so a
	// line would end in spaces. Invisible on a terminal, but it lands in a
	// redirected file and in a diff.
	useConfig(t, `{"database": {"url": "env:DATABASE_URL"}}`)
	t.Setenv(envfile.DatabaseURL, "postgresql://user:pass@localhost:5432/saka?sslmode=disable")

	out, err := runConfigPrintCmd(t)
	require.NoError(t, err)

	for line := range strings.SplitSeq(out, "\n") {
		assert.Equal(t, strings.TrimRight(line, " "), line, "line ends in whitespace: %q", line)
	}
}

func TestASubcommandFlagReachesTheConfiguration(t *testing.T) {
	// A root Before receives the root command, whose lineage stops at the root:
	// without walking down to the subcommand the args name, serve --port would
	// be parsed and then ignored, leaving the file's value in place.
	useConfig(t, `{}`)

	root := testRoot(&bytes.Buffer{}, "", &cli.Command{
		Name: "serve",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "port"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := configFrom(ctx)
			require.NoError(t, err)
			assert.Equal(t, 3191, cfg.Server.Port)
			return nil
		},
	})

	require.NoError(t, root.Run(t.Context(), []string{"saka", "serve", "--port", "3191"}))
}
