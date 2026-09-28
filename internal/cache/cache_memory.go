package cache

import (
	"context"
	"hash/maphash"
	"sync"
	"time"
)

// DefaultMaxMemory is the memory budget a driver falls back to when the
// caller does not name one. It exists so a zero-value mistake cannot turn
// into an unbounded cache.
const DefaultMaxMemory = 32 << 20

// memoryChunkSize is the size of one chunk of the entry ring. Entries live in
// fixed byte chunks instead of per-entry allocations, so the garbage
// collector sees a handful of byte arrays and never the entries themselves.
const memoryChunkSize = 64 << 10

// memoryShardCount caps the number of independent rings a cache holds. Every
// shard owns its lock, index, and ring, so concurrent writes rarely contend;
// the count stays a power of two.
const memoryShardCount = 64

// cacheEpoch is the monotonic baseline every expiry lives against: an entry
// stores its deadline as an offset from it, so a wall-clock step — an NTP
// correction, a manual change — can neither age an entry early nor
// resurrect an expired one. time.Since reads the process's monotonic clock,
// which never jumps.
var cacheEpoch = time.Now()

// monotonicNow reads the clock expiry deadlines are measured on.
func monotonicNow() int64 { return int64(time.Since(cacheEpoch)) }

// Memory is the in-process Cache driver.
//
// The design is adopted from VictoriaMetrics/fastcache without the
// dependency: entries are appended into fixed byte chunks (no per-entry
// allocation, nothing for the garbage collector to scan inside a chunk), an
// index maps a keyed hash to where the entry lives, and the API is built to
// be used in zero-allocation mode — Get appends into the caller's dst.
//
// Two properties the adoption deliberately keeps:
//
//   - Hash collisions are handled, which is what fastcache does and bigcache
//     does not: every entry stores its own key, a lookup verifies the bytes
//     before answering, and an entry that collides with the one holding the
//     primary slot lives in a secondary slot rather than being overwritten.
//     Two keys that share a hash therefore keep working; only a third key
//     with the same hash evicts the secondary, the way a full ring would
//     have evicted it anyway.
//   - The ring is cleaned when full. Space of a deleted or overwritten entry
//     is reclaimed only when the ring reaches it again; an eviction is a
//     whole-cache reset that keeps the allocated chunks. An entry that
//     disappears between two gets is the honest answer of a budget-driven
//     cache, never silent corruption.
type Memory struct {
	shards []memoryShard
	mask   uint64
	seed   maphash.Seed
	ttl    time.Duration

	// hashFn replaces the keyed hash in tests, so the collision path runs
	// without hoping two random strings collide. It is nil in production.
	hashFn func(string) uint64
}

// memoryShard is one ring of the cache. maxMemory is the byte budget of this
// shard alone, not of the whole cache.
type memoryShard struct {
	mu         sync.RWMutex
	m          map[uint64]memoryEntry
	collisions map[uint64]memoryEntry
	chunks     [][]byte
	curr       int
	off        int
	maxMemory  int
}

// memoryEntry locates the bytes of one entry: the key and the value sit
// contiguously in the shard's ring, starting at chunk/off, and the value may
// reach past that chunk into the following ones. expiresAt is measured on
// the monotonic clock (see cacheEpoch). The fields are plain ints —
// no pointer among them keeps the index outside of the garbage collector's
// work.
type memoryEntry struct {
	chunk     int
	off       int
	keyLen    int
	valLen    int
	expiresAt int64
}

