package logger

import (
	"fmt"
	"path/filepath"

	"go.loglayer.dev/transports/lumberjack/v3"

	"github.com/riipandi/saka/internal/config"
)

// fileSink is the rotating file transport behind the async worker the sinks
// a request writes to share. The rotator is held so Close can release the
// file descriptor it owns after the queue has drained.
type fileSink struct {
	async *asyncTransport
}

// newFileSink builds the rotating file sink the configuration names.
//
// The path is derived from storage.local_path rather than configured on its own:
// there is one data directory in this application, and a second path for the logs
// could disagree with it. A relative path in the configuration stays relative, so
// the sink writes where the working directory says, exactly as the local storage
// driver does.
//
// Rotation settings are passed through as configured; Validate has already
// refused the combination that never deletes a rotated file. No batch writer
// sits in front of the rotator: it writes through, so a size-based rotation
// never overshoots by more than one entry.
func newFileSink(cfg config.Config) (*fileSink, error) {
	rotator, err := lumberjack.Build(lumberjack.Config{
		Filename:   LogFilePath(cfg),
		MaxSize:    cfg.Log.File.MaxSize,
		MaxBackups: cfg.Log.File.MaxBackups,
		MaxAge:     cfg.Log.File.MaxAge,
		Compress:   cfg.Log.File.Compress,
		ID:         "file",
	})
	if err != nil {
		return nil, fmt.Errorf("logger: file sink: %w", err)
	}
	return &fileSink{async: newAsyncTransport(rotator, nil)}, nil
}

// LogFilePath is the active log file, under the one data directory of the
// process. It is exported because a command that reports where logs go — or a
// test that reads them — needs the same answer the sink uses, and re-deriving it
// at the call site is how two paths start to disagree.
func LogFilePath(cfg config.Config) string {
	return filepath.Join(dataDir(cfg), config.LogDir, config.LogFileName)
}

// dataDir resolves the application data directory from the configuration. It is
// Storage.LocalPath, the one path the local driver writes to; the layer already
// applied the precedence, so there is nothing to re-check here.
func dataDir(cfg config.Config) string {
	if cfg.Storage.LocalPath == "" {
		return config.DefaultDataDir
	}
	return cfg.Storage.LocalPath
}
