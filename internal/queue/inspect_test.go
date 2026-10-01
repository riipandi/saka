package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"uuid"
)

// inspectTask is the queue the inspection tests enqueue through.
type inspectTask struct{}

func (inspectTask) Config() QueueConfig {
	return QueueConfig{Name: "inspect-queue", MaxAttempts: 3, Timeout: time.Minute}
}

// TestInspectReadsWhatTheDispatcherHasNotClaimed proves the read surface a
// console answers with: a queued task is listed, its payload opens in the
// detail, and the cancel the console offers removes it.
func TestInspectReadsWhatTheDispatcherHasNotClaimed(t *testing.T) {
	client := newTestClient(t)
	client.Register(NewQueue(func(context.Context, inspectTask) error { return nil }))
	ctx := t.Context()

	ids, err := client.Add(inspectTask{}).Ctx(ctx).Save()
	require.NoError(t, err)
	require.Len(t, ids, 1)
	id, err := uuid.Parse(ids[0])
	require.NoError(t, err)

	queues, err := client.Queues(ctx)
	require.NoError(t, err)
	require.Len(t, queues, 1)
	require.Equal(t, "inspect-queue", queues[0].Name)
	require.EqualValues(t, 3, queues[0].MaxAttempts)
	require.EqualValues(t, 1, queues[0].Pending)
	require.EqualValues(t, 0, queues[0].Dead)

	tasks, total, err := client.Tasks(ctx, "", 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, tasks, 1)
	require.Equal(t, id, tasks[0].ID)
	require.False(t, tasks[0].Claimed)
	require.Equal(t, TaskStatusPending, statusOf(tasks[0].Claimed))

	detail, exists, err := client.Detail(ctx, id)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, TaskStatusPending, detail.Status)
	require.Equal(t, "inspect-queue", detail.Queue)
	require.NotEmpty(t, detail.Payload)

	cancelled, err := client.Cancel(ctx, id)
	require.NoError(t, err)
	require.True(t, cancelled)

	_, total, err = client.Tasks(ctx, "", 0, 10)
	require.NoError(t, err)
	require.Zero(t, total)
}

// TestInspectFiltersByQueueName proves the queue filter: another queue's
// rows stay out of the page, and the total counts only what the filter
// admits.
func TestInspectFiltersByQueueName(t *testing.T) {
	client := newTestClient(t)
	client.Register(NewQueue(func(context.Context, inspectTask) error { return nil }))
	ctx := t.Context()

	_, err := client.Add(inspectTask{}).Ctx(ctx).Save()
	require.NoError(t, err)

	_, total, err := client.Tasks(ctx, "inspect-queue", 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)

	_, total, err = client.Tasks(ctx, "another-queue", 0, 10)
	require.NoError(t, err)
	require.Zero(t, total)
}

// TestInspectAnswersAnArchivedTask proves the archive half of the detail: a
// row the completed table holds answers its state and its failure message,
// and the dead listing pages it.
func TestInspectAnswersAnArchivedTask(t *testing.T) {
	client := newTestClient(t)
	client.Register(NewQueue(func(context.Context, inspectTask) error { return nil }))
	ctx := t.Context()

	archived := &completedRow{
		ID:             uuid.NewV7(),
		Queue:          "inspect-queue",
		Payload:        []byte(`{"reason":"Horcrux"}`),
		Attempts:       3,
		LastDuration:   time.Second,
		Succeeded:      false,
		Error:          strPtr("Expecto failed"),
		ExpiresAt:      timePtr(time.Now().Add(time.Hour)),
		CreatedAt:      time.Now().Add(-time.Minute),
		LastExecutedAt: time.Now(),
	}
	require.NoError(t, insertCompleted(ctx, client.store, archived))

	dead, total, err := client.DeadTasks(ctx, "", 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, dead, 1)
	require.Equal(t, archived.ID, dead[0].ID)
	require.Equal(t, 3, dead[0].Attempts)

	detail, exists, err := client.Detail(ctx, archived.ID)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, TaskStatusFailure, detail.Status)
	require.Equal(t, `{"reason":"Horcrux"}`, string(detail.Payload))
	require.Equal(t, "Expecto failed", *detail.Error)

	queues, err := client.Queues(ctx)
	require.NoError(t, err)
	require.Len(t, queues, 1)
	require.EqualValues(t, 1, queues[0].Dead)
}

// TestInspectReportsAnUnknownTaskAsAbsent proves the not-found answer: an id
// neither table names is absent, not an error — the console renders an empty
// state rather than a failure.
func TestInspectReportsAnUnknownTaskAsAbsent(t *testing.T) {
	client := newTestClient(t)
	ctx := t.Context()

	_, exists, err := client.Detail(ctx, uuid.NewV7())
	require.NoError(t, err)
	require.False(t, exists)
}

func strPtr(s string) *string { return &s }

func timePtr(t time.Time) *time.Time { return &t }
