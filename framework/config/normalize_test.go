package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSplitListReadsACommaSeparatedValue(t *testing.T) {
	// The form an environment variable carries, which is what a directive
	// resolves to.
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, splitList("alpha,beta,gamma"))
	assert.Equal(t, []string{"alpha", "beta"}, splitList("alpha, beta"),
		"space around an entry is dropped")
	assert.Equal(t, []string{"alpha"}, splitList("alpha"))
	assert.Equal(t, []string{"alpha"}, splitList(" alpha "))
	assert.Equal(t, []string{"alpha", "beta"}, splitList("alpha,,beta"),
		"an empty entry names nothing and is dropped")
	assert.Empty(t, splitList(""), "an empty value is an empty list, not one empty name")
	assert.Empty(t, splitList(" , "))
}

func TestNormalizeListsLeavesAJSONArrayAlone(t *testing.T) {
	// A config file writes the list as an array, which is already the shape the
	// field wants; only a string needs splitting.
	keys := map[string]any{"widget.tags": []any{"alpha", "beta"}}

	NormalizeLists(keys, []string{"widget.tags"})

	assert.Equal(t, []any{"alpha", "beta"}, keys["widget.tags"])
}

func TestNormalizeListsTouchesNoOtherKey(t *testing.T) {
	// A comma-separated string under a key that is not a list is an ordinary
	// value, and splitting it would silently change it.
	keys := map[string]any{"widget.name": "one,two"}

	NormalizeLists(keys, []string{"widget.tags"})

	assert.Equal(t, "one,two", keys["widget.name"])
}

func TestSplitMapReadsTheSpecificationForm(t *testing.T) {
	// The form the OpenTelemetry specification uses for its own headers
	// variable, which is what a directive resolves to.
	assert.Equal(t,
		map[string]string{"authorization": "Bearer x", "x-tenant": "acme"},
		splitMap("authorization=Bearer x,x-tenant=acme"))
	assert.Equal(t,
		map[string]string{"authorization": "Bearer x"},
		splitMap(" authorization = Bearer x "),
		"space around a name or a value is dropped")
	assert.Equal(t,
		map[string]string{"x-empty": ""},
		splitMap("x-empty="),
		"an empty value is a header with no value, which is legal")
	assert.Empty(t, splitMap(""), "an empty value carries no header")
	assert.Empty(t, splitMap("authorization"),
		"an entry with no = names no value and is dropped rather than sent")
	assert.Equal(t,
		map[string]string{"x-tenant": "acme"},
		splitMap("=,x-tenant=acme"),
		"a nameless entry is dropped rather than becoming an empty header name")
}

func TestNormalizeMapsLeavesAJSONObjectAlone(t *testing.T) {
	// A config file writes the map as an object, which is already the shape the
	// field wants; only a string needs splitting.
	keys := map[string]any{"widget.labels": map[string]any{"authorization": "Bearer x"}}

	NormalizeMaps(keys, []string{"widget.labels"})

	assert.Equal(t, map[string]any{"authorization": "Bearer x"}, keys["widget.labels"])
}

func TestNormalizeMapsTouchesNoOtherKey(t *testing.T) {
	// A string with an = under a key that is not a map is an ordinary value, and
	// splitting it would silently change it.
	keys := map[string]any{"widget.name": "https://example.com?a=b"}

	NormalizeMaps(keys, []string{"widget.labels"})

	assert.Equal(t, "https://example.com?a=b", keys["widget.name"])
}

func TestNormalizeDurationsReadsSecondsAndLeavesStringsAlone(t *testing.T) {
	keys := map[string]any{"widget.size": 900, "widget.name": 5}

	NormalizeDurations(keys, []string{"widget.size"})

	assert.Equal(t, int64(900*time.Second), int64(keys["widget.size"].(time.Duration)))
	assert.Equal(t, 5, keys["widget.name"], "a number under another key stays a number")

	keys = map[string]any{"widget.size": "15m"}
	NormalizeDurations(keys, []string{"widget.size"})
	assert.Equal(t, "15m", keys["widget.size"], "a quoted value is a duration string for the decoder")
}
