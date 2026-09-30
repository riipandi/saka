package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/webhook"
)

// WebhookRunner is the seam the delivery processor runs through: the webhook
// area's service, resolved at task-run time so the queue never has to be
// built after the area is.
type WebhookRunner interface {
	// RunDelivery performs one delivery attempt for the delivery the task
	// names.
	RunDelivery(ctx context.Context, deliveryID string) error
	// PruneAttempts deletes the attempt rows the retention window has aged
	// out.
	PruneAttempts(ctx context.Context) (int64, error)
}

// webhookDeliverProcessor performs one delivery attempt. The attempt's
// failure is the error the queue retries on; a delivery whose endpoint
// cannot receive — gone, disabled, unsigned — is marked failed inside the
// runner, and the task ends quietly rather than burning the attempts.
func webhookDeliverProcessor(ctx context.Context, task webhook.DeliverTask, runner WebhookRunner) error {
	if runner == nil {
		return errors.New("webhook_deliver: no delivery runner is wired")
	}
	return runner.RunDelivery(ctx, task.DeliveryID)
}

// WebhookPruneName is the queue the attempt retention runs on.
const WebhookPruneName = "webhook_prune"

// DefaultWebhookPruneInterval is how often the attempt retention is applied.
// An attempt row ages out within a day of its window: the attempts are an
// operator's failure story, and a day of extra rows costs nothing.
const DefaultWebhookPruneInterval = 24 * time.Hour

// WebhookPruneTask deletes the delivery attempts older than the retention
// window. The interval it re-enqueues itself with rides in the payload, so a
// run that changes the schedule takes effect at the next run.
type WebhookPruneTask struct {
	IntervalMillis int64 `json:"interval_millis"`
}

// Interval is the wait between two runs.
func (t WebhookPruneTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the retention runs on. Like the other maintenance
// jobs it keeps no record of itself and retries shortly.
func (t WebhookPruneTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        WebhookPruneName,
		MaxAttempts: 3,
		Timeout:     5 * time.Minute,
		Backoff:     time.Minute,
	}
}

// webhookPruneProcessor deletes the aged-out attempts and queues the next
// run. The delivery rows keep their own summary, so the pruning never makes
// a list lie about what happened.
func webhookPruneProcessor(ctx context.Context, task WebhookPruneTask, runner WebhookRunner, log *slog.Logger) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("webhook_prune: queue client missing from context")
	}
	interval := task.Interval()
	if interval <= 0 {
		return errors.New("webhook_prune: interval must be positive")
	}

	deleted, err := runner.PruneAttempts(ctx)
	if err != nil {
		return err
	}
	if deleted > 0 {
		log.InfoContext(ctx, "webhook: aged-out attempts deleted", "deleted", deleted)
	}

	// The next run is queued before this one succeeds, so the schedule never
	// depends on the process that ran the last one.
	_, err = client.Add(WebhookPruneTask{IntervalMillis: task.IntervalMillis}).Ctx(ctx).Wait(interval).Save()
	return err
}

// webhookPruneSeed is the payload the first run of the retention is seeded
// with.
func webhookPruneSeed(interval time.Duration) WebhookPruneTask {
	return WebhookPruneTask{IntervalMillis: interval.Milliseconds()}
}
