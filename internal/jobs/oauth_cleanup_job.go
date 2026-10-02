package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/identity/oauthsso"
)

// OAuthCleanupName is the queue the flow sweep runs on.
const OAuthCleanupName = "oauth_cleanup"

// DefaultOAuthCleanupInterval is how often the sweep runs. An expired
// ceremony is worthless the moment its window died — the state is the
// credential and no browser still carries it — so an hourly sweep trades
// an hour of dead rows for a quiet delete, the trade the webauthn sweep
// makes.
const DefaultOAuthCleanupInterval = time.Hour

// OAuthCleanupTask deletes the flow rows past their expiry and
// re-enqueues itself. The interval rides the payload, so a run that
// changes the schedule takes effect at the next run.
type OAuthCleanupTask struct {
	IntervalMillis int64 `json:"interval_millis"`
}

// Interval is the wait between two runs.
func (t OAuthCleanupTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the sweep runs on. Like the other maintenance
// jobs it keeps no record of itself and retries shortly: a failed sweep
// is retried, and the rows it did not delete are still there for the
// next one.
func (t OAuthCleanupTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        OAuthCleanupName,
		MaxAttempts: 3,
		Timeout:     5 * time.Minute,
		Backoff:     time.Minute,
	}
}

// oauthCleanupProcessor deletes the expired flow rows and queues the
// next run. The delete is bounded by the expiry and by nothing else: a
// flow row is a ceremony in flight, and an expired one is the state
// every read already refuses.
func oauthCleanupProcessor(ctx context.Context, task OAuthCleanupTask, pool *datastore.Postgres) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("oauth_cleanup: queue client missing from context")
	}

	interval := task.Interval()
	if interval <= 0 {
		return errors.New("oauth_cleanup: interval must be positive")
	}

	deleted, err := deleteExpiredOAuthFlows(ctx, pool, time.Now())
	if err != nil {
		return err
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "oauthsso: expired flow rows deleted", "deleted", deleted)
	}

	// The next run is queued before this one succeeds, so the schedule
	// never depends on the process that ran the last one.
	_, err = client.Add(OAuthCleanupTask{
		IntervalMillis: task.IntervalMillis,
	}).Ctx(ctx).Wait(interval).Save()
	if err != nil {
		return err
	}
	return nil
}

// deleteExpiredOAuthFlows removes the ceremony rows past their expiry
// and answers how many went. The delete runs on the pool like the other
// maintenance jobs, because it removes rows nothing reads back.
func deleteExpiredOAuthFlows(ctx context.Context, pool *datastore.Postgres, now time.Time) (int64, error) {
	repo := oauthsso.NewRepository(pool)
	return repo.DeleteExpiredFlows(ctx, now)
}

// oauthCleanupSeed is the payload the first run of the sweep is seeded
// with.
func oauthCleanupSeed(interval time.Duration) OAuthCleanupTask {
	return OAuthCleanupTask{IntervalMillis: interval.Milliseconds()}
}