// NewMemory builds the in-memory driver. maxMemory is the total byte budget
// across all shards; defaultTTL is the lifetime of an entry that Set stores
// without an explicit one. A budget of zero falls back to DefaultMaxMemory,
// and a budget is always rounded to whole chunks.
func NewMemory(maxMemory int64, defaultTTL time.Duration) *Memory {
	if maxMemory <= 0 {
		maxMemory = DefaultMaxMemory
	}
	if defaultTTL <= 0 {
		defaultTTL = 5 * time.Minute
	}

	shards := 1
	for shards < memoryShardCount && maxMemory/int64(2*shards) >= memoryChunkSize {
		shards <<= 1
	}

	c := &Memory{
		shards: make([]memoryShard, shards),
		mask:   uint64(shards) - 1,
		seed:   maphash.MakeSeed(),
		ttl:    defaultTTL,
	}
	perShard := max(memoryChunkSize, int(maxMemory)/shards)
	for i := range c.shards {
		c.shards[i].maxMemory = perShard
		c.shards[i].m = make(map[uint64]memoryEntry)
	}
	return c
}

// Get implements Cache. The hot path holds the read lock, verifies the
// stored key bytes, checks the expiry, and appends the value — no allocation
// while dst has room for the value. The context is taken but never waited
// on: an in-process read has no network round trip to cancel.
func (c *Memory) Get(_ context.Context, dst []byte, key string) ([]byte, bool) {
	h := c.hash(key)
	shard := &c.shards[h&c.mask]

	shard.mu.RLock()
	entry, ok := shard.lookup(h, key)
	if ok && entry.expiresAt > monotonicNow() {
		dst = shard.appendValue(dst, entry)
		shard.mu.RUnlock()
		return dst, true
	}
	shard.mu.RUnlock()

	// An expired entry drops its stale index here rather than leaving it
	// for later: ring space is reclaimed by position anyway, so the index
	// is the only thing that would survive.
	if ok {
		c.delete(h, key)
	}
	return dst, false
}

