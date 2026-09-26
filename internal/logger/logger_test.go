package logger_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"encoding/json/jsontext"
	"encoding/json/v2"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/logger"
)

// newLogger builds a logger from the default configuration with the console
// rendering captured in a buffer, so a test reads what a person would see.
func newLogger(t *testing.T, adjust func(*config.Config)) (*logger.Logger, *bytes.Buffer) {
	t.Helper()

	cfg := config.Default()
	if adjust != nil {
		adjust(&cfg)
	}

	buf := &bytes.Buffer{}
	log, err := logger.New(cfg, logger.WithWriter(buf))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Shutdown(context.Background())) })

	return log, buf
}

// newFileLogger builds a logger that writes to a rotating file under a temporary
// data directory, and returns the path it writes to.
//
// The path comes from logger.LogFilePath, not from a second guess here: the sink
// derives it from storage.local_path, so a test that spelled it out again would
// pass while the two disagreed.
func newFileLogger(t *testing.T, adjust func(*config.Config)) (*logger.Logger, *bytes.Buffer, string) {
	t.Helper()

	dir := t.TempDir()
	buf := &bytes.Buffer{}

	cfg := config.Default()
	cfg.Storage.LocalPath = dir
	cfg.Log.Transport = []string{config.LogTransportConsole, config.LogTransportFile}
	if adjust != nil {
		adjust(&cfg)
	}

	log, err := logger.New(cfg, logger.WithWriter(buf))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Shutdown(context.Background())) })

	return log, buf, logger.LogFilePath(cfg)
}

func TestConsoleRendersEveryLevelTheConfigurationAllows(t *testing.T) {
	log, buf := newLogger(t, func(cfg *config.Config) { cfg.Log.Level = config.LogDebug })

	log.Slog().Debug("debug line")
	log.Slog().Info("info line")
	log.Slog().Warn("warn line")
	log.Slog().Error("error line")
	log.Flush()

	out := buf.String()
	for _, message := range []string{"debug line", "info line", "warn line", "error line"} {
		assert.Contains(t, out, message)
	}
}

func TestConfiguredLevelIsTheLoggerThreshold(t *testing.T) {
	// The threshold lives on the logger, not on a sink, so every destination
	// agrees on what is worth emitting: a file that receives what the console
	// dropped would be a second answer to the same question.
	log, buf := newLogger(t, func(cfg *config.Config) { cfg.Log.Level = config.LogWarn })

	log.Slog().Debug("dropped debug")
	log.Slog().Info("dropped info")
	log.Slog().Warn("kept warn")
	log.Flush()

	out := buf.String()
	assert.NotContains(t, out, "dropped debug")
	assert.NotContains(t, out, "dropped info")
	assert.Contains(t, out, "kept warn")
}

func TestStructuredFormatWritesOneJSONObjectPerLine(t *testing.T) {
	// The slog frontend is the one the application calls, so a field added
	// through slog must survive into the entry the transports write.
	log, buf := newLogger(t, func(cfg *config.Config) { cfg.Log.Format = config.LogStructured })

	log.Slog().Info("served", "user", "alice", "status", 200)
	log.Flush()

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 1)

	var entry map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &entry))
	assert.Equal(t, "served", entry["msg"])
	assert.Equal(t, "info", entry["level"])
	assert.Equal(t, "alice", entry["user"], "an attribute must survive as a field")
	assert.Equal(t, float64(200), entry["status"])
}

func TestSlogChainCarriesFieldsThroughThePipeline(t *testing.T) {
	// Both slog idioms reach the transports, because the handler behind the
	// logger is what implements them: With(...) accumulates attributes on a
	// derived handler and every later record carries them, while WithGroup(...)
	// nests what follows under one key. Feature code is expected to use these
	// rather than reaching for the LogLayer core, so the path is asserted here
	// rather than assumed.
	log, buf := newLogger(t, func(cfg *config.Config) { cfg.Log.Format = config.LogStructured })

	request := log.Slog().With("request_id", "abc123")
	request.Info("first")
	request.WithGroup("user").Info("second", "id", 42)
	log.Flush()

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 2)

	var first, second map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &first))
	require.NoError(t, json.Unmarshal(lines[1], &second))

	assert.Equal(t, "abc123", first["request_id"],
		"an attribute added through With must reach every later record")
	assert.Equal(t, "abc123", second["request_id"],
		"a derived logger must keep the attribute its parent set")
	assert.Equal(t, "second", second["msg"])
	user, ok := second["user"].(map[string]any)
	require.True(t, ok, "WithGroup must nest what follows under its key")
	assert.Equal(t, float64(42), user["id"])
}

func TestConsoleStaysPlainTextOffATerminal(t *testing.T) {
	// A redirected run must not carry escape codes: the output is a log, and a
	// reader that greps it would see the codes as text.
	log, buf := newLogger(t, nil)

	log.Slog().Info("plain")
	log.Flush()

	assert.NotContains(t, buf.String(), "\x1b[")
}

