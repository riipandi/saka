package queue

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/pkg/crypto"
)

// testKey is a 64-character hex key, the shape APP_SECRET_KEY stores.
const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// Adding several tasks in one operation must not let them share the encode
// buffer: each payload is the buffer's own backing array until cloned, and a
// second encode into the same buffer would rewrite the first task's bytes.
func TestAddEncodesEveryPayloadIntoItsOwnBytes(t *testing.T) {
	client := newTestClient(t)

	ids := save(t, client.Add(
		probeTask{Name: "first"},
		probeTask{Name: "second"},
		probeTask{Name: "third"},
	))

	rows, err := client.store.Query(t.Context(),
		"SELECT task FROM public.queue_tasks WHERE id = ANY($1) ORDER BY id", ids)
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var payload []byte
		require.NoError(t, rows.Scan(&payload))
		var task probeTask
		require.NoError(t, json.Unmarshal(payload, &task), "payload %q", payload)
		names = append(names, task.Name)
	}
	require.NoError(t, rows.Err())

	require.Equal(t, []string{"first", "second", "third"}, names,
		"every task must decode to its own payload, not the last one written")
}

// A batch of tasks is one statement's worth of work: all of them land, in a
// single transaction, and none is left half-written.
func TestAddSavesTheWholeBatchAtomically(t *testing.T) {
	client := newTestClient(t)

	tasks := make([]Task, 25)
	for i := range tasks {
		tasks[i] = probeTask{Name: "batch"}
	}
	ids := save(t, client.Add(tasks...))
	require.Len(t, ids, 25)

	var count int
	require.NoError(t, client.store.QueryRow(t.Context(),
		"SELECT count(*) FROM public.queue_tasks").Scan(&count))
	require.Equal(t, 25, count)
}

// The wait applies to every task of the batch, not just the first one.
func TestAddDelaysTheWholeBatch(t *testing.T) {
	client := newTestClient(t)

	save(t, client.Add(probeTask{Name: "a"}, probeTask{Name: "b"}).Wait(50*time.Millisecond))

	rows, err := client.store.Query(t.Context(),
		"SELECT wait_until FROM public.queue_tasks ORDER BY id")
	require.NoError(t, err)
	defer rows.Close()

	var waits []time.Time
	for rows.Next() {
		var wait *time.Time
		require.NoError(t, rows.Scan(&wait))
		require.NotNil(t, wait)
		waits = append(waits, *wait)
	}
	require.NoError(t, rows.Err())
	require.Len(t, waits, 2)
	require.WithinDuration(t, waits[0], waits[1], time.Second)
}

// dlqTask always fails and keeps its payload in the archive: the replay's
// raw material.
type dlqTask struct {
	Name string `json:"name"`
}

func (t dlqTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "dlq_probe",
		MaxAttempts: 2,
		Timeout:     2 * time.Second,
		Backoff:     10 * time.Millisecond,
		Retention: &Retention{
			Data: &RetainData{},
		},
	}
}

// bareTask always fails but its queue retains no payload, so the dead task's
// content is gone.
type bareTask struct {
	Name string `json:"name"`
}

func (t bareTask) Config() QueueConfig {
	return QueueConfig{
		Name:        "bare_probe",
		MaxAttempts: 1,
		Timeout:     2 * time.Second,
		Backoff:     10 * time.Millisecond,
		Retention:   &Retention{},
	}
}

// A dead task comes back with a fresh identity and a fresh attempt budget,
// and its archived form is gone once the re-queued one exists.
func TestReplayDeadRequeuesTheDeadTasks(t *testing.T) {
	client := newTestClient(t)

	var executions atomic.Int64
	runWith(t, client, NewQueue(func(ctx context.Context, task dlqTask) error {
		executions.Add(1)
		return errors.New("always fails")
	}))

	save(t, client.Add(dlqTask{Name: "dead"}))
	require.Eventually(t, func() bool {
		dead, err := client.Dead(t.Context(), "dlq_probe")
		return err == nil && dead == 1
	}, 10*time.Second, 20*time.Millisecond, "the task must exhaust and die")

	replayed, err := client.ReplayDead(t.Context(), "")
	require.NoError(t, err)
	require.EqualValues(t, 1, replayed)

	// The replayed task runs its own attempt budget and dies again.
	require.Eventually(t, func() bool {
		dead, err := client.Dead(t.Context(), "dlq_probe")
		return err == nil && dead == 1
	}, 10*time.Second, 20*time.Millisecond, "the replayed task must exhaust again")
	require.EqualValues(t, 4, executions.Load(), "two attempts before the replay, two after")
}