// Set implements Cache. When the hash of key collides with the hash of
// another entry, the new entry takes the primary slot and the previous one
// moves to the secondary — both stay reachable, and a third collision in the
// same slot evicts the secondary the way the ring would have evicted it
// anyway.
func (c *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) {
	h := c.hash(key)
	shard := &c.shards[h&c.mask]

	if ttl <= 0 {
		ttl = c.ttl
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()
	shard.set(h, key, value, monotonicNow()+int64(ttl))
}

// Del implements Cache. Deleting removes the entry whose key bytes match; a
// colliding entry is left where it lives.
func (c *Memory) Del(_ context.Context, key string) {
	h := c.hash(key)
	shard := &c.shards[h&c.mask]

	shard.mu.Lock()
	defer shard.mu.Unlock()

	if entry, ok := shard.m[h]; ok && shard.matches(entry, key) {
		delete(shard.m, h)
		return
	}
	if entry, ok := shard.collisions[h]; ok && shard.matches(entry, key) {
		delete(shard.collisions, h)
	}
}

// GetMany implements Cache. The in-process driver has no round trip to
// amortize, so a batch is a loop over Get — the sharded locks already keep
// the keys out of each other's way.
func (c *Memory) GetMany(ctx context.Context, keys []string) map[string][]byte {
	found := make(map[string][]byte, len(keys))
	for _, key := range keys {
		if value, ok := c.Get(ctx, nil, key); ok {
			found[key] = value
		}
	}
	return found
}

// SetMany implements Cache.
func (c *Memory) SetMany(ctx context.Context, items map[string][]byte, ttl time.Duration) {
	for key, value := range items {
		c.Set(ctx, key, value, ttl)
	}
}

// DelMany implements Cache.
func (c *Memory) DelMany(ctx context.Context, keys []string) {
	for _, key := range keys {
		c.Del(ctx, key)
	}
}

// DelPrefix implements Cache. The index maps hashes to entries, so a prefix
// removal walks the index of every shard and drops the entries whose stored
// key bytes begin with the prefix — the entries carry their own keys, which
// is what makes the walk possible without a second data structure.
func (c *Memory) DelPrefix(_ context.Context, prefix string) {
	for i := range c.shards {
		shard := &c.shards[i]

		shard.mu.Lock()
		for h, entry := range shard.m {
			if shard.startsWith(entry, prefix) {
				delete(shard.m, h)
			}
		}
		for h, entry := range shard.collisions {
			if shard.startsWith(entry, prefix) {
				delete(shard.collisions, h)
			}
		}
		shard.mu.Unlock()
	}
}

// startsWith reads the key stored at the entry's location and reports
// whether it begins with the prefix. spans walks the bytes across the chunk
// boundary, so the comparison never materializes the key.
func (s *memoryShard) startsWith(entry memoryEntry, prefix string) bool {
	if entry.keyLen < len(prefix) {
		return false
	}
	consumed := 0
	s.spans(entry.chunk, entry.off, len(prefix), func(b []byte) {
		if consumed < 0 || string(b) != prefix[consumed:consumed+len(b)] {
			consumed = -1
			return
		}
		consumed += len(b)
	})
	return consumed >= 0
}

// Reset drops every entry and winds the rings back, keeping the memory the
// shards already grew. It is what an operator or a test runs to reclaim
// space before the ring would have reached it on its own — the only drain
// the driver needs, since the budget, the TTLs, and the ring's own reset
// already bound what a running cache holds.
func (c *Memory) Reset() {
	for i := range c.shards {
		shard := &c.shards[i]
		shard.mu.Lock()
		clear(shard.m)
		clear(shard.collisions)
		shard.curr = 0
		shard.off = 0
		shard.mu.Unlock()
	}
}

// delete is the locked re-entry of Get's expiry path.
func (c *Memory) delete(h uint64, key string) {
	shard := &c.shards[h&c.mask]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if entry, ok := shard.m[h]; ok && shard.matches(entry, key) {
		delete(shard.m, h)
		return
	}
	if entry, ok := shard.collisions[h]; ok && shard.matches(entry, key) {
		delete(shard.collisions, h)
	}
}

func (c *Memory) hash(key string) uint64 {
	if c.hashFn != nil {
		return c.hashFn(key)
	}
	return maphash.String(c.seed, key)
}

func (s *memoryShard) lookup(h uint64, key string) (memoryEntry, bool) {
	if entry, ok := s.m[h]; ok && s.matches(entry, key) {
		return entry, true
	}
	if entry, ok := s.collisions[h]; ok && s.matches(entry, key) {
		return entry, true
	}
	return memoryEntry{}, false
}

// matches reads the key stored at the entry's location and compares it with
// the requested one. This is the check that makes a hash collision harmless:
// two keys sharing a hash never answer for each other. The conversions in
// the comparison are free of allocation — the compiler compares the bytes
// against the string directly.
func (s *memoryShard) matches(entry memoryEntry, key string) bool {
	if entry.keyLen != len(key) {
		return false
	}
	prefix, rest := s.slice(entry.chunk, entry.off, entry.keyLen)
	return string(prefix) == key[:len(prefix)] && string(rest) == key[len(prefix):]
}

// appendValue appends the value the entry holds.
func (s *memoryShard) appendValue(dst []byte, entry memoryEntry) []byte {
	s.spans(entry.chunk, entry.off+entry.keyLen, entry.valLen, func(b []byte) {
		dst = append(dst, b...)
	})
	return dst
}

// spans calls fn for every contiguous piece of the length bytes that start
// at chunk/off, in order. An entry that reaches past its chunk continues in
// the following one, which is why a read can yield more than one span.
func (s *memoryShard) spans(chunk int, off int, length int, fn func([]byte)) {
	for length > 0 {
		c := s.chunks[chunk]
		n := min(len(c)-off, length)
		fn(c[off : off+n])
		chunk++
		off = 0
		length -= n
	}
}

// slice returns the first span of the ring starting at chunk/off, with the
// remainder in the second return value. Only the key comparison needs it —
// a key is checked in at most two pieces, without building a temporary.
func (s *memoryShard) slice(chunk int, off int, length int) (first, second []byte) {
	c := s.chunks[chunk]
	if remaining := len(c) - off; length <= remaining {
		return c[off : off+length], nil
	}
	first = c[off:]
	second = s.chunks[chunk+1][:length-len(first)]
	return first, second
}

// set writes the entry and indexes it. The bytes are [key][value], the
// metadata lives in the index entry.
func (s *memoryShard) set(h uint64, key string, value []byte, expiresAt int64) {
	if entry, ok := s.m[h]; ok && s.matches(entry, key) {
		// Same key: a value of the same length overwrites in place, and
		// anything else is written fresh, so no reader can ever see a
		// value made of halves from two eras.
		if entry.valLen == len(value) {
			s.spans(entry.chunk, entry.off+entry.keyLen, entry.valLen, func(b []byte) {
				n := copy(b, value)
				value = value[n:]
			})
			entry.expiresAt = expiresAt
			s.m[h] = entry
			return
		}
	}

	entry := memoryEntry{
		keyLen:    len(key),
		valLen:    len(value),
		expiresAt: expiresAt,
	}
	chunk, off, ok := s.write(key, value)
	if !ok {
		// Larger than this shard's budget. Leaving the previous entry in
		// place is the same answer as an eviction that never stored this one.
		return
	}
	entry.chunk, entry.off = chunk, off

	if prev, ok := s.m[h]; ok {
		// A different key owns this hash: move it to the secondary slot so
		// both stay reachable.
		if s.collisions == nil {
			s.collisions = make(map[uint64]memoryEntry)
		}
		s.collisions[h] = prev
	}
	s.m[h] = entry
}

// write appends key and value to the ring and reports where they start.
// Bytes may cross into following chunks. A reset never lands in the middle
// of this copy: when the remainder cannot hold the entry, the ring rewinds
// first. An entry larger than the shard budget is refused, because storing
// it would overwrite its own head.
func (s *memoryShard) write(key string, value []byte) (chunk int, off int, ok bool) {
	need := len(key) + len(value)
	if need > s.maxMemory {
		return 0, 0, false
	}
	if len(s.chunks) == 0 || s.room() < need {
		s.rewind()
	}
	chunk, off = s.curr, s.off
	s.appendString(key)
	s.appendBytes(value)
	return chunk, off, true
}

// appendString copies a string into the ring.
func (s *memoryShard) appendString(b string) {
	for len(b) > 0 {
		if s.off == memoryChunkSize {
			s.advance()
		}
		cur := s.chunks[s.curr]
		n := copy(cur[s.off:], b)
		b = b[n:]
		s.off += n
	}
}

// appendBytes copies b into the ring, moving to the next chunk when the
// current one is full. The ring order is linear, so the entry a write
// starts stays contiguous with what its continuation writes.
func (s *memoryShard) appendBytes(b []byte) {
	for len(b) > 0 {
		if s.off == memoryChunkSize {
			s.advance()
		}
		cur := s.chunks[s.curr]
		n := copy(cur[s.off:], b)
		b = b[n:]
		s.off += n
	}
}

// room is the bytes this write can still use without wrapping onto its own
// start: the rest of the current chunk, the chunks already allocated after
// it, and the budget not yet allocated.
func (s *memoryShard) room() int {
	if len(s.chunks) == 0 {
		return s.maxMemory
	}
	left := (len(s.chunks)-s.curr)*memoryChunkSize - s.off
	allocated := len(s.chunks) * memoryChunkSize
	if allocated < s.maxMemory {
		left += s.maxMemory - allocated
	}
	return left
}

// rewind drops the index and starts the next entry at the front of the ring.
// Allocated chunks stay: a cache that filled once keeps that memory.
func (s *memoryShard) rewind() {
	clear(s.m)
	clear(s.collisions)
	s.curr = 0
	s.off = 0
	if len(s.chunks) == 0 {
		s.chunks = append(s.chunks, make([]byte, memoryChunkSize))
	}
}

// advance moves to the next chunk. write checks room first, so the budget
// always has a chunk for this step.
func (s *memoryShard) advance() {
	if s.curr+1 < len(s.chunks) {
		s.curr++
		s.off = 0
		return
	}
	s.chunks = append(s.chunks, make([]byte, memoryChunkSize))
	s.curr = len(s.chunks) - 1
	s.off = 0
}
