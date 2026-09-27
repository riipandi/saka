package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forceCollision returns a hash that maps the named keys to distinct values
// and everything else to one shared value, so the collision path runs
// without hoping two random strings collide.
func forceCollision(keys ...string) func(string) uint64 {
	return func(key string) uint64 {
		for i, want := range keys {
			if key == want {
				return uint64(i + 1)
			}
		}
		return 0
	}
}

func TestMemoryRoundTripsAnEntry(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)

	c.Set(t.Context(), "user:1", []byte("ali"), 0)

	value, ok := c.Get(t.Context(), make([]byte, 0, 64), "user:1")
	require.True(t, ok)
	assert.Equal(t, "ali", string(value))
}

func TestMemoryMissReturnsWithoutAnEntry(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)

	value, ok := c.Get(t.Context(), make([]byte, 0, 64), "absent")
	assert.False(t, ok)
	assert.Empty(t, value)
}

func TestMemoryOverwritesTheSameKey(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)

	c.Set(t.Context(), "k", []byte("first"), 0)
	c.Set(t.Context(), "k", []byte("second"), 0)

	value, ok := c.Get(t.Context(), nil, "k")
	require.True(t, ok)
	assert.Equal(t, "second", string(value))

	// A value of the same length overwrites in place; both paths must
	// answer the same.
	c.Set(t.Context(), "k", []byte("third"), 0)
	value, ok = c.Get(t.Context(), nil, "k")
	require.True(t, ok)
	assert.Equal(t, "third", string(value))
}

func TestMemoryDeletesOnlyTheRequestedKey(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)

	c.Set(t.Context(), "gone", []byte("1"), 0)
	c.Set(t.Context(), "stays", []byte("2"), 0)

	c.Del(t.Context(), "gone")

	_, ok := c.Get(t.Context(), nil, "gone")
	assert.False(t, ok)
	value, ok := c.Get(t.Context(), nil, "stays")
	require.True(t, ok)
	assert.Equal(t, "2", string(value))
}

func TestMemoryExpiresEntries(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)

	c.Set(t.Context(), "short", []byte("1"), 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	_, ok := c.Get(t.Context(), nil, "short")
	assert.False(t, ok, "an expired entry is a miss")

	c.Set(t.Context(), "long", []byte("1"), time.Hour)
	_, ok = c.Get(t.Context(), nil, "long")
	assert.True(t, ok)

	// The zero ttl takes the default, so a caller does not spell the
	// configured lifetime at every call site.
	c.Set(t.Context(), "default", []byte("1"), 0)
	_, ok = c.Get(t.Context(), nil, "default")
	assert.True(t, ok)
}

func TestMemoryKeepsBothKeysAcrossAHashCollision(t *testing.T) {
	// The property bigcache lacks: two keys that hash the same must both
	// keep working, because a lookup verifies the stored key bytes before
	// answering.
	const (
		left  = "session:alpha"
		right = "session:beta"
	)
	c := NewMemory(0, 5*time.Minute)
	c.hashFn = forceCollision(left, right)

	c.Set(t.Context(), left, []byte("value-a"), 0)
	c.Set(t.Context(), right, []byte("value-b"), 0)

	value, ok := c.Get(t.Context(), nil, left)
	require.True(t, ok, "the key that lost the primary slot must stay reachable")
	assert.Equal(t, "value-a", string(value))

	value, ok = c.Get(t.Context(), nil, right)
	require.True(t, ok)
	assert.Equal(t, "value-b", string(value))

	// Deleting one must not disturb the other.
	c.Del(t.Context(), left)
	_, ok = c.Get(t.Context(), nil, left)
	assert.False(t, ok)
	value, ok = c.Get(t.Context(), nil, right)
	require.True(t, ok)
	assert.Equal(t, "value-b", string(value))
}

func TestMemoryNeverConfusesTwoKeysUnderForcedCollisions(t *testing.T) {
	// Three keys sharing one hash: the third evicts the secondary, and the
	// remaining two must still answer for themselves and nobody else.
	c := NewMemory(0, 5*time.Minute)
	c.hashFn = func(string) uint64 { return 1 } // every key collides

	c.Set(t.Context(), "a", []byte("va"), 0)
	c.Set(t.Context(), "b", []byte("vb"), 0)
	c.Set(t.Context(), "c", []byte("vc"), 0)

	// "c" holds the primary slot and "b" the secondary; "a" was evicted by
	// the third write. Nothing may leak across keys.
	value, ok := c.Get(t.Context(), nil, "c")
	require.True(t, ok)
	assert.Equal(t, "vc", string(value))
	value, ok = c.Get(t.Context(), nil, "b")
	require.True(t, ok)
	assert.Equal(t, "vb", string(value))
	_, ok = c.Get(t.Context(), nil, "a")
	assert.False(t, ok, "a three-way collision evicts the secondary")
}

func TestMemoryGetIsAllocationFreeOnAHit(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)
	c.Set(t.Context(), "k", []byte("a short payload"), 0)

	dst := make([]byte, 0, 512)
	allocs := testing.AllocsPerRun(100, func() {
		value, ok := c.Get(t.Context(), dst, "k")
		if !ok || string(value) != "a short payload" {
			t.Fatal("unexpected cache answer")
		}
	})
	assert.Zero(t, allocs, "a hit must not allocate while dst has room")
}

