package queue

import (
	"context"
	"strings"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/require"
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

	queues, pagination, err := client.Queues(ctx, "", "", true, 1, 25)
	require.NoError(t, err)
	require.Len(t, queues, 1)
	require.Equal(t, "inspect-queue", queues[0].Name)
	require.EqualValues(t, 3, queues[0].MaxAttempts)
	require.EqualValues(t, 1, queues[0].Pending)
	require.EqualValues(t, 0, queues[0].Dead)
	require.NotNil(t, pagination.TotalItems)
	require.EqualValues(t, 1, *pagination.TotalItems)

	tasks, pagination, err := client.Tasks(ctx, "", "", false, 1, 25)
	require.NoError(t, err)
	require.NotNil(t, pagination.TotalItems)
	require.EqualValues(t, 1, *pagination.TotalItems)
	require.Len(t, tasks, 1)
	require.Equal(t, id, tasks[0].ID)
	require.True(t, strings.HasPrefix(FormatID(tasks[0].ID), "que_"))
	require.False(t, tasks[0].Claimed)
	require.Equal(t, TaskStatusPending, statusOf(tasks[0].Claimed))

	// The search narrows the queue summaries the way the account list's
	// search narrows the accounts.
	_, pagination, err = client.Queues(ctx, "inspect", "", true, 1, 25)
	require.NoError(t, err)
	require.EqualValues(t, 1, *pagination.TotalItems)
	_, pagination, err = client.Queues(ctx, "no-such-queue", "", true, 1, 25)
	require.NoError(t, err)
	require.Zero(t, *pagination.TotalItems)

	detail, exists, err := client.Detail(ctx, id)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, TaskStatusPending, detail.Status)
	require.Equal(t, "inspect-queue", detail.Queue)
	require.NotEmpty(t, detail.Payload)

	cancelled, err := client.Cancel(ctx, id)
	require.NoError(t, err)
	require.True(t, cancelled)

	_, pagination, err = client.Tasks(ctx, "", "", false, 1, 25)
	require.NoError(t, err)
	require.NotNil(t, pagination.TotalItems)
	require.Zero(t, *pagination.TotalItems)
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

	_, pagination, err := client.Tasks(ctx, "inspect-queue", "", false, 1, 25)
	require.NoError(t, err)
	require.EqualValues(t, 1, *pagination.TotalItems)

	_, pagination, err = client.Tasks(ctx, "another-queue", "", false, 1, 25)
	require.NoError(t, err)
	require.Zero(t, *pagination.TotalItems)
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

	dead, pagination, err := client.DeadTasks(ctx, "", "", true, 1, 25)
	require.NoError(t, err)
	require.EqualValues(t, 1, *pagination.TotalItems)
	require.Len(t, dead, 1)
	require.Equal(t, archived.ID, dead[0].ID)
	require.Equal(t, 3, dead[0].Attempts)

	detail, exists, err := client.Detail(ctx, archived.ID)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, TaskStatusFailure, detail.Status)
	require.Equal(t, `{"reason":"Horcrux"}`, string(detail.Payload))
	require.Equal(t, "Expecto failed", *detail.Error)

	queues, pagination, err := client.Queues(ctx, "", "", true, 1, 25)
	require.NoError(t, err)
	require.Len(t, queues, 1)
	require.EqualValues(t, 1, queues[0].Dead)
	require.EqualValues(t, 1, *pagination.TotalItems)
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
