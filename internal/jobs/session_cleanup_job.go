package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/modules/identity/session"
)

// SessionCleanupName is the queue the session retention runs on.
const SessionCleanupName = "session_cleanup"

// DefaultSessionCleanupInterval is how often the retention is applied. A row
// that aged out is worth deleting within a day rather than within a minute:
// the table is read by an account's own session center, not by a request, so
// the schedule trades a day of extra rows for a quiet daily delete — the
// trade the audit retention makes.
const DefaultSessionCleanupInterval = 24 * time.Hour

// SessionCleanupTask deletes the session rows whose end is older than the
// retention window. The window it applies and the interval it re-enqueues
// itself with ride in the payload, so a run that changes the configuration
// takes effect at the next run rather than needing the queue reseeded.
type SessionCleanupTask struct {
	IntervalMillis int64 `json:"interval_millis"`
	// RetentionDays is how long a row outlives its end. It is a number of
	// days rather than a duration because the policy is stated in days: the
	// window the config key names is the window this payload carries.
	RetentionDays int `json:"retention_days"`
}

// Interval is the wait between two runs.
func (t SessionCleanupTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the retention runs on. Like the other maintenance
// jobs it keeps no record of itself and retries shortly: a failed sweep is
// retried, and the rows it did not delete are still there for the next one.
func (t SessionCleanupTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        SessionCleanupName,
		MaxAttempts: 3,
		Timeout:     5 * time.Minute,
		Backoff:     time.Minute,
	}
}

// sessionCleanupProcessor deletes the retention-expired rows and queues the
// next run.
//
// The delete is bounded by the window and by nothing else: a row inside it is
// untouched whatever its account, and the session center's count stops being a
// lifetime ledger only because every account ages the same way.
func sessionCleanupProcessor(ctx context.Context, task SessionCleanupTask, pool *datastore.Postgres) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("session_cleanup: queue client missing from context")
	}

	interval := task.Interval()
	if interval <= 0 {
		return errors.New("session_cleanup: interval must be positive")
	}
	if task.RetentionDays <= 0 {
		return errors.New("session_cleanup: retention days must be positive")
	}

	deleted, err := session.NewRepository().DeleteRetentionExpired(ctx, pool, time.Now().AddDate(0, 0, -task.RetentionDays))
	if err != nil {
		return err
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "session: retention-expired rows deleted",
			"deleted", deleted, "retention_days", task.RetentionDays)
	}

	// The next run is queued before this one succeeds, so the schedule never
	// depends on the process that ran the last one.
	_, err = client.Add(SessionCleanupTask{
		IntervalMillis: task.IntervalMillis,
		RetentionDays:  task.RetentionDays,
	}).Ctx(ctx).Wait(interval).Save()
	if err != nil {
		return err
	}
	return nil
}

// sessionCleanupSeed is the payload the first run of the retention is seeded
// with.
func sessionCleanupSeed(interval time.Duration, retentionDays int) SessionCleanupTask {
	return SessionCleanupTask{
		IntervalMillis: interval.Milliseconds(),
		RetentionDays:  retentionDays,
	}
}
