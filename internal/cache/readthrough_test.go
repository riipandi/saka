package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryDelPrefixRemovesOnlyTheFamily pins the prefix removal the
// invalidations depend on: the entries whose keys begin with the prefix go,
// every other entry — a sibling family, a key that merely contains the
// prefix after its first byte — stays.
func TestMemoryDelPrefixRemovesOnlyTheFamily(t *testing.T) {
	c := NewMemory(0, time.Minute)
	ctx := context.Background()

	c.Set(ctx, "authz:roles:a", []byte("1"), 0)
	c.Set(ctx, "authz:roles:b", []byte("2"), 0)
	c.Set(ctx, "authz:role:one", []byte("3"), 0)
	c.Set(ctx, "settings:public", []byte("4"), 0)

	c.DelPrefix(ctx, "authz:roles:")

	_, ok := c.Get(ctx, nil, "authz:roles:a")
	assert.False(t, ok, "the first entry of the family is gone")
	_, ok = c.Get(ctx, nil, "authz:roles:b")
	assert.False(t, ok, "the second entry of the family is gone")
	_, ok = c.Get(ctx, nil, "authz:role:one")
	assert.True(t, ok, "a sibling family sharing the prefix's head survives")
	_, ok = c.Get(ctx, nil, "settings:public")
	assert.True(t, ok, "another feature's entry survives")
}

// TestMemoryDelPrefixAcrossChunkBoundaries fills a shard past one 64 KiB
// chunk and removes a family whose entries straddle the boundary: the walk
// reads every stored key through the ring's spans, not through a second
// index, so the boundary costs nothing.
func TestMemoryDelPrefixAcrossChunkBoundaries(t *testing.T) {
	c := NewMemory(0, time.Minute)
	ctx := context.Background()

	value := make([]byte, 4096)
	for i := range value {
		value[i] = byte('a' + i%26)
	}
	for i := range 100 {
		c.Set(ctx, "family:"+string(rune('a'+i)), value, 0)
		c.Set(ctx, "other:"+string(rune('a'+i)), value, 0)
	}

	c.DelPrefix(ctx, "family:")

	for i := range 100 {
		key := "family:" + string(rune('a'+i))
		_, ok := c.Get(ctx, nil, key)
		assert.False(t, ok, "%s is gone", key)
	}
	_, ok := c.Get(ctx, nil, "other:a")
	assert.True(t, ok, "the other family survives the walk")
}

// textKey is an identifier that crosses Fetch's JSON boundary the way the
// wire-form identifiers the features cache do: as text, both ways.
type textKey struct{ raw string }

func (t textKey) MarshalText() ([]byte, error)  { return []byte(t.raw), nil }
func (t *textKey) UnmarshalText(b []byte) error { t.raw = string(b); return nil }

type textRow struct {
	ID   textKey
	Name string
}

// TestFetchRoundTripsThroughJSON pins the serialization Fetch owes its
// callers: a value carrying a text-marshalable identifier comes back with
// it intact, and a miss loads, stores, and answers the loader's value.
func TestFetchRoundTripsThroughJSON(t *testing.T) {
	c := NewMemory(0, time.Minute)
	ctx := context.Background()
	loads := 0

	load := func(context.Context) (textRow, error) {
		loads++
		return textRow{ID: textKey{raw: "role_abc"}, Name: "Gryffindor"}, nil
	}

	first, err := Fetch(ctx, c, "test:row", 0, false, load)
	require.NoError(t, err)
	assert.Equal(t, "role_abc", first.ID.raw)
	assert.Equal(t, 1, loads, "a miss loads")

	second, err := Fetch(ctx, c, "test:row", 0, false, load)
	require.NoError(t, err)
	assert.Equal(t, "role_abc", second.ID.raw, "the identifier survives the round trip")
	assert.Equal(t, 1, loads, "a hit does not load")
}

// TestFetchBypassLeavesTheCacheAlone pins the bypass's purity: the source
// answers, the loader runs, and the entry the last miss stored is neither
// read nor replaced — the callers reading the cache keep reading it.
func TestFetchBypassLeavesTheCacheAlone(t *testing.T) {
	c := NewMemory(0, time.Minute)
	ctx := context.Background()
	value := "first"

	load := func(context.Context) (string, error) {
		return value, nil
	}

	_, err := Fetch(ctx, c, "test:key", 0, false, load)
	require.NoError(t, err)

	value = "second"
	bypassed, err := Fetch(ctx, c, "test:key", 0, true, load)
	require.NoError(t, err)
	assert.Equal(t, "second", bypassed, "a bypassed call reads the source")

	cached, ok := c.Get(ctx, nil, "test:key")
	require.True(t, ok)
	assert.JSONEq(t, `"first"`, string(cached), "the bypass left the stored entry alone")
}

// TestFetchDoesNotCacheALoaderFailure pins what a failure costs: nothing is
// stored, so the next call reads the source again.
func TestFetchDoesNotCacheALoaderFailure(t *testing.T) {
	c := NewMemory(0, time.Minute)
	ctx := context.Background()

	loads := 0
	load := func(context.Context) (string, error) {
		loads++
		return "", assert.AnError
	}

	_, err := Fetch(ctx, c, "test:fail", 0, false, load)
	assert.Error(t, err)
	_, err = Fetch(ctx, c, "test:fail", 0, false, load)
	assert.Error(t, err)
	assert.Equal(t, 2, loads, "a failed read is never cached")
}
