package health

import (
	"context"
	"fmt"
	"time"
)

// CheckNameKVStore is the name of the key-value backend check in a result.
// It names the kind of dependency, never the product behind it, so a report
// does not advertise which backend a deployment runs.
const CheckNameKVStore = "kvstore"

// DefaultKVStoreTimeout bounds the backend probe. It matches the ping timeout
// the client itself applies, so a hanging server is caught twice at the same
// scale rather than by the checker's global deadline alone.
const DefaultKVStoreTimeout = 2 * time.Second

// KVPinger is the part of the key-value backend client a kvstore check needs.
// *datastore.Valkey satisfies it, and so does a fake in a test.
type KVPinger interface {
	Ping(ctx context.Context) error
}

// KVStoreCheck reports whether the shared key-value backend answers.
//
// The caller adds this check only while the backend is enabled: a disabled
// backend is never dialled by the process, so reporting it down would
// describe a dependency the application does not have.
//
// The report carries no target: the REST endpoint publishes this result, and
// which host the process dials is not something an unauthenticated reader
// should learn. The CLI report uses KVStoreCheckWithTarget instead.
func KVStoreCheck(kv KVPinger) Check {
	return kvStoreCheck(kv, "")
}

// KVStoreCheckWithTarget is KVStoreCheck with the redacted connection string
// in the result's target, for a surface an operator reads directly. The
// endpoint publishes the plain check instead.
func KVStoreCheckWithTarget(kv KVPinger, target string) Check {
	return kvStoreCheck(kv, target)
}

// kvStoreCheck builds the probe. target is what the report names, empty to
// name nothing.
func kvStoreCheck(kv KVPinger, target string) Check {
	return Check{
		Name:    CheckNameKVStore,
		Target:  target,
		Timeout: DefaultKVStoreTimeout,
		Check: func(ctx context.Context) error {
			if err := kv.Ping(ctx); err != nil {
				return fmt.Errorf("kvstore: %w", err)
			}
			return nil
		},
	}
}
