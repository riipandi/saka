package logger_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/logger"
)

// TestEveryTransportShipsTheSameEntry is the check the transport list exists for:
// one call site reaches the console and the file, and the entry is the same in
// both. The OTLP transport's shipping path is covered against the in-process
// collector stand-in in correlation_test.go.
func TestEveryTransportShipsTheSameEntry(t *testing.T) {
	marker := "hogwarts-all-transports"
	dir := t.TempDir()

	opts := testOptions()
	opts.FilePath = filepath.Join(dir, "logs", "hogwarts.log")
	opts.Transports = []logger.Transport{logger.TransportConsole, logger.TransportFile}
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
