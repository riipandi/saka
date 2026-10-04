package cache

import (
	"context"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/riipandi/saka/framework/datastore"
)

// ValkeyKeyPrefix namespaces the keys the cache owns inside the shared
// backend. Features sharing one server are isolated by prefix, not by
// logical database index — which Valkey cluster does not support — so a
// prefix is also what an operator scans or cleans per feature.
const ValkeyKeyPrefix = "saka:cache:"

// Valkey is the Cache driver over the shared key-value backend. It holds no
// connection of its own: the backend client is the process-wide one, and the
// driver only names its keys within a prefix so other features can share the
// same server.
type Valkey struct {
	client valkeyClient
	prefix string
	ttl    time.Duration
}

// valkeyClient is the part of the backend client the driver needs. It keeps
// a test free of a real server by handing in a mock of the same shape.
type valkeyClient interface {
	Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult
	DoMulti(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult
	B() valkey.Builder
}

// NewValkey builds the driver over the shared backend client. defaultTTL is
// the lifetime of an entry that Set stores without an explicit one.
func NewValkey(kv *datastore.Valkey, defaultTTL time.Duration) *Valkey {
	if defaultTTL <= 0 {
		defaultTTL = 5 * time.Minute
	}
	return &Valkey{client: kv.Client(), prefix: ValkeyKeyPrefix, ttl: defaultTTL}
}

// Get implements Cache. A backend that is unreachable, a key that is absent,
// and an error of any kind are all a miss: a cache may degrade, the feature
// behind it may not.
func (c *Valkey) Get(ctx context.Context, dst []byte, key string) ([]byte, bool) {
	value, err := c.client.Do(ctx, c.client.B().Get().Key(c.prefix+key).Build()).AsBytes()
	if err != nil {
		return dst, false
	}
	return append(dst, value...), true
}

// Set implements Cache, with the ttl carried as PX — the per-entry lifetime
// the contract promises, on the server's own clock.
func (c *Valkey) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.ttl
	}
	// BinaryString hands the bytes to the command without a copy: the
	// builder owns them for the duration of the request.
	c.client.Do(ctx, c.client.B().Set().
		Key(c.prefix+key).
		Value(valkey.BinaryString(value)).
		Px(ttl).
		Build())
}

// Del implements Cache.
func (c *Valkey) Del(ctx context.Context, key string) {
	c.client.Do(ctx, c.client.B().Del().Key(c.prefix+key).Build())
}

// GetMany implements Cache. One MGET answers for every key — the round trip
// a batch exists to save. A key the backend does not hold, and a failure of
// any kind, are a miss, never an error.
func (c *Valkey) GetMany(ctx context.Context, keys []string) map[string][]byte {
	if len(keys) == 0 {
		return nil
	}

	resp, err := c.client.Do(ctx, c.client.B().Mget().Key(c.prefixed(keys)...).Build()).ToArray()
	if err != nil {
		return nil
	}

	found := make(map[string][]byte, len(keys))
	for i, msg := range resp {
		if msg.IsNil() {
			continue
		}
		value, err := msg.AsBytes()
		if err != nil {
			continue
		}
		if i < len(keys) {
			found[keys[i]] = value
		}
	}
	return found
}

// SetMany implements Cache. The writes travel as one pipeline of SETs — one
// round trip, the server applies each entry on its own.
func (c *Valkey) SetMany(ctx context.Context, items map[string][]byte, ttl time.Duration) {
	if len(items) == 0 {
		return
	}
	if ttl <= 0 {
		ttl = c.ttl
	}

	cmds := make([]valkey.Completed, 0, len(items))
	for key, value := range items {
		// BinaryString hands the bytes to the command without a copy: the
		// builder owns them for the duration of the request.
		cmds = append(cmds, c.client.B().Set().
			Key(c.prefix+key).
			Value(valkey.BinaryString(value)).
			Px(ttl).
			Build())
	}
	c.client.DoMulti(ctx, cmds...)
}

// DelMany implements Cache. One DEL removes every named entry in a single
// round trip.
func (c *Valkey) DelMany(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	c.client.Do(ctx, c.client.B().Del().Key(c.prefixed(keys)...).Build())
}

// DelPrefix implements Cache. The backend has no atomic prefix removal, so
// the driver scans the keys the namespace holds and deletes the ones the
// pattern matches, batch by batch — an O(dataset) walk like the memory
// driver's index walk, and as best-effort: a scan that fails leaves the
// entries it did not reach for the TTL to end.
func (c *Valkey) DelPrefix(ctx context.Context, prefix string) {
	pattern := c.prefix + prefix + "*"

	var cursor uint64
	for {
		scan := c.client.B().Scan().Cursor(cursor).Match(pattern).Count(100).Build()
		resp, err := c.client.Do(ctx, scan).AsScanEntry()
		if err != nil {
			return
		}
		if len(resp.Elements) > 0 {
			c.client.Do(ctx, c.client.B().Del().Key(resp.Elements...).Build())
		}
		if resp.Cursor == 0 {
			return
		}
		cursor = resp.Cursor
	}
}

// prefixed names the keys within the namespace the driver owns.
func (c *Valkey) prefixed(keys []string) []string {
	names := make([]string, len(keys))
	for i, key := range keys {
		names[i] = c.prefix + key
	}
	return names
}
