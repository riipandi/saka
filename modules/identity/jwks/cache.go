package jwks

import (
	"sync"
	"time"
)

// SigningCacheTTL is how long the opened signing key, the derived algorithm
// and the verification key set are reused before the database is read again.
// A rotation staged by another process (jwks:generate) reaches this one within
// the window; the old key keeps verifying throughout because the set holds
// every active key. It is short because a signing-key change should land
// promptly, and not zero because every authenticated request would otherwise
// pay two reads and a private-key decrypt.
const SigningCacheTTL = 30 * time.Second

// ttlCache holds one loaded value for a fixed time. The load runs under the
// lock, so a burst of requests on a cold or expired entry costs one read, not
// one each. A failed load is not stored: the next call tries again.
//
// The epoch is how a rotation drops every entry without taking their locks:
// the rotation runs inside a load, so a reset that locked would wait on the
// load it is part of. An entry is valid only for the epoch it was read under,
// and the epoch is read before the load, so a drop that lands mid-load leaves
// the loaded value stale at once.
type ttlCache[T any] struct {
	mu       sync.Mutex
	value    T
	loadedAt time.Time
	epoch    uint64
	valid    bool
}

func (c *ttlCache[T]) get(now time.Time, ttl time.Duration, epoch uint64, load func() (T, error)) (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && c.epoch == epoch && now.Before(c.loadedAt.Add(ttl)) {
		return c.value, nil
	}
	value, err := load()
	if err != nil {
		var zero T
		return zero, err
	}
	c.value, c.loadedAt, c.epoch, c.valid = value, now, epoch, true
	return value, nil
}
