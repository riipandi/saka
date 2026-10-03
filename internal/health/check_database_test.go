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

// stubPool is a Pinger whose answer the test controls.
type stubPool struct {
	err    error
	called bool
}

func (s *stubPool) Ping(context.Context) error {
	s.called = true
	return s.err
}

func TestDatabaseCheckPassesWhenPoolPings(t *testing.T) {
	pool := &stubPool{}

	result := health.NewChecker(health.WithCheck(health.DatabaseCheckWithTarget(pool, "localhost:5432/test"))).Check(t.Context())

	assert.True(t, pool.called)
	assert.Equal(t, health.GlobalHealthy, result.Status)
	assert.Equal(t, health.CheckNameDatabase, result.Details["database"].Name)
}

// The report must not carry the connection string: the endpoint publishes
// this result, and which host the process dials is not something an
// unauthenticated reader should learn.
func TestDatabaseCheckHidesTarget(t *testing.T) {
	check := health.DatabaseCheck(&stubPool{})

	assert.Empty(t, check.Target)
}

// The target must be reported, so the text output says which database was
// reached, and must never carry the password from the DSN.
func TestDatabaseCheckReportsTarget(t *testing.T) {
	check := health.DatabaseCheckWithTarget(&stubPool{}, "localhost:5432/test")

	assert.Equal(t, "localhost:5432/test", check.Target)
	assert.NotContains(t, check.Target, "@")
}

// The error must name the dependency generically, so a failing probe says
// which kind of dependency is down without revealing the engine behind it.
func TestDatabaseCheckWrapsPingError(t *testing.T) {
	pool := &stubPool{err: errors.New("connection refused")}

	result := health.NewChecker(health.WithCheck(health.DatabaseCheckWithTarget(pool, "localhost:5432/test"))).Check(t.Context())

	assert.Equal(t, health.GlobalUnhealthy, result.Status)
	assert.Contains(t, result.Details["database"].Error, "database")
	assert.Contains(t, result.Details["database"].Error, "connection refused")
}

// The check must be required, so a dead database fails the probe rather than
// being reported as an optional extra.
func TestDatabaseCheckIsRequired(t *testing.T) {
	check := health.DatabaseCheckWithTarget(&stubPool{}, "localhost:5432/test")
	assert.False(t, check.Optional)

	// It carries its own timeout, shorter than the checker default, so a
	// hanging database still leaves time to report the failure.
	assert.Positive(t, check.Timeout)
	assert.Less(t, check.Timeout, health.DefaultTimeout)
}

// The check must work against the real adapter, not only a stub: this is
// the interface the CLI and the server wire up.
func TestDatabaseCheckAgainstRealPool(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })

	result := health.NewChecker(health.WithCheck(health.DatabaseCheckWithTarget(pool, "localhost:5432/test"))).Check(t.Context())
	assert.Equal(t, health.GlobalHealthy, result.Status)
	assert.Empty(t, result.Failed())

	// Once the pool is closed the probe must fail, which is what a lost
	// database looks like from the process.
	pool.Shutdown(context.Background())

	result = health.NewChecker(
		health.WithCheck(health.DatabaseCheckWithTarget(pool, "localhost:5432/test")),
		health.WithTimeout(5*time.Second),
	).Check(t.Context())
	assert.Equal(t, health.GlobalUnhealthy, result.Status)
	assert.Contains(t, result.Details["database"].Error, "database")
}
