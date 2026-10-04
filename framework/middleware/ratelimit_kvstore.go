package middleware

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/valkey-io/valkey-go"
)

// RateLimitKeyPrefix namespaces the limiter's keys on a shared backend, the
// way the cache prefixes its own: features that share one server are isolated
// by prefix, not by logical database index.
const RateLimitKeyPrefix = "ratelimit:"

// The fixed-window check, run server-side: the count and its expiry move in
// one script, so a client cannot wedge a key that was never given a window.
// It answers the count so far and the key's remaining time to live.
const rateLimitScript = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
	redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return {count, redis.call('PTTL', KEYS[1])}
`

// KVStoreLimiter runs the fixed-window check on the shared key-value backend.
//
// It is the driver a run with a Valkey server names in place of the database
// one, moving the counter's write off the SQL pool; the semantics are the
// check function's: one window per key, counted from the first request. The
// budget a key counts against rides with each Allow call — the bucket's
// policy, not this limiter's own.
type KVStoreLimiter struct {
	client valkey.Client
}

// NewKVStoreLimiter builds the limiter over the shared backend client. The
// client is the one the process opened; the limiter owns no connection.
func NewKVStoreLimiter(client valkey.Client) *KVStoreLimiter {
	return &KVStoreLimiter{client: client}
}

// Allow runs the window script. The count is allowed to pass the limit — the
// window keeps counting, as the database check does — so a burst that arrives
// together reads one coherent answer instead of racing an expiry.
func (l *KVStoreLimiter) Allow(ctx context.Context, key string, policy Policy) (Result, error) {
	windowSeconds := max(int(policy.Window/time.Second), 1)

	cmd := l.client.B().Eval().
		Script(rateLimitScript).
		Numkeys(1).
		Key(RateLimitKeyPrefix + key).
		Arg(strconv.Itoa(windowSeconds)).
		Build()

	values, err := l.client.Do(ctx, cmd).ToArray()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: kvstore: %w", err)
	}
	if len(values) < 2 {
		return Result{}, fmt.Errorf("ratelimit: kvstore: script returned %d values", len(values))
	}

	count, err := values[0].ToInt64()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: kvstore: count: %w", err)
	}
	ttlMillis, err := values[1].ToInt64()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: kvstore: ttl: %w", err)
	}

	// A window the backend already forgot — expired between the calls — is a
	// window that starts now, not an error.
	ttl := max(time.Duration(ttlMillis)*time.Millisecond, 0)
	return Result{
		Limited:    count > int64(policy.Limit),
		Limit:      policy.Limit,
		Remaining:  policy.Limit - int(count),
		ResetAt:    time.Now().Add(ttl),
		RetryAfter: ttl,
	}, nil
}
