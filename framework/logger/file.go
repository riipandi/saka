package logger

import (
	"fmt"

	"go.loglayer.dev/transports/lumberjack/v3"
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
func newFileSink(opts Options) (*fileSink, error) {
	rotator, err := lumberjack.Build(lumberjack.Config{
		Filename:   opts.FilePath,
		MaxSize:    opts.File.MaxSize,
		MaxBackups: opts.File.MaxBackups,
		MaxAge:     opts.File.MaxAge,
		Compress:   opts.File.Compress,
		ID:         "file",
	})
	if err != nil {
		return nil, fmt.Errorf("logger: file sink: %w", err)
	}
	return &fileSink{async: newAsyncTransport(rotator, nil)}, nil
}
