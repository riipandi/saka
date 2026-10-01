package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/queue"
)

// inspectTask is the task the scheduler inspection tests enqueue.
type inspectTask struct{}

func (inspectTask) Config() queue.QueueConfig {
	return queue.QueueConfig{Name: "inspect-schedule", MaxAttempts: 2, Timeout: time.Minute}
}

// TestJobsAnswersTheSeededStateRows proves the read surface: a seeded job
// lists with its spec and cursor, in name order, and the state row is the
// source — not the in-memory registry.
func TestJobsAnswersTheSeededStateRows(t *testing.T) {
	pool := migratedPool(t)
	client := newInspectClient(t, pool)
	s := newScheduler(t, pool, client, Job{Name: "hermione-cron", Spec: "@every 1h", Task: inspectTask{}})

	require.NoError(t, s.seed(t.Context(), &s.jobs[0]))

	jobs, err := s.Jobs(t.Context())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "hermione-cron", jobs[0].Name)
	require.Equal(t, "@every 1h", jobs[0].Spec)
	require.False(t, jobs[0].NextDue.IsZero())
	require.Nil(t, jobs[0].LastFired)
}

// TestRunNowEnqueuesWithoutAdvancingTheSchedule proves the trigger: a job run
// on demand puts its task on the queue, and the next-due cursor — the claim
// the cron fire owns — stays where it was.
func TestRunNowEnqueuesWithoutAdvancingTheSchedule(t *testing.T) {
	pool := migratedPool(t)
	client := newInspectClient(t, pool)
	s := newScheduler(t, pool, client, Job{Name: "ron-cron", Spec: "@every 1h", Task: inspectTask{}})

	require.NoError(t, s.seed(t.Context(), &s.jobs[0]))
	jobs, err := s.Jobs(t.Context())
	require.NoError(t, err)
	dueBefore := jobs[0].NextDue

	require.NoError(t, s.RunNow(t.Context(), "ron-cron"))

	pending, err := client.Pending(t.Context(), "inspect-schedule")
	require.NoError(t, err)
	require.EqualValues(t, 1, pending)

	jobs, err = s.Jobs(t.Context())
	require.NoError(t, err)
	require.Equal(t, dueBefore, jobs[0].NextDue)
}

// TestRunNowRefusesAnUnknownJob proves the not-found answer: a name the
// process never registered is a caller's mistake, not an enqueue.
func TestRunNowRefusesAnUnknownJob(t *testing.T) {
	pool := migratedPool(t)
	client := newInspectClient(t, pool)
	s := newScheduler(t, pool, client, Job{Name: "draco-cron", Spec: "@every 1h", Task: inspectTask{}})

	err := s.RunNow(t.Context(), "no-such-job")
	require.ErrorIs(t, err, ErrJobUnknown)
}

// newInspectClient builds a queue client over the shared pool without
// starting the dispatcher: the inspection surface reads the tables, it does
// not run them.
func newInspectClient(t *testing.T, pool queue.Store) *queue.Client {
	t.Helper()
	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)
	return client
}
