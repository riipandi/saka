package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"encoding/json/jsontext"
	"encoding/json/v2"

	"github.com/riipandi/saka/internal/config"
)

// dsn is a valid Postgres connection string, so a test that only exercises
// precedence does not fail validation for an unrelated reason.
const dsn = "postgresql://user:pass@localhost:5432/saka?sslmode=disable"

// secret is a 64-character hex string, the shape of APP_SECRET_KEY.
const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// baseBody is the smallest config file that satisfies validation. It is
// interpolated rather than literal, so no test file holds a credential, and it
// doubles as the example of how a secret is meant to be written.
const baseBody = `"database": {"url": "env:DATABASE_URL"}, "auth": {"secret_key": "env:AUTH_SECRET_KEY"}`

// baseEnv is the environment that baseBody resolves from. PUBLIC_BASE_URL
// rides along because Validate requires an issuer, and the loader fills an
// unset one from this variable.
func baseEnv() []string {
	return []string{
		"DATABASE_URL=" + dsn,
		"AUTH_SECRET_KEY=" + secret,
		"PUBLIC_BASE_URL=http://localhost:3080",
	}
}

// writeConfig writes a JSON config file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "app.config.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// configFile writes a config file holding the base keys plus extra, a JSON
// object of further keys. Sections are merged, so a test states only the keys it
// is about and can extend database or auth without repeating them.
func configFile(t *testing.T, extra string) string {
	t.Helper()

	doc := map[string]any{
		"database": map[string]any{"url": "env:DATABASE_URL"},
		"auth":     map[string]any{"secret_key": "env:AUTH_SECRET_KEY"},
	}
	if extra != "" {
		var more map[string]any
		require.NoError(t, json.Unmarshal([]byte("{"+extra+"}"), &more))
		mergeDoc(doc, more)
	}

	body, err := json.Marshal(doc, jsontext.WithIndent("    "))
	require.NoError(t, err)
	return writeConfig(t, string(body))
}

// mergeDoc copies every key of src into dst, recursing when both sides hold an
// object so a fragment can add one leaf to a section the base already defines.
func mergeDoc(dst, src map[string]any) {
	for key, value := range src {
		srcChild, ok := value.(map[string]any)
		if !ok {
			dst[key] = value
			continue
		}
		dstChild, ok := dst[key].(map[string]any)
		if !ok {
			dst[key] = srcChild
			continue
		}
		mergeDoc(dstChild, srcChild)
	}
}

// load resolves a configuration from a file body and the base environment plus
// extra variables. It fails the test when resolution fails, which is the
// outcome every caller but the error tests wants.
func load(t *testing.T, body string, extra ...string) config.Config {
	t.Helper()

	cfg, err := config.Load(config.Options{
		ConfigFile: writeConfig(t, body),
		Environ:    append(baseEnv(), extra...),
	})
	require.NoError(t, err)
	return cfg
}

// writeEnvFile writes a dotenv file and returns its path.
func writeEnvFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".env.local")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestDefaultsAreValid(t *testing.T) {
	cfg := load(t, "{"+baseBody+"}")

	defaults := config.Default()
	require.Equal(t, defaults.Server.Port, cfg.Server.Port)
	require.Equal(t, defaults.Storage.LocalPath, cfg.Storage.LocalPath)
	require.Equal(t, defaults.Log.Level, cfg.Log.Level)
	require.Equal(t, config.LayerDefault, cfg.Origin("server.port"))
}

func TestIssuerFallsBackToPublicBaseURL(t *testing.T) {
	// An issuer no source set takes PUBLIC_BASE_URL: the origin the
	// deployment is published at is the value a token consumer checks the
	// claim against. The fallback is a resolution rule, not a layer, so
	// Origin keeps reporting that no source set the key.
	path := configFile(t, "")
	environ := append([]string{"PUBLIC_BASE_URL=https://saka.example"}, baseEnv()...)

	cfg, err := config.Load(config.Options{ConfigFile: path, Environ: environ})
	require.NoError(t, err)

	assert.Equal(t, "https://saka.example", cfg.Auth.Issuer)
	assert.Equal(t, config.LayerDefault, cfg.Origin("auth.issuer"),
		"the fallback is a resolution rule, not a layer")
}

func TestEnvironmentAloneCannotSetAKey(t *testing.T) {
	// The environment alone cannot set a key: with a file that says nothing
	// about the port, every key keeps its default. This is the guarantee that a
	// stray export cannot change a run.
	cfg, err := config.Load(config.Options{
		ConfigFile: configFile(t, ""),
		Environ:    append(baseEnv(), "SERVER_PORT=9999"),
	})
	require.NoError(t, err)

	require.Equal(t, config.Default().Server, cfg.Server)
	require.Equal(t, config.LayerDefault, cfg.Origin("server.port"))
}

func TestNamedConfigFileMustExist(t *testing.T) {
	_, err := config.Load(config.Options{
		ConfigFile: filepath.Join(t.TempDir(), "absent.json"),
		Environ:    baseEnv(),
	})
	require.ErrorIs(t, err, config.ErrNoConfigFile)
}

func TestDefaultConfigFileInWorkingDirectory(t *testing.T) {
	// The file is the source of truth, so the default location is looked up in
	// the working directory when no source names one.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, config.DefaultConfigFile),
		[]byte("{"+baseBody+`, "server": {"port": 7777}}`),
		0o600,
	))
	t.Chdir(dir)

	cfg, err := config.Load(config.Options{Environ: baseEnv()})
	require.NoError(t, err)

	require.Equal(t, 7777, cfg.Server.Port)
	require.Equal(t, config.LayerConfigFile, cfg.Origin("server.port"))
}

func TestMissingDefaultConfigFileIsAnError(t *testing.T) {
	// A run with no configuration is not a configuration anyone chose, so the
	// absence is reported rather than silently falling back to the defaults.
	t.Chdir(t.TempDir())

	_, err := config.Load(config.Options{Environ: baseEnv()})
	require.ErrorIs(t, err, config.ErrNoConfigFile)
}

func TestConfigPathPrecedence(t *testing.T) {
	fromFlag := "/from/flag.json"
	fromEnv := "/from/env.json"

	require.Equal(t, fromFlag, config.ConfigPath(
		config.Options{ConfigFile: fromFlag},
		[]string{config.FileEnv + "=" + fromEnv}))
	require.Equal(t, fromEnv, config.ConfigPath(
		config.Options{},
		[]string{config.FileEnv + "=" + fromEnv}))
	require.Equal(t, config.DefaultConfigFile, config.ConfigPath(config.Options{}, nil))
}
