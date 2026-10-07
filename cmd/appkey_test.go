package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/envfile"
)

// runKeyGenerateCmd executes key:generate with args and returns stdout.
// stdin feeds the confirmation prompt; an empty stdin means no answer.
// Only the subcommand's own --env-file flag is registered, mirroring
// cli_debug.go where the root flag exists for the server commands.
func runKeyGenerateCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer

	root := &cli.Command{
		Name:     "saka",
		Writer:   &out,
		Reader:   strings.NewReader(stdin),
		Commands: []*cli.Command{keyGenerateCmd},
	}
	err := root.Run(context.Background(), append([]string{"saka", "key:generate"}, args...))
	return out.String(), err
}

func TestKeyGeneratePrintsAllKeys(t *testing.T) {
	out, err := runKeyGenerateCmd(t, "")
	require.NoError(t, err)

	for _, name := range []string{"APP_SECRET_KEY", "AUTH_SECRET_KEY"} {
		assert.Contains(t, out, name+"=")
	}
	assert.Equal(t, 2, strings.Count(out, "\n"))
}

// Without --env-file the command must not prompt or touch any file, even
// when .env.local exists in the working directory.
func TestKeyGenerateWithoutEnvFileTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	original := "SERVER_HOST=localhost\nAPP_SECRET_KEY=stale\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.local"), []byte(original), 0o600))

	t.Chdir(dir)

	out, err := runKeyGenerateCmd(t, "")
	require.NoError(t, err)

	assert.NotContains(t, out, "already exists")
	assert.NotContains(t, out, "left unchanged")
	assert.Equal(t, 2, strings.Count(out, "\n"))

	raw, err := os.ReadFile(filepath.Join(dir, ".env.local"))
	require.NoError(t, err)
	assert.Equal(t, original, string(raw), "the env file must not change")
}

// A root --env-file (used to load config for the server commands) must
// not turn into a write target for key:generate.
func TestKeyGenerateIgnoresRootEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.local")
	original := "SERVER_HOST=localhost\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

	var out bytes.Buffer
	root := &cli.Command{
		Name:     "saka",
		Writer:   &out,
		Reader:   strings.NewReader(""),
		Commands: []*cli.Command{keyGenerateCmd},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "env-file", Usage: "Load environment variables from a file"},
		},
	}
	require.NoError(t, root.Run(context.Background(),
		[]string{"saka", "--env-file=" + path, "key:generate"}))

	assert.NotContains(t, out.String(), "already exists")
	assert.NotContains(t, out.String(), "wrote ")
	assert.Equal(t, 2, strings.Count(out.String(), "\n"))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, string(raw), "the env file must not change")
}

func TestKeyGeneratePrintsAllKeysForEveryAlgorithm(t *testing.T) {
	for _, algorithm := range []string{"ES384", "EdDSA", "RS256"} {
		_, err := runKeyGenerateCmd(t, "", "--algorithm="+algorithm)
		require.ErrorIs(t, err, crypto.ErrUnsupportedAlgorithm, algorithm)
	}

	for _, algorithm := range []string{"HS256", "HS384", "HS512"} {
		out, err := runKeyGenerateCmd(t, "", "--algorithm="+algorithm)
		require.NoError(t, err, algorithm)

		for _, name := range []string{"APP_SECRET_KEY", "AUTH_SECRET_KEY"} {
			assert.Contains(t, out, name+"=", algorithm)
		}
	}
}

func TestKeyGenerateRejectsUnknownAlgorithm(t *testing.T) {
	_, err := runKeyGenerateCmd(t, "", "--algorithm=HS999")
	assert.ErrorIs(t, err, crypto.ErrUnsupportedAlgorithm)
}

func TestKeyGenerateCreatesMissingEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")

	out, err := runKeyGenerateCmd(t, "", "--env-file="+path)
	require.NoError(t, err)
	assert.Contains(t, out, "(2 added, 0 replaced; secret HS256)")

	file, err := envfile.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"APP_SECRET_KEY", "AUTH_SECRET_KEY"},
		file.Keys())
}

func TestKeyGenerateAsksBeforeReplacing(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")
	original := "# app\nSERVER_HOST=localhost\nAPP_SECRET_KEY=stale\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	out, err := runKeyGenerateCmd(t, "y\n", "--env-file="+path)
	require.NoError(t, err)
	assert.Contains(t, out, "already exists — replace its key values? [y/N]")
	assert.Contains(t, out, "(1 added, 1 replaced; secret HS256)")

	file, err := envfile.Load(path)
	require.NoError(t, err)
	secret, ok := file.Get("APP_SECRET_KEY")
	require.True(t, ok)
	assert.NotEqual(t, "stale", secret)
	assert.Equal(t, "localhost", hostOf(t, file))
}

func TestKeyGenerateLeavesFileUntouchedWithoutConfirmation(t *testing.T) {
	for name, stdin := range map[string]string{
		"declined":        "n\n",
		"empty answer":    "\n",
		"no input at all": "",
		"other word":      "maybe\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env.local")
			original := "SERVER_HOST=localhost\nAPP_SECRET_KEY=stale\n"
			require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

			out, err := runKeyGenerateCmd(t, stdin, "--env-file="+path)
			require.NoError(t, err)

			// The keys are still printed so they can be copied by hand.
			assert.Contains(t, out, "APP_SECRET_KEY=")
			assert.Contains(t, out, path+" left unchanged")

			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, original, string(raw), "the env file must not change")
		})
	}
}

func TestKeyGenerateOverwriteSkipsConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")
	require.NoError(t, os.WriteFile(path, []byte("SERVER_HOST=localhost\nAPP_SECRET_KEY=stale\n"), 0o600))

	out, err := runKeyGenerateCmd(t, "", "--env-file="+path, "--overwrite")
	require.NoError(t, err)
	assert.NotContains(t, out, "already exists")
	assert.Contains(t, out, "(1 added, 1 replaced; secret HS256)")

	file, err := envfile.Load(path)
	require.NoError(t, err)
	secret, ok := file.Get("APP_SECRET_KEY")
	require.True(t, ok)
	assert.NotEqual(t, "stale", secret)
	assert.Equal(t, []string{"SERVER_HOST", "APP_SECRET_KEY", "AUTH_SECRET_KEY"},
		file.Keys())
}

func TestKeyGenerateReportsAlgorithms(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")

	out, err := runKeyGenerateCmd(t, "", "--env-file="+path)
	require.NoError(t, err)
	assert.Contains(t, out, "secret HS256)")

	out, err = runKeyGenerateCmd(t, "y\n", "--env-file="+path, "--algorithm=HS512")
	require.NoError(t, err)
	assert.Contains(t, out, "secret HS512)")

	_, err = runKeyGenerateCmd(t, "y\n", "--env-file="+path, "--algorithm=ES384")
	require.ErrorIs(t, err, crypto.ErrUnsupportedAlgorithm,
		"the key pair's algorithm is the database's (jwks:generate), not the secret's")
}

func TestKeyGenerateFailsOnUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", ".env.local")
	_, err := runKeyGenerateCmd(t, "", "--env-file="+path)
	assert.Error(t, err, "a missing parent directory must fail rather than be created silently")
}

// hostOf reads HOST from a parsed dotenv file.
func hostOf(t *testing.T, file *envfile.File) string {
	t.Helper()
	host, ok := file.Get("SERVER_HOST")
	require.True(t, ok)
	return host
}
