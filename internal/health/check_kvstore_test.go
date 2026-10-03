package health_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/health"
	"github.com/riipandi/saka/pkg/testutils"
)

// stubKV is a KVPinger whose answer the test controls.
type stubKV struct {
	err    error
	called bool
}

func (s *stubKV) Ping(context.Context) error {
	s.called = true
	return s.err
}

func TestKVStoreCheckPassesWhenClientPings(t *testing.T) {
	kv := &stubKV{}

	result := health.NewChecker(health.WithCheck(health.KVStoreCheckWithTarget(kv, "localhost:6379/0"))).Check(t.Context())

	assert.True(t, kv.called)
	assert.Equal(t, health.GlobalHealthy, result.Status)
	assert.Equal(t, health.CheckNameKVStore, result.Details["kvstore"].Name)
}

// The report must not carry the connection string: the endpoint publishes
// this result, and which host the process dials is not something an
// unauthenticated reader should learn.
func TestKVStoreCheckHidesTarget(t *testing.T) {
	check := health.KVStoreCheck(&stubKV{})

	assert.Empty(t, check.Target)
}

// The target must be reported, so the text output says which backend was
// reached, and must never carry the password from the URL.
func TestKVStoreCheckReportsTarget(t *testing.T) {
	check := health.KVStoreCheckWithTarget(&stubKV{}, "localhost:6379/0")

	assert.Equal(t, "localhost:6379/0", check.Target)
	assert.NotContains(t, check.Target, "@")
}

// The error must name the dependency generically, so a failing probe says
// which kind of dependency is down without revealing the engine behind it.
func TestKVStoreCheckWrapsPingError(t *testing.T) {
	kv := &stubKV{err: errors.New("connection refused")}

	result := health.NewChecker(health.WithCheck(health.KVStoreCheckWithTarget(kv, "localhost:6379/0"))).Check(t.Context())

	assert.Equal(t, health.GlobalUnhealthy, result.Status)
	assert.Contains(t, result.Details["kvstore"].Error, "kvstore")
	assert.Contains(t, result.Details["kvstore"].Error, "connection refused")
}

// Like the database, the backend is required once it is wired in: a dead
// server fails the probe rather than being reported as an optional extra.
func TestKVStoreCheckIsRequired(t *testing.T) {
	check := health.KVStoreCheckWithTarget(&stubKV{}, "localhost:6379/0")
	assert.False(t, check.Optional)

	// It carries its own timeout, shorter than the checker default, so a
	// hanging server still leaves time to report the failure.
	assert.Positive(t, check.Timeout)
	assert.Less(t, check.Timeout, health.DefaultTimeout)
}

// The check must work against the real adapter, not only a stub: this is
// the interface the CLI and the server wire up.
func TestKVStoreCheckAgainstRealClient(t *testing.T) {
	container := testutils.StartValkey(t.Context(), t)

	kv, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{URL: container.URL})
	require.NoError(t, err)
	t.Cleanup(func() { kv.Shutdown(context.Background()) })

	result := health.NewChecker(health.WithCheck(health.KVStoreCheckWithTarget(kv, container.URL))).Check(t.Context())
	assert.Equal(t, health.GlobalHealthy, result.Status)
	assert.Empty(t, result.Failed())

	// Once the client is closed the probe must fail, which is what a lost
	// backend looks like from the process.
	kv.Shutdown(context.Background())

	result = health.NewChecker(
		health.WithCheck(health.KVStoreCheckWithTarget(kv, container.URL)),
		health.WithTimeout(5*time.Second),
	).Check(t.Context())
	assert.Equal(t, health.GlobalUnhealthy, result.Status)
	assert.Contains(t, result.Details["kvstore"].Error, "kvstore")
}