func TestMemoryCleansItselfWhenTheBudgetIsSpent(t *testing.T) {
	// A tiny budget: one shard, one chunk, so the ring fills and the reset
	// runs while entries are still being written.
	c := NewMemory(memoryChunkSize, 5*time.Minute)

	for i := range 2000 {
		c.Set(t.Context(), fmt.Sprintf("key-%d", i), fmt.Appendf(nil, "value-%d", i), 0)
	}

	// The cache still answers: whatever survived the last reset is
	// readable, and nothing panicked or grew beyond the budget.
	chunks := 0
	for i := range c.shards {
		chunks += len(c.shards[i].chunks)
	}
	assert.LessOrEqual(t, chunks*memoryChunkSize, memoryChunkSize,
		"the ring must not grow past its budget")
}

func TestMemoryRefusesAnEntryLargerThanTheShard(t *testing.T) {
	c := NewMemory(memoryChunkSize, 5*time.Minute)
	c.Set(t.Context(), "kept", []byte("langdon"), 0)

	tooBig := make([]byte, memoryChunkSize)
	c.Set(t.Context(), "horcrux", tooBig, 0)

	got, ok := c.Get(t.Context(), nil, "kept")
	require.True(t, ok)
	assert.Equal(t, "langdon", string(got))
	_, ok = c.Get(t.Context(), nil, "horcrux")
	assert.False(t, ok, "an entry larger than the shard is not stored")
}

func TestMemoryRewindsBeforeAWriteThatDoesNotFit(t *testing.T) {
	c := NewMemory(memoryChunkSize, 5*time.Minute)
	// Leave less than the next value in the only chunk, so the write has to
	// rewind. The new bytes must come back whole.
	c.Set(t.Context(), "pad", make([]byte, memoryChunkSize-32), 0)
	value := []byte("sophie-neveu-comes-back-whole")
	c.Set(t.Context(), "tail", value, 0)

	got, ok := c.Get(t.Context(), nil, "tail")
	require.True(t, ok)
	assert.Equal(t, value, got)
}

func TestMemoryHandlesAnEntrySpanningChunks(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)

	// Large enough to cross the 64 KiB chunk boundary, no matter where the
	// ring happens to stand.
	value := make([]byte, memoryChunkSize+1024)
	for i := range value {
		value[i] = byte(i)
	}
	c.Set(t.Context(), "big", value, 0)

	got, ok := c.Get(t.Context(), nil, "big")
	require.True(t, ok)
	assert.Equal(t, value, got, "an entry that spans chunks reads back whole")
}

func TestMemorySurvivesConcurrentUse(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			dst := make([]byte, 0, 128)
			for i := range 200 {
				key := fmt.Sprintf("g%d-k%d", g, i)
				c.Set(t.Context(), key, []byte(key), 0)
				value, ok := c.Get(t.Context(), dst, key)
				if !ok || string(value) != key {
					t.Errorf("concurrent get of %s: %q (found=%v)", key, value, ok)
				}
				c.Del(t.Context(), key)
			}
		}(g)
	}
	wg.Wait()
}

func TestNewMemoryScalesTheShardsWithTheBudget(t *testing.T) {
	// A small budget takes few shards, and every shard keeps at least one
	// chunk — a budget under one chunk per shard would leave a shard with
	// no room at all.
	assert.Equal(t, 1, len(NewMemory(memoryChunkSize, time.Minute).shards))
	assert.Equal(t, 2, len(NewMemory(2*memoryChunkSize, time.Minute).shards))
	assert.Equal(t, memoryShardCount, len(NewMemory(0, time.Minute).shards))
}

func TestMemoryBatchRoundTrips(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)

	c.SetMany(t.Context(), map[string][]byte{"a": []byte("1"), "b": []byte("2")}, 0)

	found := c.GetMany(t.Context(), []string{"a", "b", "absent"})
	assert.Equal(t, map[string][]byte{"a": []byte("1"), "b": []byte("2")}, found)

	c.DelMany(t.Context(), []string{"a", "b"})
	assert.Empty(t, c.GetMany(t.Context(), []string{"a", "b"}))
}

func TestMemoryResetDropsEveryEntry(t *testing.T) {
	c := NewMemory(0, 5*time.Minute)
	for i := range 10 {
		c.Set(t.Context(), fmt.Sprintf("k%d", i), []byte("v"), 0)
	}

	c.Reset()

	for i := range 10 {
		_, ok := c.Get(t.Context(), nil, fmt.Sprintf("k%d", i))
		assert.False(t, ok, "reset must drop every entry")
	}

	// The rings were wound back, not abandoned: the cache keeps working
	// and reusing the memory it grew.
	c.Set(t.Context(), "after", []byte("kept"), 0)
	value, ok := c.Get(t.Context(), nil, "after")
	require.True(t, ok)
	assert.Equal(t, "kept", string(value))
}
