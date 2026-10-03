package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/health"
	"github.com/riipandi/saka/pkg/envfile"
	"github.com/riipandi/saka/pkg/testutils"
)

// passing is a check function that always succeeds.
func passing(context.Context) error { return nil }

// runHealthCmd executes the health command with args and returns stdout.
//
// The test working directory is cmd/, which has no storage/ next to it, so the
// data directory is pointed at a fresh temp directory through the configuration:
// the storage check would otherwise fail for a reason the test does not care
// about.
func runHealthCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runHealthCmdIn(t, t.TempDir(), args...)
}

// runHealthCmdIn runs the health command with an explicit data directory. The
// directory is written into the config file, because the data directory has no
// flag and no variable of its own: the file is the only place it is set.
func runHealthCmdIn(t *testing.T, dataDir string, args ...string) (string, error) {
	t.Helper()

	configFor(t, dataDir)

	// A container that starts under load answers slower than the default probe
	// budget, which would fail a healthy check for a reason the test is not
	// about. A test that exercises the timeout passes its own --timeout.
	if !hasFlag(args, "timeout") {
		args = append([]string{"--timeout=30s"}, args...)
	}

	var out bytes.Buffer
	root := testRoot(&out, "", healthCheckCmd)
	err := root.Run(context.Background(), append([]string{"saka", healthCheckCmd.Name}, args...))
	return out.String(), err
}

// hasFlag reports whether args names the flag, with either = or a space.
func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--"+name || strings.HasPrefix(arg, "--"+name+"=") {
			return true
		}
	}
	return false
}

// healthEnvFile writes an env file pointing at dsn.
func healthEnvFile(t *testing.T, dsn string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".env.local")
	require.NoError(t, os.WriteFile(path, []byte(envfile.DatabaseURL+"="+dsn+"\n"), 0o600))
	return path
}

func TestHealthReportsUpAgainstAReachableDatabase(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := healthEnvFile(t, container.NewDatabase(t))

	out, err := runHealthCmd(t, "--env-file="+envFile)
	require.NoError(t, err)
	assert.Contains(t, out, "status: healthy")
}

func TestHealthReportsDownAgainstAnUnreachableDatabase(t *testing.T) {
	// A port nothing listens on: the probe must fail, not hang.
	envFile := healthEnvFile(t, "postgresql://postgres:securedb@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")

	out, err := runHealthCmd(t, "--env-file="+envFile, "--timeout=3s")
	require.Error(t, err)

	// The exit code must be distinct from a generic failure, so a script can
	// tell "the service is unhealthy" from "the command could not run".
	var exitErr cli.ExitCoder
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, exitUnhealthy, exitErr.ExitCode())
	assert.Contains(t, out, "status: unhealthy")
	assert.Contains(t, out, "postgres")
}

// A missing DSN is a usage error, not an unhealthy service: it must not report
// exit code 3.
func TestHealthFailsWithoutDatabaseURL(t *testing.T) {
	t.Setenv(envfile.DatabaseURL, "")
	t.Setenv("HOME", t.TempDir())

	out, err := runHealthCmd(t)
	require.ErrorIs(t, err, ErrDatabaseURLUnset)
	assert.NotContains(t, out, "unhealthy")
}

// --json must print a machine-readable result with the same shape as the REST
// body's data field.
func TestHealthJSONOutput(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := healthEnvFile(t, container.NewDatabase(t))

	out, err := runHealthCmd(t, "--env-file="+envFile, "--json")
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "healthy", result["status"])
	assert.Contains(t, result, "took_ms")

	// Details are an ordered array, so the CLI output and the API body have the
	// same shape and the same order.
	details, ok := result["details"].([]any)
	require.True(t, ok, "details must be an array")
	require.Len(t, details, 2)

	// Ordered by check name, so the CLI and the API always agree.
	first, ok := details[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "database", first["name"])
	assert.Equal(t, "up", first["status"])

	second, ok := details[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "storage", second["name"])
	assert.Equal(t, "up", second["status"])

	// The CLI report may name the data directory: an operator reading the
	// terminal owns the machine. The endpoint leaves the target empty.
	storage, ok := second["target"].(string)
	require.True(t, ok, "storage target must be present in the CLI report")
	assert.True(t, filepath.IsAbs(storage), "storage target must be absolute: %s", storage)
}

// --short must print one word, so a shell script reads it without parsing.
func TestHealthShortOutput(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := healthEnvFile(t, container.NewDatabase(t))

	out, err := runHealthCmd(t, "--env-file="+envFile, "--short")
	require.NoError(t, err)
	assert.Equal(t, "healthy\n", out)

	// An unreachable database reports the other word and the failing exit code.
	badEnvFile := healthEnvFile(t, "postgresql://postgres:securedb@127.0.0.1:1/none?sslmode=disable")
	out, err = runHealthCmd(t, "--env-file="+badEnvFile, "--short", "--timeout=3s")
	require.Error(t, err)
	assert.Equal(t, "unhealthy\n", out)
}