// A replay that names a queue brings back only that queue's dead: the other
// queues' dead piles stay archived, and the response count covers what moved.
func TestReplayDeadNarrowsToTheNamedQueue(t *testing.T) {
	client := newTestClient(t)

	runWith(t, client, NewQueue(func(ctx context.Context, task dlqTask) error {
		return errors.New("always fails")
	}))
	runWith(t, client, NewQueue(func(ctx context.Context, task retainedProbeTask) error {
		return errors.New("always fails")
	}))

	save(t, client.Add(dlqTask{Name: "first"}))
	save(t, client.Add(retainedProbeTask{Name: "second"}))
	require.Eventually(t, func() bool {
		dlq, err := client.Dead(t.Context(), "dlq_probe")
		retained, err2 := client.Dead(t.Context(), "retained")
		return err == nil && err2 == nil && dlq == 1 && retained == 1
	}, 10*time.Second, 20*time.Millisecond, "both queues must hold one dead task")

	// The named queue's replay moves one; the other queue's dead stays put.
	replayed, err := client.ReplayDead(t.Context(), "dlq_probe")
	require.NoError(t, err)
	require.EqualValues(t, 1, replayed)

	retained, err := client.Dead(t.Context(), "retained")
	require.NoError(t, err)
	require.EqualValues(t, 1, retained, "the other queue's dead task must stay archived")

	// An absent name replays what is left.
	replayed, err = client.ReplayDead(t.Context(), "")
	require.NoError(t, err)
	require.EqualValues(t, 1, replayed)
}

// A dead task whose queue retained no payload cannot come back: the replay
// skips it and the archive keeps it for the cleanup to expire.
func TestReplaySkipsDeadTasksWithoutAPayload(t *testing.T) {
	client := newTestClient(t)

	runWith(t, client, NewQueue(func(ctx context.Context, task bareTask) error {
		return errors.New("always fails")
	}))

	save(t, client.Add(bareTask{Name: "gone"}))
	require.Eventually(t, func() bool {
		dead, err := client.Dead(t.Context(), "bare_probe")
		return err == nil && dead == 1
	}, 10*time.Second, 20*time.Millisecond)

	replayed, err := client.ReplayDead(t.Context(), "")
	require.NoError(t, err)
	require.Zero(t, replayed)

	var count int
	require.NoError(t, client.store.QueryRow(t.Context(),
		"SELECT count(*) FROM public.queue_tasks_completed WHERE queue = 'bare_probe'").Scan(&count))
	require.Equal(t, 1, count, "the payload-less dead task stays archived")
}

// With an encryptor on the client, the payload rests sealed and the queue
// still hands the processor the plaintext.
func TestEncryptedTasksRestSealedAndRunDecoded(t *testing.T) {
	cipher, err := crypto.NewCipherFromHex(testKey)
	require.NoError(t, err)

	client, err := NewClient(ClientConfig{
		Store:        migratedPool(t),
		Logger:       slog.Default(),
		NumWorkers:   2,
		ReleaseAfter: time.Hour,
		Encryptor:    cipher,
	})
	require.NoError(t, err)

	var received atomic.Pointer[string]
	runWith(t, client, NewQueue(func(ctx context.Context, task probeTask) error {
		received.Store(&task.Name)
		return nil
	}))

	save(t, client.Add(probeTask{Name: "secret"}))

	require.Eventually(t, func() bool {
		return received.Load() != nil && *received.Load() == "secret"
	}, 10*time.Second, 20*time.Millisecond, "the processor must see its own task")

	rows, err := client.store.Query(t.Context(),
		"SELECT task FROM public.queue_tasks_completed")
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		require.NoError(t, rows.Scan(&payload))
		require.True(t, bytes.HasPrefix(payload, []byte(crypto.EncPrefix)),
			"the archived payload must rest sealed, got %q", payload)
	}
	require.NoError(t, rows.Err())
}

// A task written before the deployment started sealing reads as the
// plaintext it is, so flipping the flag never strands an in-flight task.
func TestSealingClientReadsAPlaintextTask(t *testing.T) {
	pool := migratedPool(t)

	plain, err := NewClient(ClientConfig{
		Store: pool, Logger: slog.Default(), NumWorkers: 1, ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)
	plain.Register(NewQueue(func(ctx context.Context, task probeTask) error { return nil }))
	_, err = plain.Add(probeTask{Name: "written plain"}).Save()
	require.NoError(t, err)

	cipher, err := crypto.NewCipherFromHex(testKey)
	require.NoError(t, err)
	sealed, err := NewClient(ClientConfig{
		Store: pool, Logger: slog.Default(), NumWorkers: 1, ReleaseAfter: time.Hour,
		Encryptor: cipher,
	})
	require.NoError(t, err)

	var received atomic.Int64
	sealed.Register(NewQueue(func(ctx context.Context, task probeTask) error {
		received.Add(1)
		return nil
	}))
	sealed.Start(t.Context())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		sealed.Stop(ctx)
	})

	require.Eventually(t, func() bool {
		return received.Load() == 1
	}, 10*time.Second, 20*time.Millisecond, "the plaintext task must run under the sealing client")
}
