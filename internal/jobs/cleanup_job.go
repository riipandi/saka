// Package jobs holds the concrete jobs the application runs on the queue.
//
// A job file — one job per *_job.go — declares the task type it enqueues, the
// queue configuration that governs it, and the processor that executes it.
// register.go lists every job the application runs, so the composition root
// has one call to make and a job has one place to be spelled out. The engine
// itself (internal/queue) knows none of them: jobs are application code, the
// queue is infrastructure.
//
// A recurring job keeps its own schedule: its processor enqueues the next
// instance before returning, and Register seeds the first one only while no
// task of that queue is pending, so a restart never adds a second schedule.
package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/identity/multifactor"
)

// CleanupName is the queue the maintenance job runs on.
const CleanupName = "cleanup"

// CleanupTask purges the completed task records their retention has expired.
// The interval it re-enqueues itself with rides in the payload, so the
// schedule a run carries is the schedule that run was seeded with. It is
// written as its number of milliseconds: a duration is an int64, and the
// payload is read back years later by whatever version then runs.
type CleanupTask struct {
	IntervalMillis int64 `json:"interval_millis"`
}

// Interval is the wait between two runs.
func (t CleanupTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the cleanup runs on. It is internal maintenance,
// so it keeps no record of itself and retries shortly.
func (t CleanupTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        CleanupName,
		MaxAttempts: 3,
		Timeout:     5 * time.Minute,
		Backoff:     time.Minute,
	}
}

// cleanupProcessor deletes the expired records and queues the next run. The
// sweep also reaches the auth tables whose rows no query cleans up by
// itself: the unconfirmed TOTP enrollments whose ceremony never completed,
// and the sign-in bridges past their life — rows the runtime refuses on
// read, but which hold sealed secrets until someone deletes them.
func cleanupProcessor(ctx context.Context, task CleanupTask, pool *datastore.Postgres) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("cleanup: queue client missing from context")
	}

	interval := task.Interval()
	if interval <= 0 {
		return errors.New("cleanup: interval must be positive")
	}

	deleted, err := client.DeleteExpiredCompleted(ctx)
	if err != nil {
		return err
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "queue: deleted expired completed tasks",
			"deleted", deleted)
	}

	// The window matches the tables' own lifetimes: an unconfirmed
	// enrollment's ceremony is minutes long, a bridge's life shorter still,
	// so anything older than an hour is dead weight no caller can reach.
	purged, err := purgeExpiredAuthRows(ctx, pool, time.Now().Add(-time.Hour))
	if err != nil {
		return err
	}
	if purged[0] > 0 || purged[1] > 0 {
		slog.InfoContext(ctx, "queue: purged expired auth rows",
			"unconfirmed_enrollments", purged[0], "pending_bridges", purged[1])
	}

	// The next run is queued before this one succeeds, so the schedule never
	// depends on the process that ran the last one.
	_, err = client.Add(CleanupTask{IntervalMillis: task.IntervalMillis}).Ctx(ctx).Wait(interval).Save()
	if err != nil {
		return err
	}

	slog.DebugContext(ctx, "queue: cleanup rescheduled",
		"interval", interval.String())
	return nil
}

// purgeExpiredAuthRows deletes the multifactor rows their own lifetimes have
// ended: enrollments never confirmed and bridges never completed. The deletes
// live in the multifactor repository because the tables are its vocabulary;
// the job is the schedule that calls them.
func purgeExpiredAuthRows(ctx context.Context, pool *datastore.Postgres, cutoff time.Time) ([2]int, error) {
	repo := multifactor.NewRepository()
	unconfirmed, err := repo.DeleteUnconfirmedTotpBefore(ctx, pool, cutoff)
	if err != nil {
		return [2]int{}, err
	}
	bridges, err := repo.DeleteExpiredPending(ctx, pool, cutoff)
	if err != nil {
		return [2]int{}, err
	}
	return [2]int{unconfirmed, bridges}, nil
}

// cleanupSeed is the payload the first run of the maintenance job is seeded
// with.
func cleanupSeed(interval time.Duration) CleanupTask {
	return CleanupTask{IntervalMillis: interval.Milliseconds()}
}
