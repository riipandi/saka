package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The engine is tested against a schema of its own, the way a second binary
// would use it: keys that belong to no application (decision 16).
func testSchema() Schema {
	return Schema{
		Defaults: map[string]any{
			"widget.name":   "default-widget",
			"widget.size":   30 * time.Second,
			"widget.tags":   []string{"a"},
			"widget.labels": map[string]string{},
			"gadget.count":  1,
		},
		Durations: []string{"widget.size"},
		Lists:     []string{"widget.tags"},
		Maps:      []string{"widget.labels"},
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.config.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestResolveAppliesTheLayersInPrecedenceOrder(t *testing.T) {
	path := writeConfig(t, `{"widget": {"name": "from-file"}}`)

	resolved, err := Resolve(Options{
		ConfigFile: path,
		Flags:      map[string]any{"widget.name": "from-flag"},
		Environ:    []string{},
	}, testSchema())
	require.NoError(t, err)

	var cfg struct {
		Widget struct {
			Name string `koanf:"name"`
		} `koanf:"widget"`
	}
	require.NoError(t, resolved.Unmarshal(&cfg))
	assert.Equal(t, "from-flag", cfg.Widget.Name)
	assert.Equal(t, LayerFlag, resolved.Origin("widget.name"))

	// A key no source set keeps its default.
	assert.Equal(t, LayerDefault, resolved.Origin("gadget.count"))
}

func TestResolveKeepsUnsetKeysAtTheirDefaults(t *testing.T) {
	path := writeConfig(t, `{"widget": {"size": 90}}`)

	resolved, err := Resolve(Options{ConfigFile: path, Environ: []string{}}, testSchema())
	require.NoError(t, err)

	var cfg struct {
		Widget struct {
			Name  string        `koanf:"name"`
			Size  time.Duration `koanf:"size"`
			Count int           `koanf:"gadget.count"`
		} `koanf:"widget"`
		Gadget struct {
			Count int `koanf:"count"`
		} `koanf:"gadget"`
	}
	require.NoError(t, resolved.Unmarshal(&cfg))
	assert.Equal(t, "default-widget", cfg.Widget.Name)
	assert.Equal(t, 90*time.Second, cfg.Widget.Size)
	assert.Equal(t, 1, cfg.Gadget.Count)
	assert.Equal(t, LayerConfigFile, resolved.Origin("widget.size"))
}

func TestResolveDropsKeysTheSchemaDoesNotKnow(t *testing.T) {
	path := writeConfig(t, `{"widget": {"name": "kept"}, "stray": {"name": "dropped"}}`)

	resolved, err := Resolve(Options{ConfigFile: path, Environ: []string{}}, testSchema())
	require.NoError(t, err)

	assert.Equal(t, LayerConfigFile, resolved.Origin("widget.name"))
	assert.Empty(t, resolved.Origin("stray.name"), "a key outside the schema must not exist")
}

func TestResolveInterpolatesTheFileDirectivesFromTheEnvironment(t *testing.T) {
	path := writeConfig(t, `{"widget": {"name": "env:WIDGET_NAME", "labels": "a=1,b=${WIDGET_B}"}}`)

	resolved, err := Resolve(Options{
		ConfigFile: path,
		EnvFile:    map[string]string{"WIDGET_NAME": "from-envfile"},
		Environ:    []string{"WIDGET_B=two", "WIDGET_NAME=from-system"},
	}, testSchema())
	require.NoError(t, err)

	// The env file wins over the system environment for a name both set.
	assert.Equal(t, "from-envfile", resolved.LookupEnv("WIDGET_NAME"))

	var cfg struct {
		Widget struct {
			Name   string            `koanf:"name"`
			Labels map[string]string `koanf:"labels"`
		} `koanf:"widget"`
	}
	require.NoError(t, resolved.Unmarshal(&cfg))
	assert.Equal(t, "from-envfile", cfg.Widget.Name)
	assert.Equal(t, map[string]string{"a": "1", "b": "two"}, cfg.Widget.Labels)
}

func TestResolveReportsAnUnresolvedDirectiveAndKeepsTheDefault(t *testing.T) {
	path := writeConfig(t, `{"widget": {"name": "env:WIDGET_ABSENT"}}`)

	resolved, err := Resolve(Options{ConfigFile: path, Environ: []string{}}, testSchema())
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"widget.name": "WIDGET_ABSENT"}, resolved.Unresolved())
	assert.Equal(t, LayerDefault, resolved.Origin("widget.name"),
		"a dropped directive leaves the key at the layer that set it last")
}

func TestResolveReadsTheSourceForms(t *testing.T) {
	path := writeConfig(t, `{
		"widget": {
			"size": 900,
			"tags": "one, two",
			"labels": "x=1,y=2"
		}
	}`)

	resolved, err := Resolve(Options{ConfigFile: path, Environ: []string{}}, testSchema())
	require.NoError(t, err)

	var cfg struct {
		Widget struct {
			Size   time.Duration     `koanf:"size"`
			Tags   []string          `koanf:"tags"`
			Labels map[string]string `koanf:"labels"`
		} `koanf:"widget"`
	}
	require.NoError(t, resolved.Unmarshal(&cfg))
	assert.Equal(t, 900*time.Second, cfg.Widget.Size, "a bare number is seconds")
	assert.Equal(t, []string{"one", "two"}, cfg.Widget.Tags)
	assert.Equal(t, map[string]string{"x": "1", "y": "2"}, cfg.Widget.Labels)
}

func TestResolveRequiresAConfigFile(t *testing.T) {
	_, err := Resolve(Options{
		ConfigFile: filepath.Join(t.TempDir(), "absent.json"),
		Environ:    []string{},
	}, testSchema())
	require.ErrorIs(t, err, ErrNoConfigFile)
}

func TestConfigPathResolvesTheSourcesInOrder(t *testing.T) {
	named := filepath.Join(t.TempDir(), "named.json")

	assert.Equal(t, named, ConfigPath(Options{ConfigFile: named}, nil))
	assert.Equal(t, "from-env", ConfigPath(Options{}, []string{FileEnv + "=from-env"}))
	assert.Equal(t, DefaultConfigFile, ConfigPath(Options{}, []string{}))
}

func TestEnvNameSpellsTheConvention(t *testing.T) {
	assert.Equal(t, "DATABASE_URL", EnvName("database.url"))
	assert.Equal(t, "WIDGET_NAME", EnvName("widget.name"))
}
