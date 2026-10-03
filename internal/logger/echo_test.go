package logger_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/logger"
)

// TestConsolelessTransportsStillEchoWarnings pins the echo sink: a run whose
// transports do not name the console is silent on the terminal by
// configuration, but a warning or an error still reaches the operator, while
// the routine entries a file-only deployment chose to keep off the terminal
// do not.
func TestConsolelessTransportsStillEchoWarnings(t *testing.T) {
	cfg := config.Default()
	cfg.Log.Transport = []string{config.LogTransportFile}
	cfg.Storage.LocalPath = t.TempDir()

	out := &bytes.Buffer{}
	echo := &bytes.Buffer{}
	log, err := logger.New(cfg, logger.WithWriter(out), logger.WithEchoWriter(echo))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Shutdown(context.Background())) })

	log.Slog().Info("routine")
	log.Slog().Warn("something is wrong")
	log.Flush()

	assert.Contains(t, echo.String(), "something is wrong",
		"the terminal must hear a warning even without a console transport")
	assert.NotContains(t, echo.String(), "routine",
		"the echo must not become a second copy of every entry")
}

// TestEchoOnlyWhenConsoleAbsent pins the other side: when the console is
// named, the entries already reach the terminal and the echo sink must stay
// out of the way, or a warning would print twice.
func TestEchoOnlyWhenConsoleAbsent(t *testing.T) {
	cfg := config.Default()
	cfg.Log.Transport = []string{config.LogTransportConsole}

	out := &bytes.Buffer{}
	echo := &bytes.Buffer{}
	log, err := logger.New(cfg, logger.WithWriter(out), logger.WithEchoWriter(echo))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Shutdown(context.Background())) })

	log.Slog().Warn("heard once")
	log.Flush()

	assert.Empty(t, echo.String(),
		"the console already carries the entry; the echo would duplicate it")
	assert.Contains(t, out.String(), "heard once")
}
