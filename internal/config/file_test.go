package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
)

func TestConfigFileResolvedFromEnvironment(t *testing.T) {
	path := writeConfig(t, "{"+baseBody+`, "server": {"port": 4444}}`)

	cfg, err := config.Load(config.Options{Environ: append(baseEnv(), config.FileEnv+"="+path)})
	require.NoError(t, err)

	assert.Equal(t, 4444, cfg.Server.Port)
}

func TestConfigFileFlagBeatsEnvironment(t *testing.T) {
	fromFlag := writeConfig(t, "{"+baseBody+`, "server": {"port": 1111}}`)
	fromEnv := writeConfig(t, "{"+baseBody+`, "server": {"port": 2222}}`)

	cfg, err := config.Load(config.Options{
		ConfigFile: fromFlag,
		Environ:    append(baseEnv(), config.FileEnv+"="+fromEnv),
	})
	require.NoError(t, err)

	assert.Equal(t, 1111, cfg.Server.Port)
}

func TestConfigFileRejectsMalformedJSON(t *testing.T) {
	path := writeConfig(t, `{"server": {"port":`)

	_, err := config.Load(config.Options{ConfigFile: path, Environ: baseEnv()})
	require.Error(t, err)
	assert.NotErrorIs(t, err, config.ErrNoConfigFile)
}

func TestConfigFileIgnoresUnknownKeys(t *testing.T) {
	path := configFile(t, `"server": {"port": 4444, "nope": true}, "unknown": {"a": 1}`)

	cfg, err := config.Load(config.Options{ConfigFile: path, Environ: baseEnv()})
	require.NoError(t, err)

	assert.Equal(t, 4444, cfg.Server.Port)
	assert.Empty(t, cfg.Origin("server.nope"))
	assert.Empty(t, cfg.Origin("unknown.a"))
}

func TestInterpolationFromEnvironment(t *testing.T) {
	path := configFile(t, `"app": {"base_url": "http://${TEST_HOST}:3080"}, "log": {"level": "info"}`)
	environ := append(baseEnv(), "TEST_HOST=example.test")

	cfg, err := config.Load(config.Options{ConfigFile: path, Environ: environ})
	require.NoError(t, err)

	assert.Equal(t, "http://example.test:3080", cfg.App.BaseURL)
	assert.Equal(t, "info", cfg.Log.Level, "a value with no directive is untouched")
}

func TestInterpolationExpandsEveryDirective(t *testing.T) {
	path := configFile(t, `"storage": {"local_path": "${A}-${B}-${C}"}`)
	environ := append(baseEnv(), "A=1", "B=2", "C=3")

	cfg, err := config.Load(config.Options{ConfigFile: path, Environ: environ})
	require.NoError(t, err)

	assert.Equal(t, "1-2-3", cfg.Storage.LocalPath)
}

func TestInterpolationAppliesToEveryField(t *testing.T) {
	// Interpolation is not limited to the string fields: a numeric or duration
	// field is written as a string and converted by the decoder, which is how a
	// generated file lets a variable fill any key.
	path := configFile(t, `"server": {"port": "${THE_PORT}", "read_timeout": "${THE_TIMEOUT}"}`)
	environ := append(baseEnv(), "THE_PORT=9000", "THE_TIMEOUT=45s")

	cfg, err := config.Load(config.Options{ConfigFile: path, Environ: environ})
	require.NoError(t, err)

	assert.Equal(t, 9000, cfg.Server.Port)
	assert.Equal(t, 45*time.Second, cfg.Server.ReadTimeout)
}

func TestInterpolationLeavesBareDollarAlone(t *testing.T) {
	// A password commonly contains $; expanding it would corrupt the value.
	path := configFile(t, `"storage": {"local_path": "/srv/$data$$x"}`)

	cfg, err := config.Load(config.Options{ConfigFile: path, Environ: baseEnv()})
	require.NoError(t, err)

	assert.Equal(t, "/srv/$data$$x", cfg.Storage.LocalPath)
}

func TestInterpolationLeavesUnterminatedDirectiveAlone(t *testing.T) {
	path := configFile(t, `"storage": {"local_path": "/srv/${5"}`)

	cfg, err := config.Load(config.Options{ConfigFile: path, Environ: baseEnv()})
	require.NoError(t, err)

	assert.Equal(t, "/srv/${5", cfg.Storage.LocalPath)
}

func TestInterpolationMissingVariableIsDeferred(t *testing.T) {
	// A directive naming an unset variable does not fail the load: the key keeps
	// its default and the problem is recorded, so a command that reads another
	// key is not blocked. Validate reports it.
	for _, body := range []string{
		`{"database": {"url": "env:NOT_SET_ANYWHERE"}}`,
		`{"database": {"url": "postgres://${NOT_SET_ANYWHERE}/db"}}`,
	} {
		cfg, err := config.Load(config.Options{ConfigFile: writeConfig(t, body), Environ: baseEnv()})
		require.NoError(t, err, body)

		require.Equal(t, "NOT_SET_ANYWHERE", cfg.Unresolved()["database.url"], body)

		// The key kept its default rather than holding the raw directive.
		require.Empty(t, cfg.Database.URL, body)

		// Validate reports the variable by name, so the message says what to set.
		err = cfg.Validate()
		require.ErrorIs(t, err, config.ErrInvalid, body)
		require.Contains(t, err.Error(), "NOT_SET_ANYWHERE", body)
	}
}

func TestUnresolvedVariableDoesNotBlockAnUnrelatedKey(t *testing.T) {
	// This is the property the whole design rests on: migrate:status reads
	// database.url and must not be stopped by a secret it never touches.
	cfg, err := config.Load(config.Options{
		ConfigFile: writeConfig(t, `{"database": {"url": "env:DATABASE_URL"}, "auth": {"secret_key": "env:NOT_SET"}}`),
		Environ:    baseEnv(),
	})
	require.NoError(t, err)

	require.Equal(t, dsn, cfg.Database.URL)
	require.Equal(t, "NOT_SET", cfg.Unresolved()["auth.secret_key"])
	require.Empty(t, cfg.Auth.SecretKey)
}

func TestEmptyVariableIsAValueNotAnUnresolvedDirective(t *testing.T) {
	// An empty value is a decision, unlike an absent variable.
	cfg, err := config.Load(config.Options{
		ConfigFile: writeConfig(t, `{"app": {"base_url": "env:EMPTY_URL"}}`),
		Environ:    append(baseEnv(), "EMPTY_URL="),
	})
	require.NoError(t, err)

	require.Empty(t, cfg.Unresolved())
	require.Equal(t, config.LayerConfigFile, cfg.Origin("app.base_url"))
}

func TestInterpolationOnlyAppliesToTheFile(t *testing.T) {
	// A variable's own value is data, not a template: a DSN containing ${ must
	// survive intact.
	path := writeConfig(t, `{"database": {"url": "env:LITERAL"}}`)

	cfg, err := config.Load(config.Options{
		ConfigFile: path,
		Environ:    append(baseEnv(), "LITERAL=/srv/${NOPE}"),
	})
	require.NoError(t, err)

	assert.Equal(t, "/srv/${NOPE}", cfg.Database.URL)
}
