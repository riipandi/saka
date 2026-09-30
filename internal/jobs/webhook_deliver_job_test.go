package jobs

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/fetcher"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/webhook"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"
)

// The hex the test cipher builds from: sixty-four hex characters, the shape
// the application secret carries.
const webhookTestKeyHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// TestWebhookPruneDeletesAgedOutAttemptsAndReschedules runs the retention
// through the real service over a migrated database: the attempts older than
// the window go, the delivery keeps its summary, and the next run is queued
// before the current one ends.
func TestWebhookPruneDeletesAgedOutAttemptsAndReschedules(t *testing.T) {
	pool := testutils.MigratedPostgres(t, "webhook_jobs_test")
	log := slog.New(slog.DiscardHandler)

	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		Logger:       slog.Default(),
		NumWorkers:   1,
		ReleaseAfter: 10 * time.Minute,
	})
	require.NoError(t, err)

	cipher, err := crypto.NewCipherFromHex(webhookTestKeyHex)
	require.NoError(t, err)

	// The runner is the real service: the retention is its rows' deletion
	// path, so the test drives the same object the queue will.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(receiver.Close)
	fetchClient, err := fetcher.New(config.Default(), log)
	require.NoError(t, err)
	service := webhook.NewService(pool, audit.NewRecorder(log), client, fetchClient, cipher, log)

	// The prune processor is wired the way Register wires it, with the real
	// service as the runner. The delivery queue is registered to a no-op:
	// this test pins the retention, and a live delivery would add attempts
	// of its own behind the test's back.
	client.Register(queue.NewQueue[webhook.DeliverTask](func(ctx context.Context, task webhook.DeliverTask) error {
		return nil
	}))
	client.Register(queue.NewQueue[WebhookPruneTask](func(ctx context.Context, task WebhookPruneTask) error {
		return webhookPruneProcessor(ctx, task, service, log)
	}))

	endpoint, _, err := service.Create(t.Context(), webhook.CreateParams{
		Name:     "pruning",
		Endpoint: receiver.URL,
		Method:   http.MethodPost,
	})
	require.NoError(t, err)

	// Three attempts the runner records the honest way: the test delivery
	// against the failing receiver, tried until the delivery fails.
	require.NoError(t, service.Test(t.Context(), endpoint.ID))
	deliveries, _, err := service.ListDeliveries(t.Context(), endpoint.ID, 1, 10)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	deliveryID := deliveries[0].Delivery.ID.String()
	for i := 0; i < 3; i++ {
		require.Error(t, service.RunDelivery(t.Context(), deliveryID))
	}

	agedOut := time.Now().AddDate(0, 0, -8)
	_, execErr := pool.Exec(t.Context(), `UPDATE public.webhook_delivery_attempts SET created_at = $1 WHERE attempt_number <= 2`, agedOut)
	require.NoError(t, execErr)

	// The run goes through the queue's own dispatcher, the way a serve run
	// does, and the successor it queues is the signal that it finished. The
	// task is added with a long interval, so the successor sits far enough
	// out that the pending count sees one schedule rather than a loop.
	_, err = client.Add(WebhookPruneTask{IntervalMillis: time.Hour.Milliseconds()}).Save()
	require.NoError(t, err)

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	client.Start(runCtx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer stopCancel()
		client.Stop(stopCtx)
	})
	require.Eventually(t, func() bool {
		var waitUntil *time.Time
		scanErr := pool.QueryRow(t.Context(),
			`SELECT wait_until FROM public.queue_tasks WHERE queue = $1`,
			WebhookPruneName).Scan(&waitUntil)
		return scanErr == nil && waitUntil != nil && waitUntil.After(time.Now().Add(30*time.Minute))
	}, 20*time.Second, 25*time.Millisecond,
		"the prune run must finish and queue its successor")

	// The delivery keeps its own summary, and the attempt inside the window
	// is the one its view rides.
	views, _, err := service.ListDeliveries(t.Context(), endpoint.ID, 1, 10)
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, 3, views[0].Delivery.AttemptCount, "the pruning never makes a list lie about what happened")
	require.NotNil(t, views[0].Attempt)
	assert.Equal(t, 3, views[0].Attempt.AttemptNumber)

	// The next run is queued before this one ends, so the schedule survives
	// the process that ran the last one — the successor above is that
	// proof; the count here is a second view of the same fact.
	pending, err := client.Pending(t.Context(), WebhookPruneName)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending)
}