func TestErrorsAreLoggedAsRenderedText(t *testing.T) {
	// The error slot LogLayer's serializer renders is reachable only through
	// LogLayer's own API, which feature code does not hold — the official slog
	// integration maps every attribute onto fields, and a raw error value
	// there encodes as an empty object. The convention is therefore the shape
	// LogLayer's default serializer emits: the rendered text under err.
	log, buf := newLogger(t, func(cfg *config.Config) { cfg.Log.Format = config.LogStructured })

	err := errors.New("connection refused")
	log.Slog().Error("failed", "err", err.Error())
	log.Flush()

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "connection refused", entry["err"])
}

// TestSourceIsADebugOnlyField pins the sensitivity rule: the function, file,
// and line of the emitting call reach the sinks only at debug level, because
// a file path tells a reader where the code lives.
func TestSourceIsADebugOnlyField(t *testing.T) {
	log, buf := newLogger(t, func(cfg *config.Config) {
		cfg.Log.Format = config.LogStructured
		cfg.Log.Level = config.LogDebug
	})

	log.Slog().Info("at info", "n", 1)
	log.Slog().Debug("at debug", "n", 2)
	log.Flush()

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 2)

	var info, debug map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &info))
	require.NoError(t, json.Unmarshal(lines[1], &debug))

	assert.NotContains(t, info, "source",
		"an info entry must not carry the call site")
	assert.Contains(t, debug, "source",
		"a debug entry is where the call site belongs")
}

func TestTheDefaultConfigurationWritesOnlyToTheConsole(t *testing.T) {
	// A fresh checkout writes no files and dials nothing, so nothing has to be
	// installed before the application runs. This is the default the docs
	// promise, and the one a container that logs to stdout relies on.
	assert.Equal(t, []string{config.LogTransportConsole}, config.Default().Log.Transport)

	dir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.LocalPath = dir

	buf := &bytes.Buffer{}
	log, err := logger.New(cfg, logger.WithWriter(buf))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Shutdown(context.Background())) })

	log.Slog().Info("console only")
	log.Flush()

	assert.Contains(t, buf.String(), "console only")
	assert.NoDirExists(t, filepath.Join(dir, config.LogDir),
		"a run that did not name the file transport must not create its directory")
}

func TestFileTransportWritesUnderTheDataDirectory(t *testing.T) {
	// The sink's path is storage.local_path + /logs, so the configuration has one
	// answer for where this process keeps its files.
	log, _, path := newFileLogger(t, nil)

	log.Slog().Info("written to disk", "n", 42)
	require.NoError(t, log.Shutdown(context.Background()))

	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the sink creates the directory tree it was given")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(raw, &entry))
	assert.Equal(t, "written to disk", entry["msg"])
	assert.Equal(t, float64(42), entry["n"])
}

func TestFileSinkKeepsItsOwnJSONForm(t *testing.T) {
	// The console format is what a person reads; a file is read by a machine, so
	// asking for a pretty console must not put escape codes and column padding
	// in the file.
	log, buf, path := newFileLogger(t, func(cfg *config.Config) { cfg.Log.Format = config.LogPretty })

	log.Slog().Info("both sinks")
	require.NoError(t, log.Shutdown(context.Background()))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, jsontext.Value(raw).IsValid(), "the file must hold JSON: %s", raw)
	assert.NotContains(t, string(raw), "\x1b[")
	assert.Contains(t, buf.String(), "both sinks")
}

func TestShutdownIsIdempotent(t *testing.T) {
	// A shutdown path runs once on the happy path and again from a deferred
	// cleanup, so a second call must not fail or write to a closed file.
	log, _, _ := newFileLogger(t, nil)

	require.NoError(t, log.Shutdown(context.Background()))
	require.NoError(t, log.Shutdown(context.Background()))
}

func TestSetDefaultInstallsTheProcessLogger(t *testing.T) {
	// A dependency that logs through slog without being handed a logger must
	// land in the same pipeline, which is what makes one stack enough.
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })

	log, buf := newLogger(t, nil)
	log.SetDefault()

	slog.Info("from the package default")
	log.Flush()

	assert.Contains(t, buf.String(), "from the package default")
}

