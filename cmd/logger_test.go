package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
)

func TestLoggerFromBuildsOneLoggerPerRun(t *testing.T) {
	// The state is installed once by initConfig and read by every command that
	// logs, so a second call must hand back the same logger: two loggers would
	// mean two file descriptors on the same path and a flush that races itself.
	ctx := contextWithLogger(t)

	first, err := loggerFrom(ctx)
	require.NoError(t, err)

	second, err := loggerFrom(ctx)
	require.NoError(t, err)

	assert.Same(t, first, second)
	require.NoError(t, closeLogger(ctx))
}

func TestLoggerFromReportsAMissingConfiguration(t *testing.T) {
	// A command that asks for a logger on a run whose configuration did not
	// resolve is told why, rather than handed a nil it would dereference.
	ctx := context.WithValue(context.Background(), configKey{},
		configState{err: errors.New("config: no config file")})
	ctx = installLogger(ctx, nil)

	_, err := loggerFrom(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config")
}

// contextWithLogger installs the logger state the way initConfig does, without
// going through a command, so the accessors can be exercised directly.
func contextWithLogger(t *testing.T) context.Context {
	t.Helper()

	cfg := config.Default()
	cfg.Log.Transport = []string{config.LogTransportConsole}

	ctx := context.WithValue(context.Background(), configKey{}, configState{cfg: cfg})
	return installLogger(ctx, nil)
}
