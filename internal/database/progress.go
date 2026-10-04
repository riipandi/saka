package database

import (
	"context"
	"log/slog"
	"maps"
	"time"
)

// ProgressState is where a migration stands in a run.
type ProgressState string

const (
	// ProgressStarted means the migration is running. It is reported once per
	// migration, before its first statement.
	ProgressStarted ProgressState = "started"
	// ProgressApplied means the migration ran and changed the schema.
	ProgressApplied ProgressState = "applied"
	// ProgressEmpty means the migration ran but holds no statements.
	ProgressEmpty ProgressState = "empty"
	// ProgressRolledBack means the migration's Down block ran.
	ProgressRolledBack ProgressState = "rolled back"
)

// ProgressEvent is one step of a migration run, reported through
// MigratorOptions.Progress as it happens rather than at the end.
type ProgressEvent struct {
	Version   int64
	Name      string
	Direction string
	State     ProgressState
	Duration  time.Duration
}

// gooseMessage names the log records this package translates. Everything else
// goose reports is dropped: its remaining records restate what the caller
// already prints from the returned results.
const (
	gooseMessageStatement = "executing statement"
	gooseMessageCompleted = "migration completed"
)

// progressHandler turns goose's verbose log records into ProgressEvents.
//
// goose reports one record per statement and one per completed migration, but
// nothing when a migration starts. The first statement of a migration is what
// marks it as running, so this handler emits ProgressStarted once per migration
// and ignores the statements that follow.
//
// Only the two records above are translated. A goose release that renames one
// costs the live per-migration line and nothing else, because the caller's own
// summary comes from the returned results.
//
// A handler is not safe for concurrent use. goose runs migrations sequentially,
// so one handler serves one run.
type progressHandler struct {
	emit  func(ProgressEvent)
	attrs map[string]slog.Value
}

// Enabled reports that every level is accepted. goose logs the records this
// handler wants at info level, and filtering happens by message instead.
func (h *progressHandler) Enabled(context.Context, slog.Level) bool { return true }

// WithAttrs returns a handler that carries the extra attributes, so a record
// keeps the fields goose attached to the logger as well as its own.
func (h *progressHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make(map[string]slog.Value, len(h.attrs)+len(attrs))
	maps.Copy(merged, h.attrs)
	for _, attr := range attrs {
		merged[attr.Key] = attr.Value
	}
	return &progressHandler{emit: h.emit, attrs: merged}
}

// WithGroup returns the handler unchanged. goose logs no groups, so there is
// nothing to nest.
func (h *progressHandler) WithGroup(string) slog.Handler { return h }

// Handle reports the record as a progress event, or drops it.
func (h *progressHandler) Handle(_ context.Context, record slog.Record) error {
	event, ok := h.event(record)
	if ok {
		h.emit(event)
	}
	return nil
}

// event converts one record. ok is false for a record this package does not
// translate.
func (h *progressHandler) event(record slog.Record) (ProgressEvent, bool) {
	if record.Message != gooseMessageStatement && record.Message != gooseMessageCompleted {
		return ProgressEvent{}, false
	}

	attrs := make(map[string]slog.Value, len(h.attrs)+record.NumAttrs())
	maps.Copy(attrs, h.attrs)
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value
		return true
	})

	event := ProgressEvent{
		Version:   attrs["version"].Int64(),
		Name:      attrs["source"].String(),
		Direction: attrs["direction"].String(),
	}

	if record.Message == gooseMessageStatement {
		event.State = ProgressStarted
		return event, true
	}

	event.Duration = time.Duration(attrs["duration_seconds"].Float64() * float64(time.Second))
	event.State = ProgressApplied
	if attrs["state"].String() == "empty" {
		event.State = ProgressEmpty
	}
	if event.Direction == "down" {
		event.State = ProgressRolledBack
	}
	return event, true
}
