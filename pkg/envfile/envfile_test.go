package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sample = `# application config
HOST=localhost
PORT=3080

# secrets
APP_SECRET_KEY=deadbeef
`

// The key is shared by the CLI, the config layer, and deployment tooling, so a
// rename here must be deliberate rather than incidental.
func TestDatabaseURLKey(t *testing.T) {
	assert.Equal(t, "DATABASE_URL", DatabaseURL)
}

func TestParsePreservesLayout(t *testing.T) {
	file := Parse(sample)
	assert.Equal(t, sample, file.Render())

	value, ok := file.Get("HOST")
	require.True(t, ok)
	assert.Equal(t, "localhost", value)

	_, ok = file.Get("MISSING")
	assert.False(t, ok)
}

func TestSetReplacesInPlace(t *testing.T) {
	file := Parse(sample)
	require.True(t, file.Set("APP_SECRET_KEY", "cafe"))

	assert.Equal(t, strings.ReplaceAll(sample, "deadbeef", "cafe"), file.Render())
}

func TestSetAppendsMissingKey(t *testing.T) {
	file := Parse("HOST=localhost")
	require.False(t, file.Set("AUTH_SECRET_KEY", "cafe"))

	assert.Equal(t, "HOST=localhost\nAUTH_SECRET_KEY=cafe\n", file.Render())
	assert.Equal(t, []string{"HOST", "AUTH_SECRET_KEY"}, file.Keys())
}

func TestParseHandlesEmptyAndCRLF(t *testing.T) {
	assert.Equal(t, "", Parse("").Render())
	assert.Equal(t, "HOST=localhost\n", Parse("HOST=localhost\r\n").Render())
	assert.Equal(t, "HOST=localhost\n", Parse("HOST=localhost").Render())
}

func TestSetQuotesUnsafeValues(t *testing.T) {
	file := Parse("")
	file.Set("PLAIN", "value")
	file.Set("EMPTY", "")
	file.Set("SPACED", "two words")

	assert.Equal(t, "PLAIN=value\nEMPTY=\"\"\nSPACED=\"two words\"\n", file.Render())
}

func TestGetUnquotesValues(t *testing.T) {
	file := Parse("A=\"quoted value\"\nB='single value'\nC=plain\n")

	for name, want := range map[string]string{"A": "quoted value", "B": "single value", "C": "plain"} {
		value, ok := file.Get(name)
		require.True(t, ok)
		assert.Equal(t, want, value)
	}
}

func TestParseSkipsCommentsAndBlankKeys(t *testing.T) {
	file := Parse("# comment\n\n=nokey\nexport EXPORTED=1\nKEY=value\n")
	assert.Equal(t, []string{"EXPORTED", "KEY"}, file.Keys())

	value, ok := file.Get("EXPORTED")
	require.True(t, ok)
	assert.Equal(t, "1", value)
}

func TestLoadMissingFileYieldsEmptyFile(t *testing.T) {
	file, err := Load(filepath.Join(t.TempDir(), "absent.env"))
	require.NoError(t, err)
	assert.Empty(t, file.Render())
}

func TestWriteCreatesWithRestrictedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	file := Parse("APP_SECRET_KEY=deadbeef\n")
	require.NoError(t, file.Write(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, DefaultMode, info.Mode().Perm())

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "APP_SECRET_KEY=deadbeef\n", string(raw))
}

func TestWriteKeepsExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	require.NoError(t, os.WriteFile(path, []byte("HOST=localhost\n"), 0o644))

	file, err := Load(path)
	require.NoError(t, err)
	file.Set("PORT", "3080")
	require.NoError(t, file.Write(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}
