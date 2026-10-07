package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fconfig "github.com/riipandi/saka/framework/config"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/pkg/envfile"
)

// The environment is not a layer: a variable reaches a config key only where the
// file references it. These tests hold that line, because it is what makes the
// file the single source of truth.

func TestVariableAloneDoesNotReachAKey(t *testing.T) {
	// The whole point of the design: exporting a variable cannot change a value
	// the config file did not ask for.
	cfg := load(t, "{"+baseBody+"}", "SERVER_PORT=9999", "LOG_LEVEL=debug")

	assert.Equal(t, config.Default().Server.Port, cfg.Server.Port)
	assert.Equal(t, config.Default().Log.Level, cfg.Log.Level)
	assert.Equal(t, fconfig.LayerDefault, cfg.Origin("server.port"))
	assert.Empty(t, cfg.Origin("server.port.by_env"))
}

func TestArbitrarilyNamedVariableReachesAKey(t *testing.T) {
	// A user names the variable whatever they like; the file decides which key
	// it fills.
	cfg := load(t, `{
		"database": {"url": "env:MY_OWN_DSN"},
		"auth": {"secret_key": "env:THE_HMAC"},
		"server": {"port": "${THE_PORT}"}
	}`, "MY_OWN_DSN="+dsn, "THE_HMAC="+secret, "THE_PORT=4444")

	assert.Equal(t, dsn, cfg.Database.URL)
	assert.Equal(t, secret, cfg.Auth.SecretKey)
	assert.Equal(t, 4444, cfg.Server.Port)
}

func TestEnvNameRoundTripsEveryKey(t *testing.T) {
	// EnvName is a naming convention, not a loader mapping, but it must still be
	// total and unique: the generated file and .env.example rely on it, and two
	// keys sharing a name would make one of them unnameable.
	seen := make(map[string]string)
	for _, key := range config.Keys() {
		name := fconfig.EnvName(key)
		assert.NotContains(t, seen, name, "duplicate environment name for %s and %s", seen[name], key)
		seen[name] = key
	}
	assert.Equal(t, "DATABASE_URL", fconfig.EnvName("database.url"))
	assert.Equal(t, "AUTH_SECRET_KEY", fconfig.EnvName("auth.secret_key"))
	assert.Equal(t, "SERVER_READ_TIMEOUT", fconfig.EnvName("server.read_timeout"))
}

func TestEnvNameAgreesWithEnvfile(t *testing.T) {
	// pkg/envfile spells DATABASE_URL out because pkg/ cannot import internal/.
	// The config layer derives the same name, so the two must agree.
	assert.Equal(t, envfile.DatabaseURL, fconfig.EnvName("database.url"))
}

func TestUnknownFileKeyIsIgnored(t *testing.T) {
	// A file key naming no config key must not reach the layer: an unrelated
	// entry cannot change the configuration.
	cfg := load(t, `{
		"database": {"url": "env:DATABASE_URL"},
		"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
		"nope": {"a": 1},
		"storage": "oops"
	}`)

	assert.Equal(t, config.StorageLocal, cfg.Storage.Driver)
	assert.Equal(t, config.DefaultDataDir, cfg.Storage.LocalPath)
	assert.Empty(t, cfg.Origin("nope.a"))
}

func TestEmptyValueIsKept(t *testing.T) {
	// An empty value is a decision, unlike an absent variable: it must reach the
	// layer so validation can report it.
	cfg := load(t, `{
		"database": {"url": "env:DATABASE_URL"},
		"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
		"app": {"base_url": ""}
	}`)

	assert.Empty(t, cfg.App.BaseURL)
	assert.Equal(t, fconfig.LayerConfigFile, cfg.Origin("app.base_url"))
}

func TestDurationAndNumericFromFile(t *testing.T) {
	// A duration key is written as a plain number of seconds, which is the form
	// config:generate writes. A duration string is still accepted, because a
	// hand-written file may carry the unit explicitly.
	cfg := load(t, `{
		"database": {"url": "env:DATABASE_URL", "max_conns": 25, "connect_timeout": 3},
		"auth": {"secret_key": "env:AUTH_SECRET_KEY", "access_ttl": "15m"},
		"server": {"port": 9000, "read_timeout": 45, "idle_timeout": 90}
	}`)

	assert.Equal(t, int32(25), cfg.Database.MaxConns)
	assert.Equal(t, 9000, cfg.Server.Port)
	assert.Equal(t, 45*time.Second, cfg.Server.ReadTimeout)
	assert.Equal(t, 90*time.Second, cfg.Server.IdleTimeout)
	assert.Equal(t, 3*time.Second, cfg.Database.ConnectTimeout)
	assert.Equal(t, 15*time.Minute, cfg.Auth.AccessTTL)
}

func TestNumberOutsideADurationKeyIsNotReadAsSeconds(t *testing.T) {
	// The conversion is keyed, not typed: only a named duration key turns a bare
	// number into seconds. A number under any other key keeps its own meaning.
	cfg := load(t, `{"database": {"url": "env:DATABASE_URL", "max_conns": 25}}`)

	assert.Equal(t, int32(25), cfg.Database.MaxConns)
}

func TestEnvFileFeedsInterpolation(t *testing.T) {
	// --env-file is the table the file's directives resolve from, so a secret
	// can live in a file that is never committed while the config file names it.
	path := writeConfig(t, `{
		"database": {"url": "env:MY_DSN"},
		"auth": {"secret_key": "env:MY_SECRET"},
		"app": {"base_url": "http://${MY_HOST}:3000"}
	}`)

	cfg, err := config.Load(config.Options{
		ConfigFile: path,
		EnvFile:    map[string]string{"MY_DSN": dsn, "MY_SECRET": secret, "MY_HOST": "example.test"},
		Environ:    nil,
	})
	require.NoError(t, err)

	assert.Equal(t, dsn, cfg.Database.URL)
	assert.Equal(t, secret, cfg.Auth.SecretKey)
	assert.Equal(t, "http://example.test:3000", cfg.App.BaseURL)
}

func TestEnvFileBeatsSystemEnvironmentForAName(t *testing.T) {
	// A user who named an env file meant it to be the one that answers.
	path := writeConfig(t, `{
		"database": {"url": "env:DATABASE_URL"},
		"auth": {"secret_key": "env:AUTH_SECRET_KEY"}
	}`)

	cfg, err := config.Load(config.Options{
		ConfigFile: path,
		Environ:    []string{"DATABASE_URL=postgresql://system@system:5432/system"},
		EnvFile:    map[string]string{"DATABASE_URL": dsn, "AUTH_SECRET_KEY": secret},
	})
	require.NoError(t, err)

	assert.Equal(t, dsn, cfg.Database.URL)
}