func TestUnknownTransportFailsConstruction(t *testing.T) {
	// Validate refuses a name no sink matches, so this is only reachable from a
	// configuration nobody checked. It still fails rather than skipping the
	// entry: a sink that was asked for and quietly not built is the failure the
	// transport list exists to prevent.
	cfg := config.Default()
	cfg.Log.Transport = []string{"syslog"}

	_, err := logger.New(cfg, logger.WithWriter(&bytes.Buffer{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "syslog")
}

func TestNoTransportFailsConstruction(t *testing.T) {
	cfg := config.Default()
	cfg.Log.Transport = nil

	_, err := logger.New(cfg, logger.WithWriter(&bytes.Buffer{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log.transport")
}

func TestUnsetOTLPVariableDoesNotReachTheSink(t *testing.T) {
	// The configuration is the only source of the endpoint. The exporter reads
	// OTEL_EXPORTER_OTLP_* on its own, so a stray variable in the environment
	// must not decide where a deployment ships its logs.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1/v1/logs")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer leaked")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=from-the-shell")

	server := newOTLPTestServer(t)
	cfg := config.Default()
	cfg.Log.Transport = []string{config.LogTransportConsole, config.LogTransportOTLP}
	cfg.OTEL.Endpoint = server.URL

	log, err := logger.New(cfg, logger.WithWriter(&bytes.Buffer{}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Shutdown(context.Background())) })

	log.Slog().Info("shipped")
	require.NoError(t, log.Shutdown(context.Background()))

	received := server.requests()
	require.Len(t, received, 1)
	assert.Equal(t, "/v1/logs", received[0].path, "the protocol path comes from the config endpoint")
	assert.Empty(t, received[0].authorization, "a header from the environment must not be sent")
	assert.NotContains(t, string(received[0].body), "from-the-shell",
		"the resource is built here, not read from OTEL_RESOURCE_ATTRIBUTES")
	assert.Contains(t, string(received[0].body), config.AppIdentifier,
		"the service is the one this program names")
}

func TestOTLPSinkShipsToTheConfiguredEndpoint(t *testing.T) {
	server := newOTLPTestServer(t)

	cfg := config.Default()
	cfg.Log.Transport = []string{config.LogTransportOTLP}
	cfg.OTEL.Endpoint = server.URL

	log, err := logger.New(cfg, logger.WithWriter(&bytes.Buffer{}))
	require.NoError(t, err)

	log.Slog().Info("collected")
	require.NoError(t, log.Shutdown(context.Background()))

	received := server.requests()
	require.Len(t, received, 1)
	assert.NotEmpty(t, received[0].body, "the record must carry a protobuf payload")
}

func TestOTLPEndpointPathIsUsedWhenTheConfigurationNamesOne(t *testing.T) {
	server := newOTLPTestServer(t)

	cfg := config.Default()
	cfg.Log.Transport = []string{config.LogTransportOTLP}
	cfg.OTEL.Endpoint = server.URL + "/collector/v1/logs"

	log, err := logger.New(cfg, logger.WithWriter(&bytes.Buffer{}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Shutdown(context.Background())) })

	log.Slog().Info("routed")
	require.NoError(t, log.Shutdown(context.Background()))

	received := server.requests()
	require.Len(t, received, 1)
	assert.Equal(t, "/collector/v1/logs", received[0].path)
}

func TestOTLPHeadersReachTheCollector(t *testing.T) {
	// Headers are what a collector authenticating the sender reads, so the value
	// from the configuration must be the one on the wire. A header the
	// environment set must not be, which is the rule the whole package follows.
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer leaked")

	server := newOTLPTestServer(t)

	cfg := config.Default()
	cfg.Log.Transport = []string{config.LogTransportOTLP}
	cfg.OTEL.Endpoint = server.URL
	cfg.OTEL.Headers = map[string]string{"authorization": "Bearer configured"}

	log, err := logger.New(cfg, logger.WithWriter(&bytes.Buffer{}))
	require.NoError(t, err)

	log.Slog().Info("authenticated")
	require.NoError(t, log.Shutdown(context.Background()))

	received := server.requests()
	require.Len(t, received, 1)
	assert.Equal(t, "Bearer configured", received[0].authorization,
		"the configured header is the one sent, not one the shell set")
}

func TestAConfigurationThatOmitsOTLPDialsNothing(t *testing.T) {
	// The default configuration ships nowhere: a local checkout must run with no
	// collector, and a sink the list does not name is never dialled — not even
	// when its endpoint is set, which is what keeps a stale address in the file
	// harmless.
	server := newOTLPTestServer(t)

	log, _ := newLogger(t, func(cfg *config.Config) {
		cfg.Log.Transport = []string{config.LogTransportConsole}
		cfg.OTEL.Endpoint = server.URL
	})
	log.Slog().Info("local only")
	require.NoError(t, log.Shutdown(context.Background()))

	assert.Empty(t, server.requests())
}

func TestEveryNamedSinkReceivesTheEntry(t *testing.T) {
	// Naming several sinks is the point of the list: one entry reaches all of
	// them, which is what lets a run keep a readable terminal and ship the same
	// lines to a collector.
	server := newOTLPTestServer(t)

	log, buf, path := newFileLogger(t, func(cfg *config.Config) {
		cfg.Log.Transport = []string{
			config.LogTransportConsole,
			config.LogTransportFile,
			config.LogTransportOTLP,
		}
		cfg.OTEL.Endpoint = server.URL
	})

	log.Slog().Info("everywhere", "n", 7)
	require.NoError(t, log.Shutdown(context.Background()))

	assert.Contains(t, buf.String(), "everywhere", "the console must receive it")

	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the file must receive it")
	assert.Contains(t, string(raw), "everywhere")

	received := server.requests()
	require.Len(t, received, 1, "the collector must receive it")
	assert.NotEmpty(t, received[0].body)
}
