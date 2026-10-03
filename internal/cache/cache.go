// Package cache holds the key-value cache drivers the application reads
// through. The in-memory driver keeps entries in process memory; the kvstore
// driver keeps them in the optional Valkey backend.
//
// New is the entry point a composition root calls: it reads the
// configuration and hands back whichever driver applies — or Noop, which
// answers every read with a miss, so a feature never checks whether caching
// is on and a call site stays identical in every deployment.
package cache

import (
	"context"
	"time"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/datastore"
)

// Cache is the key-value surface a feature reads through. Every method is
// safe for concurrent use, and every one carries the caller's context — the
// remote driver owns a network round trip, and a request that was cancelled
// must not pay for one.
//
// Values are bytes, and keys are strings: a cache entry is a serialized
// payload, so the driver never inspects what it holds. Get writes into dst —
// the append-style of an API designed to be used in zero-allocation mode, so
// a hot call site reuses its buffer and a hit costs no allocation at all.
type Cache interface {
	// Get reads the entry key holds into dst and reports whether it was
	// found and not expired. The returned slice is dst grown by append, so
	// it is valid until the caller reuses dst.
	Get(ctx context.Context, dst []byte, key string) ([]byte, bool)
	// GetMany reads the entries the named keys hold and returns only the
	// ones found and not expired. The map is the price a batch pays for
	// its round-trip saving on the remote driver: a single hot read stays
	// on Get, which appends into the caller's buffer and allocates
	// nothing.
	GetMany(ctx context.Context, keys []string) map[string][]byte
	// Set stores value under key until ttl elapses. A ttl of zero or less
	// means the driver's configured default. Set overwrites the value of
	// the same key; it never disturbs an entry under a different key.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
	// SetMany stores every item until ttl elapses, each the way Set stores
	// it. A ttl of zero or less means the driver's configured default.
	SetMany(ctx context.Context, items map[string][]byte, ttl time.Duration)
	// Del removes the entry under key, if any.
	Del(ctx context.Context, key string)
	// DelMany removes the entries under every named key, if any.
	DelMany(ctx context.Context, keys []string)
	// DelPrefix removes every entry whose key begins with prefix. It is how
	// a feature invalidates a family of keys it cannot enumerate — the
	// entries a query's parameters name one by one — in one call. An empty
	// prefix removes every entry the cache holds.
	DelPrefix(ctx context.Context, prefix string)
}

// Noop is the driver that caches nothing: every read is a miss, every write
// is dropped. It is what the composition root hands out when the cache is
// disabled — or when the chosen backend is unavailable — so a feature needs
// no branch for the caching question and the answer to "cached?" is simply
// "no", in a run that keeps working.
type Noop struct{}

// Get always misses.
func (Noop) Get(context.Context, []byte, string) ([]byte, bool) { return nil, false }

// GetMany always misses.
func (Noop) GetMany(context.Context, []string) map[string][]byte { return nil }

// Set drops the write.
func (Noop) Set(context.Context, string, []byte, time.Duration) {}

// SetMany drops the writes.
func (Noop) SetMany(context.Context, map[string][]byte, time.Duration) {}

// Del drops the delete.
func (Noop) Del(context.Context, string) {}

// DelMany drops the deletes.
func (Noop) DelMany(context.Context, []string) {}

// DelPrefix drops the delete.
func (Noop) DelPrefix(context.Context, string) {}

// New hands back the driver the configuration names, or Noop where caching
// does not run:
//
//   - cache.enable false — the cache is off, every read is a miss;
//   - the kvstore driver while kvstore.enable false — the cache is wanted
//     but its backend is not available, so the same run skips it;
//   - an unknown driver — the configuration refuses it elsewhere, and a run
//     that carried one anyway degrades to no cache.
//
// kv is the shared backend client the kvstore driver reads through, built
// by the composition root when the backend is enabled; the factory never
// opens one. Noop is the reason a feature never branches on the caching
// question: the answer to "cached?" is simply "no" in every bypassed case.
func New(cfg config.Config, kv *datastore.Valkey) Cache {
	if !cfg.Cache.Enable {
		return Noop{}
	}
	switch cfg.Cache.Driver {
	case config.CacheKV:
		if !cfg.KVStore.Enable || kv == nil {
			return Noop{}
		}
		return NewValkey(kv, cfg.Cache.TTL)
	default:
		return NewMemory(cfg.Cache.MaxMemory, cfg.Cache.TTL)
	}
}
