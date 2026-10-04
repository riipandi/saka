package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fconfig "github.com/riipandi/saka/framework/config"
	"github.com/riipandi/saka/internal/config"
)

// Precedence, lowest to highest: defaults, the config file, flags. The
// environment supplies values to the file's directives but is not itself a
// layer, so it can never outrank the file.

func TestConfigFileBeatsDefaults(t *testing.T) {
	cfg := load(t, "{"+baseBody+`, "server": {"port": 4444, "host": "from-file"}, "log": {"level": "warn"}}`)

	assert.Equal(t, 4444, cfg.Server.Port, "the file must win over the default")
	assert.Equal(t, "from-file", cfg.Server.Host)
	assert.Equal(t, config.LogWarn, cfg.Log.Level)
	assert.Equal(t, fconfig.LayerConfigFile, cfg.Origin("log.level"))
	assert.Equal(t, fconfig.LayerDefault, cfg.Origin("auth.issuer"), "a key no source set keeps the default")
}

func TestFlagBeatsConfigFile(t *testing.T) {
	cfg, err := config.Load(config.Options{
		ConfigFile: configFile(t, `"server": {"port": 4444}`),
		Environ:    baseEnv(),
		Flags:      map[string]any{"server.port": 3333},
	})
	require.NoError(t, err)

	assert.Equal(t, 3333, cfg.Server.Port)
	assert.Equal(t, fconfig.LayerFlag, cfg.Origin("server.port"))
}

func TestFlagBeatsTheVariableTheFileReferences(t *testing.T) {
	// A variable fills a key the file left to it; a flag still outranks the
	// result, because a flag is a decision made for this run.
	cfg, err := config.Load(config.Options{
		ConfigFile: configFile(t, `"server": {"port": "${THE_PORT}"}`),
		Environ:    append(baseEnv(), "THE_PORT=4444"),
		Flags:      map[string]any{"server.port": 3333},
	})
	require.NoError(t, err)

	assert.Equal(t, 3333, cfg.Server.Port)
	assert.Equal(t, fconfig.LayerFlag, cfg.Origin("server.port"))
}

func TestNestedLeafOverrideKeepsSiblings(t *testing.T) {
	// A flag sets one leaf inside a section the file also sets; the file's other
	// leaves must survive.
	cfg, err := config.Load(config.Options{
		ConfigFile: configFile(t, `"database": {"max_conns": 20, "min_conns": 5}`),
		Environ:    baseEnv(),
		Flags:      map[string]any{"database.max_conns": 30},
	})
	require.NoError(t, err)

	assert.Equal(t, int32(30), cfg.Database.MaxConns, "the flag wins for the leaf it sets")
	assert.Equal(t, int32(5), cfg.Database.MinConns, "a sibling keeps the file value")
}

func TestOriginReportsTheWinningLayer(t *testing.T) {
	cfg, err := config.Load(config.Options{
		ConfigFile: configFile(t, `"server": {"port": 4444}`),
		Environ:    baseEnv(),
		Flags:      map[string]any{"server.port": 6666},
	})
	require.NoError(t, err)

	assert.Equal(t, fconfig.LayerFlag, cfg.Origin("server.port"))
	assert.Equal(t, fconfig.LayerConfigFile, cfg.Origin("database.url"))
	assert.Equal(t, fconfig.LayerDefault, cfg.Origin("log.level"))
	assert.Empty(t, cfg.Origin("nope"))

	origins := cfg.Origins()
	assert.Equal(t, fconfig.LayerFlag, origins["server.port"])

	// Origins must return a copy: mutating it cannot change the Config.
	origins["server.port"] = "tampered"
	assert.Equal(t, fconfig.LayerFlag, cfg.Origin("server.port"))
}
