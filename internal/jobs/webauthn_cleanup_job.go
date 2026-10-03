package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/queue"
)

// WebauthnCleanupName is the queue the ceremony-session sweep runs on.
const WebauthnCleanupName = "webauthn_cleanup"

// DefaultWebauthnCleanupInterval is how often the sweep runs. An expired
// ceremony is worthless the moment its challenge died — the browser that
// held it is gone — so an hourly sweep trades an hour of dead rows for a
// quiet delete, the trade the audit retention makes at a day.
const DefaultWebauthnCleanupInterval = time.Hour

// webauthnSessionsTable is the table the sweep deletes from. The schema
// belongs to the migrations and the queries to the webauthn feature; the
// sweep runs on the pool like the other maintenance jobs, because it deletes
// rows nothing reads back and needs no feature to own it.
const webauthnSessionsTable = "public.webauthn_sessions"

// WebauthnCleanupTask deletes the ceremony sessions past their expiry and
// re-enqueues itself. The interval rides the payload, so a run that changes
// the schedule takes effect at the next run.
type WebauthnCleanupTask struct {
	IntervalMillis int64 `json:"interval_millis"`
}

// Interval is the wait between two runs.
func (t WebauthnCleanupTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the sweep runs on. Like the other maintenance
// jobs it keeps no record of itself and retries shortly: a failed sweep is
// retried, and the rows it did not delete are still there for the next one.
func (t WebauthnCleanupTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        WebauthnCleanupName,
		MaxAttempts: 3,
		Timeout:     5 * time.Minute,
		Backoff:     time.Minute,
	}
}

// webauthnCleanupProcessor deletes the expired ceremony sessions and queues
// the next run.
//
// The delete is bounded by the expiry and by nothing else: a ceremony row
// names no live credential and no live session, and an expired challenge is
// the one state every verify path already refuses.
func webauthnCleanupProcessor(ctx context.Context, task WebauthnCleanupTask, pool *datastore.Postgres) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("webauthn_cleanup: queue client missing from context")
	}

	interval := task.Interval()
	if interval <= 0 {
		return errors.New("webauthn_cleanup: interval must be positive")
	}

	deleted, err := deleteExpiredWebauthnSessions(ctx, pool, time.Now())
	if err != nil {
		return err
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "webauthn: expired ceremony sessions deleted", "deleted", deleted)
	}

	// The next run is queued before this one succeeds, so the schedule never
	// depends on the process that ran the last one.
	_, err = client.Add(WebauthnCleanupTask{
		IntervalMillis: task.IntervalMillis,
	}).Ctx(ctx).Wait(interval).Save()
	if err != nil {
		return err
	}
	return nil
}

// deleteExpiredWebauthnSessions removes the ceremony rows past their expiry
// and answers how many went.
func deleteExpiredWebauthnSessions(ctx context.Context, pool *datastore.Postgres, now time.Time) (int64, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(webauthnSessionsTable)
	dbt.Where(dbt.LessThan("expires_at", now))

	query, args := dbt.Build()
	tag, err := pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// webauthnCleanupSeed is the payload the first run of the sweep is seeded
// with.
func webauthnCleanupSeed(interval time.Duration) WebauthnCleanupTask {
	return WebauthnCleanupTask{IntervalMillis: interval.Milliseconds()}
}
