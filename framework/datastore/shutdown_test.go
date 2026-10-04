package datastore_test

import (
	"context"
	"testing"

	"github.com/samber/do/v2"

	"github.com/riipandi/saka/framework/datastore"
)

// The composition root hands these two to samber/do, which stops a service by
// calling Shutdown and ignores Close entirely. The assertion is the contract
// that keeps a pool from being left open at the end of a run, which is what a
// Close-only type did: the container reported a successful shutdown while the
// pool stayed connected.
var (
	_ do.ShutdownerWithContext = (*datastore.Postgres)(nil)
	_ do.ShutdownerWithContext = (*datastore.Valkey)(nil)
)

// TestShutdownIsSafeOnAnEmptyHandle covers the zero value a test or a failed
// construction can leave behind: the container calls Shutdown on whatever it
// holds, so it must not panic on a handle that was never opened.
func TestShutdownIsSafeOnAnEmptyHandle(t *testing.T) {
	t.Parallel()

	var pool *datastore.Postgres
	pool.Shutdown(context.Background())

	var kv *datastore.Valkey
	kv.Shutdown(context.Background())
}
