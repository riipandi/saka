package queue

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The claim orders by priority first and age second: a higher number is
// claimed first, and ties keep insertion order. The claim is called directly,
// so the order is observed without racing a dispatcher's clock.
func TestPriorityClaimsHigherFirst(t *testing.T) {
	pool := migratedPool(t)
	client, err := NewClient(ClientConfig{
		Store:        pool,
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)

	low := save(t, client.Add(probeTask{Name: "low"}).Priority(1))
	alsoDefault := save(t, client.Add(probeTask{Name: "default"}))
	mid := save(t, client.Add(probeTask{Name: "mid"}).Priority(5))
	high := save(t, client.Add(probeTask{Name: "high"}).Priority(9))

	// One claim at a time: a claim takes the highest-priority task left, and
	// a claimed task is no longer claimable, so the sequence of claims is the
	// priority order. (UPDATE ... RETURNING does not promise the subquery's
	// order, so a single multi-row claim cannot be asserted on.)
	ids := make([]string, 0, 4)
	for range 4 {
		claimed, err := claimReady(t.Context(), pool, now(), now().Add(-time.Second), 1)
		require.NoError(t, err)
		require.Len(t, claimed, 1)
		ids = append(ids, claimed[0].ID.String())
	}
	// Priority desc, then insertion order (the IDs are time-sortable v7s).
	expected := slices.Concat(high, mid, low, alsoDefault)
	assert.Equal(t, expected, ids)
}

// A priority below zero is refused at save time: a negative rank would
// invert the order the claim index is built for.
func TestNegativePriorityIsRefused(t *testing.T) {
	client := newTestClient(t)

	_, err := client.Add(probeTask{Name: "negative"}).Priority(-1).Save()
	assert.ErrorIs(t, err, errNegativePriority)
}

// Cancel removes a task that has not been claimed yet; the queue forgets it
// entirely, so its status reads as never having existed.
func TestCancelRemovesAnUnclaimedTask(t *testing.T) {
	client := newTestClient(t)

	saved := save(t, client.Add(probeTask{Name: "do not run"}))
	id := mustID(t, saved[0])

	cancelled, err := client.Cancel(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, cancelled)

	status, err := client.Status(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, TaskStatusNotFound, status)

	pending, err := client.Pending(t.Context(), "probe")
	require.NoError(t, err)
	assert.Zero(t, pending)
}

// A claimed task is in flight — its worker may already be halfway through
// it — so a cancel arrives too late, and the task finishes its lifecycle.
func TestCancelIsTooLateForAClaimedTask(t *testing.T) {
	client := newTestClient(t)
	started := make(chan struct{})
	release := make(chan struct{})
	runWith(t, client, NewQueue[probeTask](func(ctx context.Context, task probeTask) error {
		close(started)
		<-release
		return nil
	}))

	saved := save(t, client.Add(probeTask{Name: "in flight"}))
	<-started

	cancelled, err := client.Cancel(t.Context(), mustID(t, saved[0]))
	require.NoError(t, err)
	assert.False(t, cancelled, "a claimed task must not be cancellable")

	close(release)
}
