package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

// newValkeyCache connects the driver to the shared test backend.
func newValkeyCache(t *testing.T) (*Valkey, *datastore.Valkey) {
	t.Helper()

	backend := testutils.StartValkeyWithTimeout(t)
	kv, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{URL: backend.URL})
	require.NoError(t, err)
	t.Cleanup(func() { kv.Shutdown(context.Background()) })
	return NewValkey(kv, DefaultKeyPrefix, time.Minute), kv
}

func TestValkeyRoundTripsAnEntry(t *testing.T) {
	c, _ := newValkeyCache(t)

	c.Set(t.Context(), "user:1", []byte("ali"), 0)

	value, ok := c.Get(t.Context(), nil, "user:1")
	require.True(t, ok)
	assert.Equal(t, "ali", string(value))
}

func TestValkeyMissesAnAbsentKey(t *testing.T) {
	c, _ := newValkeyCache(t)

	value, ok := c.Get(t.Context(), nil, "absent")
	assert.False(t, ok)
	assert.Empty(t, value)
}

func TestValkeyOverwritesTheSameKey(t *testing.T) {
	c, _ := newValkeyCache(t)

	c.Set(t.Context(), "k", []byte("first"), 0)
	c.Set(t.Context(), "k", []byte("second"), 0)

	value, ok := c.Get(t.Context(), nil, "k")
	require.True(t, ok)
	assert.Equal(t, "second", string(value))
}

func TestValkeyExpiresEntriesOnTheServerClock(t *testing.T) {
	c, _ := newValkeyCache(t)

	c.Set(t.Context(), "k", []byte("1"), 50*time.Millisecond)
	time.Sleep(120 * time.Millisecond)

	_, ok := c.Get(t.Context(), nil, "k")
	assert.False(t, ok, "the server must expire the entry on its own clock")
}

func TestValkeyNamesItsKeysWithinThePrefix(t *testing.T) {
	c, kv := newValkeyCache(t)

	c.Set(t.Context(), "user:1", []byte("ali"), time.Minute)

	// A second client reading the same server finds the entry under the
	// namespaced key: the prefix is the isolation features share.
	probe, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{
		URL: testutils.StartValkeyWithTimeout(t).URL,
	})
	require.NoError(t, err)
	defer probe.Shutdown(context.Background())

	value, err := probe.Client().Do(t.Context(),
		probe.Client().B().Get().Key(DefaultKeyPrefix+"user:1").Build()).ToString()
	require.NoError(t, err)
	assert.Equal(t, "ali", value)
	_ = kv
}

func TestValkeyDeletes(t *testing.T) {
	c, _ := newValkeyCache(t)

	c.Set(t.Context(), "k", []byte("v"), 0)
	c.Del(t.Context(), "k")

	_, ok := c.Get(t.Context(), nil, "k")
	assert.False(t, ok)
}

func TestValkeyBatchRoundTrips(t *testing.T) {
	c, _ := newValkeyCache(t)

	c.SetMany(t.Context(), map[string][]byte{"a": []byte("1"), "b": []byte("2")}, 0)

	// One MGET answers for the whole batch, and a key the backend does not
	// hold is simply absent from the answer.
	found := c.GetMany(t.Context(), []string{"a", "b", "absent"})
	require.Len(t, found, 2)
	assert.Equal(t, "1", string(found["a"]))
	assert.Equal(t, "2", string(found["b"]))

	c.DelMany(t.Context(), []string{"a", "b"})
	assert.Empty(t, c.GetMany(t.Context(), []string{"a", "b"}))
}

func TestValkeyTreatsABrokenBackendAsAMiss(t *testing.T) {
	// A closed backend makes every read fail: a cache may degrade, the
	// feature behind it may not. The builder stays real (a command is
	// built the same way whatever the backend does), the round trip is
	// what breaks.
	backend := testutils.StartValkeyWithTimeout(t)
	kv, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{URL: backend.URL})
	require.NoError(t, err)
	defer kv.Shutdown(context.Background())

	c := NewValkey(kv, DefaultKeyPrefix, time.Minute)
	c.client = brokenClient{build: c.client.B()}

	value, ok := c.Get(t.Context(), nil, "k")
	assert.False(t, ok)
	assert.Empty(t, value)

	// Writes, batch writes, and deletes must not panic on the failure
	// either; a batch read is a miss.
	c.Set(t.Context(), "k", []byte("v"), 0)
	c.SetMany(t.Context(), map[string][]byte{"k": []byte("v")}, 0)
	c.Del(t.Context(), "k")
	c.DelMany(t.Context(), []string{"k"})
	assert.Nil(t, c.GetMany(t.Context(), []string{"k"}))
}

// brokenClient answers every command with an error, the state of a backend
// that went away.
type brokenClient struct {
	build valkey.Builder
}

func (brokenClient) Do(context.Context, valkey.Completed) valkey.ValkeyResult {
	return valkey.NewErrorResult(assert.AnError)
}

func (brokenClient) DoMulti(context.Context, ...valkey.Completed) []valkey.ValkeyResult {
	return []valkey.ValkeyResult{valkey.NewErrorResult(assert.AnError)}
}

func (b brokenClient) B() valkey.Builder { return b.build }
