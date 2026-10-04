package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSkipsTheCacheWhileDisabled(t *testing.T) {
	opts := Options{Enable: false}

	c := New(opts, nil)

	_, ok := c.Get(t.Context(), nil, "k")
	assert.False(t, ok)
	c.Set(t.Context(), "k", []byte("v"), 0)
	c.Del(t.Context(), "k")
}

func TestNewSkipsTheCacheWhileTheBackendIsOff(t *testing.T) {
	// The cache is wanted, the kvstore driver is named, and the backend it
	// needs is not enabled: the run skips caching rather than failing to
	// start. No backend client is needed to make that decision.
	opts := Options{Enable: true, Driver: DriverKVStore}

	assert.IsType(t, Noop{}, New(opts, nil))
}

func TestNewPicksTheMemoryDriver(t *testing.T) {
	opts := Options{Enable: true, Driver: DriverMemory}

	c := New(opts, nil)
	require.IsType(t, &Memory{}, c)

	m := c.(*Memory)
	budget := int64(0)
	for i := range m.shards {
		budget += int64(m.shards[i].maxMemory)
	}
	assert.GreaterOrEqual(t, budget, int64(32<<20),
		"the shards carry the whole budget, rounded up to whole chunks")
}

func TestNoopIsACache(t *testing.T) {
	var c Cache = Noop{}

	value, ok := c.Get(t.Context(), nil, "k")
	assert.False(t, ok)
	assert.Empty(t, value)
	// Writes and deletes are dropped, not errors: a run without a cache
	// keeps its shape.
	c.Set(t.Context(), "k", []byte("v"), time.Minute)
	c.Del(t.Context(), "k")
}
