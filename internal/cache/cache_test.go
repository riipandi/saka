package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
)

func TestNewSkipsTheCacheWhileDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Cache.Enable = false

	c := New(cfg, nil)

	_, ok := c.Get(t.Context(), nil, "k")
	assert.False(t, ok)
	c.Set(t.Context(), "k", []byte("v"), 0)
	c.Del(t.Context(), "k")
}

func TestNewSkipsTheCacheWhileTheBackendIsOff(t *testing.T) {
	// The cache is wanted, the kvstore driver is named, and the backend it
	// needs is not enabled: the run skips caching rather than failing to
	// start. No backend client is needed to make that decision.
	cfg := config.Default()
	cfg.Cache.Enable = true
	cfg.Cache.Driver = config.CacheKV
	cfg.KVStore.Enable = false

	assert.IsType(t, Noop{}, New(cfg, nil))
}

func TestNewPicksTheMemoryDriver(t *testing.T) {
	cfg := config.Default()
	cfg.Cache.Enable = true
	cfg.Cache.Driver = config.CacheMemory

	c := New(cfg, nil)
	require.IsType(t, &Memory{}, c)

	m := c.(*Memory)
	budget := int64(0)
	for i := range m.shards {
		budget += int64(m.shards[i].maxMemory)
	}
	assert.GreaterOrEqual(t, budget, cfg.Cache.MaxMemory,
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
