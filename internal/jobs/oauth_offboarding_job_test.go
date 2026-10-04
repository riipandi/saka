package jobs

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/pkg/testutils"
)

// recordingOffboarder is the oauthsso pass the job tests drive: it records
// the batch the processor handed it.
type recordingOffboarder struct {
	batches []int
}

func (o *recordingOffboarder) OffboardPass(_ context.Context, batch int) error {
	o.batches = append(o.batches, batch)
	return nil
}

// TestTheOffboardingPassRunsHourlyAndBounded dispatches one pass the way a
// serve run does: the pass reaches the feature with the default batch, and
// the successor it queues keeps the hourly schedule.
func TestTheOffboardingPassRunsHourlyAndBounded(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)
	pool, client := migratedClient(t, dsn)
	offboarder := &recordingOffboarder{}
	Register(client, time.Hour, nil, nil, pool, "", false, true, nil, nil, offboarder, nil, nil, nil, slog.New(slog.DiscardHandler))

	// The task is added with its real schedule, so the successor it
	// queues sits an hour out and the pending count settles at one.
	if _, err := client.Add(OAuthOffboardingTask{IntervalMillis: DefaultOAuthOffboardingInterval.Milliseconds()}).Save(); err != nil {
		require.NoError(t, err)
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	client.Start(ctx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer stopCancel()
		client.Stop(stopCtx)
	})

	require.Eventually(t, func() bool {
		return len(offboarder.batches) == 1
	}, 20*time.Second, 25*time.Millisecond, "the dispatched pass must reach the feature")
	assert.Equal(t, []int{DefaultOffboardingBatch}, offboarder.batches)

	// The successor is the schedule: exactly one task pending, waiting
	// out the hourly interval the pass carries.
	require.Eventually(t, func() bool {
		pending, pendingErr := client.Pending(t.Context(), OAuthOffboardingName)
		return pendingErr == nil && pending == 1
	}, 20*time.Second, 25*time.Millisecond, "the pass queues exactly one successor")
	var waitUntil time.Time
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT wait_until FROM public.queue_tasks WHERE queue = $1`, OAuthOffboardingName).Scan(&waitUntil))
	assert.True(t, waitUntil.After(time.Now().Add(59*time.Minute)),
		"the successor keeps the hourly schedule")
}

// TestTheOffboardingPassSkipsWithoutTheFeature keeps a container without the
// oauthsso feature from failing the queue: the pass runs, queues its
// successor, and probes nothing.
func TestTheOffboardingPassSkipsWithoutTheFeature(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)
	pool, client := migratedClient(t, dsn)
	Register(client, time.Hour, nil, nil, pool, "", false, true, nil, nil, nil, nil, nil, nil, slog.New(slog.DiscardHandler))

	_, err := client.Add(OAuthOffboardingTask{IntervalMillis: DefaultOAuthOffboardingInterval.Milliseconds()}).Save()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	client.Start(ctx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer stopCancel()
		client.Stop(stopCtx)
	})

	// The successor is the signal the pass ran and skipped cleanly: one
	// task pending, scheduled the interval out — a failed pass would
	// retry sooner and the queue would carry its failure.
	require.Eventually(t, func() bool {
		pending, pendingErr := client.Pending(t.Context(), OAuthOffboardingName)
		if pendingErr != nil || pending != 1 {
			return false
		}
		var waitUntil time.Time
		if scanErr := pool.QueryRow(t.Context(),
			`SELECT wait_until FROM public.queue_tasks WHERE queue = $1`, OAuthOffboardingName).Scan(&waitUntil); scanErr != nil {
			return false
		}
		return waitUntil.After(time.Now().Add(59 * time.Minute))
	}, 20*time.Second, 25*time.Millisecond, "the nil seam is a skipped pass, not a failed one")
}
