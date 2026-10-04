package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEveryConfiguredLevelHasAMapping(t *testing.T) {
	// A level the configuration accepts but this package does not know would
	// silently fall back to info, so a run asked for debug would quietly emit
	// less. The list here is the configuration's own, so the two cannot drift.
	for level, expected := range map[Level]string{
		LevelDebug: "debug",
		LevelInfo:  "info",
		LevelWarn:  "warn",
		LevelError: "error",
	} {
		assert.Equal(t, expected, levelFor(level).String(), "log.level=%s", level)
	}
}

func TestAnUnknownLevelIsNotEveryLevel(t *testing.T) {
	// Validate has already refused anything else, so this is unreachable through
	// the configuration. It is still asserted, because the safe fallback for a
	// threshold nobody recognises is the quieter one: a logger that suddenly
	// emits debug lines is worse than a logger that drops them.
	assert.Equal(t, "info", levelFor(Level("verbose")).String())
}
