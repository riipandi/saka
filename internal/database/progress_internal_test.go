package database

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The handler is unexported, so its translation table is tested from inside the
// package. pkg/testutils does not import this package, so there is no cycle.

// record builds a log record with the given attributes.
func record(message string, attrs map[string]slog.Value) slog.Record {
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, message, 0)
	for key, value := range attrs {
		rec.AddAttrs(slog.Attr{Key: key, Value: value})
	}
	return rec
}

// A migration with no statements must report empty, which is how a fresh
// skeleton is shown.
func TestProgressHandlerReportsEmptyState(t *testing.T) {
	var events []ProgressEvent
	handler := &progressHandler{emit: func(event ProgressEvent) { events = append(events, event) }}

	require.NoError(t, handler.Handle(t.Context(), record("migration completed", map[string]slog.Value{
		"version":          slog.Int64Value(4),
		"source":           slog.StringValue("00004_empty.sql"),
		"direction":        slog.StringValue("up"),
		"state":            slog.StringValue("empty"),
		"duration_seconds": slog.Float64Value(0.5),
	})))

	require.Len(t, events, 1)
	assert.Equal(t, ProgressEmpty, events[0].State)
	assert.Equal(t, int64(4), events[0].Version)
	assert.Equal(t, "00004_empty.sql", events[0].Name)
	assert.Equal(t, 500*time.Millisecond, events[0].Duration)
}

// A rollback is reported by direction, so the report never claims a migration
// was applied when it was removed.
func TestProgressHandlerReportsRollback(t *testing.T) {
	var events []ProgressEvent
	handler := &progressHandler{emit: func(event ProgressEvent) { events = append(events, event) }}

	require.NoError(t, handler.Handle(t.Context(), record("migration completed", map[string]slog.Value{
		"version":          slog.Int64Value(2),
		"source":           slog.StringValue("00002_x.sql"),
		"direction":        slog.StringValue("down"),
		"state":            slog.StringValue("applied"),
		"duration_seconds": slog.Float64Value(0.02),
	})))

	require.Len(t, events, 1)
	assert.Equal(t, ProgressRolledBack, events[0].State)
}

// Records this package does not translate must be dropped, so goose noise never
// reaches the report.
func TestProgressHandlerDropsOtherRecords(t *testing.T) {
	var events []ProgressEvent
	handler := &progressHandler{emit: func(event ProgressEvent) { events = append(events, event) }}

	require.NoError(t, handler.Handle(t.Context(), record("successfully migrated database", map[string]slog.Value{
		"current_version": slog.Int64Value(9),
	})))
	require.NoError(t, handler.Handle(t.Context(), record("no migrations to run", map[string]slog.Value{
		"current_version": slog.Int64Value(9),
	})))

	assert.Empty(t, events)
}

// A started event carries no duration and must not invent one.
func TestProgressHandlerStartedHasNoDuration(t *testing.T) {
	var events []ProgressEvent
	handler := &progressHandler{emit: func(event ProgressEvent) { events = append(events, event) }}

	require.NoError(t, handler.Handle(t.Context(), record("executing statement", map[string]slog.Value{
		"version":   slog.Int64Value(2),
		"source":    slog.StringValue("00002_x.sql"),
		"direction": slog.StringValue("up"),
		"statement": slog.StringValue("SELECT 1;"),
	})))

	require.Len(t, events, 1)
	assert.Equal(t, ProgressStarted, events[0].State)
	assert.Zero(t, events[0].Duration)
}

// Attributes attached to the logger itself must be visible to the handler, or a
// record that carries only some fields would decode as zero values.
func TestProgressHandlerKeepsLoggerAttrs(t *testing.T) {
	var events []ProgressEvent
	handler := &progressHandler{emit: func(event ProgressEvent) { events = append(events, event) }}
	withAttrs := handler.WithAttrs([]slog.Attr{
		slog.String("source", "00003_x.sql"),
		slog.Int64("version", 3),
	})

	require.NoError(t, withAttrs.Handle(t.Context(), record("migration completed", map[string]slog.Value{
		"direction":        slog.StringValue("up"),
		"state":            slog.StringValue("applied"),
		"duration_seconds": slog.Float64Value(0.01),
	})))

	require.Len(t, events, 1)
	assert.Equal(t, int64(3), events[0].Version)
	assert.Equal(t, "00003_x.sql", events[0].Name)
}