// The default output is the human-readable report, and it is stable enough to
// assert its shape.
func TestHealthTextOutput(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := healthEnvFile(t, container.NewDatabase(t))

	out, err := runHealthCmd(t, "--env-file="+envFile)
	require.NoError(t, err)

	// The report carries the display name, which is not the CLI identifier.
	assert.Contains(t, out, "name: "+config.AppName)
	assert.Contains(t, out, "version: "+config.AppVersion)
	assert.Contains(t, out, "status: healthy")
	assert.Contains(t, out, "checks: 2 up, 0 down")
	assert.Contains(t, out, "database: up (")
	assert.Contains(t, out, "storage: up (")
}

// The storage check must reach the CLI report and use the resolved data
// directory, so an unusable directory fails the command.
func TestHealthReportsUnusableDataDir(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := healthEnvFile(t, container.NewDatabase(t))

	// A directory that does not exist: the application would only discover
	// this when it tried to store the first file.
	out, err := runHealthCmdIn(t, filepath.Join(t.TempDir(), "nope"), "--env-file="+envFile)
	require.Error(t, err)

	var exitErr cli.ExitCoder
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, exitUnhealthy, exitErr.ExitCode())
	assert.Contains(t, out, "storage: down")
	assert.Contains(t, out, "does not exist")
}

// A world-writable data directory must fail the probe, because it holds uploads
// and certificates.
func TestHealthReportsWorldWritableDataDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}

	container := testutils.StartPostgres(t.Context(), t)
	envFile := healthEnvFile(t, container.NewDatabase(t))

	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o777))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	out, err := runHealthCmdIn(t, dir, "--env-file="+envFile)
	require.Error(t, err)
	assert.Contains(t, out, "world-writable")
}

// Every failing check must be reported, not just the first.
func TestHealthTextReportsEveryFailure(t *testing.T) {
	result := health.Result{
		Status: health.GlobalUnhealthy,
		Details: map[string]health.CheckResult{
			"postgres": {Name: "postgres", Status: health.StatusDown, Error: "refused"},
			"valkey":   {Name: "valkey", Status: health.StatusDown, Error: "refused"},
		},
	}

	var out strings.Builder
	require.NoError(t, health.WriteText(&out, result, nil))
	assert.Contains(t, out.String(), "postgres")
	assert.Contains(t, out.String(), "valkey")
}

// The CLI must not define a second rendering of the result: --json and the REST
// data field are the same object.
func TestHealthJSONMatchesTheAPIShape(t *testing.T) {
	checker := health.NewChecker(
		health.WithCheck(health.Check{Name: "postgres", Check: passing}),
		health.WithInfo(map[string]string{"name": "saka"}),
	)

	encoded, err := json.Marshal(checker.Check(t.Context()))
	require.NoError(t, err)

	var cli map[string]any
	require.NoError(t, json.Unmarshal(encoded, &cli))

	recorder := httptest.NewRecorder()
	health.Handler(checker).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	api, ok := envelope["data"].(map[string]any)
	require.True(t, ok)

	// The whole-call duration is measured per call, so it differs by design.
	// Everything else — the status, the ordered details, the info — must match.
	delete(cli, "took_ms")
	delete(api, "took_ms")
	assert.Equal(t, api, cli)
}

// --short must win over --json: a caller that asks for one word gets one word.
func TestHealthShortWinsOverJSON(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := healthEnvFile(t, container.NewDatabase(t))

	out, err := runHealthCmd(t, "--env-file="+envFile, "--short", "--json")
	require.NoError(t, err)
	assert.Equal(t, "healthy\n", out)
}

// --no-cache must run the check again instead of reusing a cached result.
func TestHealthNoCacheFlagExists(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	envFile := healthEnvFile(t, container.NewDatabase(t))

	out, err := runHealthCmd(t, "--env-file="+envFile, "--no-cache", "--short")
	require.NoError(t, err)
	assert.Equal(t, "healthy\n", out)
}

// The report names the database that was reached, and never its password: an
// operator pastes this output into a ticket. The rendering lives in the config
// layer, so the CLI, the health report, and the dump report cannot disagree.
func TestRedactDSNOmitsCredentials(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "url form",
			dsn:  "postgresql://postgres:supersecret@db.internal:5432/saka?sslmode=disable",
			want: "db.internal:5432/saka",
		},
		{
			name: "key value form",
			dsn:  "host=db.internal port=5432 dbname=saka user=postgres password=supersecret",
			want: "db.internal:5432/saka",
		},
		{
			name: "unparsable dsn reports a placeholder",
			dsn:  "://not-a-dsn",
			want: "[redacted]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := config.RedactDSN(tt.dsn)

			assert.Equal(t, tt.want, target)
			assert.NotContains(t, target, "supersecret")
		})
	}
}
