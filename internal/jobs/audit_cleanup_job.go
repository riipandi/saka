package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/queue"
)

// AuditCleanupName is the queue the audit-trail retention runs on.
const AuditCleanupName = "audit_cleanup"

// DefaultAuditCleanupInterval is how often the retention is applied. A record
// that aged out is worth deleting within a day rather than within a minute:
// the table is read by an administrator, not by a request, so the schedule
// trades a day of extra rows for a quiet daily delete.
const DefaultAuditCleanupInterval = 24 * time.Hour

// AuditCleanupTask deletes the audit records whose retention has expired. The
// window it applies and the interval it re-enqueues itself with ride in the
// payload, so a run that changes the configuration takes effect at the next
// run rather than needing the queue reseeded.
type AuditCleanupTask struct {
	IntervalMillis int64 `json:"interval_millis"`
	// RetentionDays is how long a record is kept. It is a number of days
	// rather than a duration because the column is a timestamp and the
	// policy is stated in days: a sub-day window is not a thing an operator
	// asks for, and the config key says days.
	RetentionDays int `json:"retention_days"`
}

// Interval is the wait between two runs.
func (t AuditCleanupTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the retention runs on. Like the other maintenance
// jobs it keeps no record of itself and retries shortly: a failed sweep is
// retried, and the rows it did not delete are still there for the next one.
func (t AuditCleanupTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        AuditCleanupName,
		MaxAttempts: 3,
		Timeout:     5 * time.Minute,
		Backoff:     time.Minute,
	}
}

// auditCleanupProcessor deletes the expired records and queues the next run.
//
// The delete is bounded by the retention window and by nothing else: an
// administrator's own record is not exempt, because a log whose entries can
// be kept selectively is a log whose entries can be removed selectively.
func auditCleanupProcessor(ctx context.Context, task AuditCleanupTask, pool *datastore.Postgres) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("audit_cleanup: queue client missing from context")
	}

	interval := task.Interval()
	if interval <= 0 {
		return errors.New("audit_cleanup: interval must be positive")
	}
	if task.RetentionDays <= 0 {
		return errors.New("audit_cleanup: retention days must be positive")
	}

	cutoff := time.Now().AddDate(0, 0, -task.RetentionDays)
	deleted, err := deleteExpiredAuditRecords(ctx, pool, cutoff)
	if err != nil {
		return err
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "audit: expired records deleted",
			"deleted", deleted, "retention_days", task.RetentionDays)
	}

	// The next run is queued before this one succeeds, so the schedule never
	// depends on the process that ran the last one.
	_, err = client.Add(AuditCleanupTask{
		IntervalMillis: task.IntervalMillis,
		RetentionDays:  task.RetentionDays,
	}).Ctx(ctx).Wait(interval).Save()
	if err != nil {
		return err
	}
	return nil
}

// deleteExpiredAuditRecords removes the records older than the cutoff and
// answers how many went.
//
// The query is built with the query builder rather than written out, the way
// every query against an application table is, and it names the table through
// the vocabulary package so a rename touches one line.
func deleteExpiredAuditRecords(ctx context.Context, pool *datastore.Postgres, cutoff time.Time) (int64, error) {
	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(entity.TableAuditLogs)
	dbl.Where(dbl.LessThan("created_at", cutoff))

	query, args := dbl.Build()
	tag, err := pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("audit_cleanup: delete: %w", err)
	}
	return tag.RowsAffected(), nil
}

// auditCleanupSeed is the payload the first run of the retention is seeded
// with.
func auditCleanupSeed(interval time.Duration, retentionDays int) AuditCleanupTask {
	return AuditCleanupTask{
		IntervalMillis: interval.Milliseconds(),
		RetentionDays:  retentionDays,
	}
}
