package cache

import (
	"context"
	"encoding/json/v2"
	"time"
)

// Fetch answers the value key holds, or reads it through the loader when the
// cache misses.
//
// The value crosses the driver as JSON, so T is any type the production
// encoder round-trips. A loader failure is returned as-is — a cache may
// degrade, the source behind it may not — and nothing is written. A value
// the encoder cannot carry is returned loaded but never stored, the one
// shape of a cache miss a retry cannot heal; a type that reaches here
// unencodable is a defect a test catches, not a runtime event.
//
// A bypassed call is a pure bypass: the loader answers and the cache is
// neither read nor written, so the entry the last miss left keeps aging
// toward its own TTL. This is the caller-facing escape hatch — a client that
// asks to skip the cache (a `nocache` flag on the request) gets the source
// of truth without disturbing what the other callers read.
func Fetch[T any](ctx context.Context, c Cache, key string, ttl time.Duration, bypass bool, load func(context.Context) (T, error)) (T, error) {
	if bypass {
		return load(ctx)
	}

	if value, ok := c.Get(ctx, nil, key); ok {
		var decoded T
		if err := json.Unmarshal(value, &decoded); err == nil {
			return decoded, nil
		}
		// An entry that no longer decodes is dead weight: drop it, then
		// read through.
		c.Del(ctx, key)
	}

	value, err := load(ctx)
	if err != nil {
		return value, err
	}

	if encoded, err := json.Marshal(value); err == nil {
		c.Set(ctx, key, encoded, ttl)
	}
	return value, nil
}
