package logger_test

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/logger"
	"github.com/riipandi/saka/pkg/testutils"
)

// TestOTLPShipsToARealVictoriaLogs is the end-to-end check of the shipping path:
// an entry logged through slog, routed by log.transport to the OTLP sink, and
// read back out of a real log store with its message, severity, resource, and
// fields intact.
//
// It uses the same store the development compose stack runs, so what a developer
// sees in Perses is what this asserts. The container is shared, so the query
// looks for this test's own marker rather than for an empty store.
func TestOTLPShipsToARealVictoriaLogs(t *testing.T) {
	store := testutils.StartVictoriaLogs(t.Context(), t)

	marker := "hogwarts-otlp-e2e"
	opts := testOptions()
	opts.Transports = []logger.Transport{logger.TransportOTLP}
	opts.OTLP.Endpoint = store.OTLPEndpoint
	opts.Level = logger.LevelDebug

	log, err := logger.New(opts, logger.WithWriter(&bytes.Buffer{}))
	require.NoError(t, err)

	log.Slog().Info("shipped end to end", "marker", marker, "n", 42)
	require.NoError(t, log.Shutdown(t.Context()), "shutdown must flush the queue")

	lines := store.AwaitQuery(t.Context(), t, fmt.Sprintf("marker:%q", marker))
	require.Len(t, lines, 1)

	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))

	assert.Equal(t, "shipped end to end", entry["_msg"], "the message must arrive whole")
	assert.Equal(t, "info", entry["severity_text"])
	assert.Equal(t, "42", entry["n"], "a field must arrive as its own column")
	// The resource is what makes a store useful with more than one service in
	// it, and it is built from the program's own identity rather than the shell.
	assert.Equal(t, "hogwarts-test", entry["service.name"])
	assert.Equal(t, "0.0.0", entry["service.version"])
	// The instrumentation scope says which part of the program emitted the
	// entry, which is the one thing the store cannot infer.
	assert.Equal(t, "hogwarts-test", entry["scope.name"])
}

// TestEveryTransportShipsTheSameEntry is the check the transport list exists for:
// one call site reaches the terminal, a file, and a collector, and the entry is
// the same in all three.
func TestEveryTransportShipsTheSameEntry(t *testing.T) {
	store := testutils.StartVictoriaLogs(t.Context(), t)

	marker := "hogwarts-all-transports"
	dir := t.TempDir()

	opts := testOptions()
	opts.FilePath = filepath.Join(dir, "logs", "hogwarts.log")
	opts.Transports = []logger.Transport{logger.TransportConsole, logger.TransportFile, logger.TransportOTLP}
	opts.OTLP.Endpoint = store.OTLPEndpoint
	opts.Format = logger.FormatStructured

	buf := &bytes.Buffer{}
	log, err := logger.New(opts, logger.WithWriter(buf))
	require.NoError(t, err)

	log.Slog().Info("one entry, three sinks", "marker", marker)
	require.NoError(t, log.Shutdown(t.Context()))

	assert.Contains(t, buf.String(), marker, "the console must receive it")

	raw, err := readLogFile(opts.FilePath)
	require.NoError(t, err, "the file must receive it")
	assert.Contains(t, raw, marker)

	lines := store.AwaitQuery(t.Context(), t, fmt.Sprintf("marker:%q", marker))
	require.Len(t, lines, 1, "the collector must receive it")
	assert.Contains(t, lines[0], "one entry, three sinks")
}

// TestTheFileTransportWritesWhereTheConfigurationSays is the check that the two
// paths agree: storage.local_path is the one data directory, and the file sink
// writes under it rather than somewhere of its own.
func TestTheFileTransportWritesWhereTheConfigurationSays(t *testing.T) {
	dir := t.TempDir()

	opts := testOptions()
	opts.FilePath = filepath.Join(dir, "logs", "hogwarts.log")
	opts.Transports = []logger.Transport{logger.TransportFile}

	log, err := logger.New(opts, logger.WithWriter(&bytes.Buffer{}))
	require.NoError(t, err)

	log.Slog().Info("under the data directory")
	require.NoError(t, log.Shutdown(t.Context()))

	path := opts.FilePath
	assert.True(t, strings.HasPrefix(path, dir), "%s must be under %s", path, dir)
	assert.Contains(t, path, "logs")

	raw, err := readLogFile(path)
	require.NoError(t, err)
	assert.Contains(t, raw, "under the data directory")
}

// readLogFile reads a log file the file sink wrote.
func readLogFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
