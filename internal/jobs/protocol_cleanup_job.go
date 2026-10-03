package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/queue"
)

// ProtocolCleanupName is the queue the OAuth/OIDC protocol state's
// retention runs on.
const ProtocolCleanupName = "protocol_cleanup"

// DefaultProtocolCleanupInterval is how often the expired protocol state is
// swept. An expired row is dead to every lookup the moment its timestamp
// passes, so the sweep is housekeeping, not a security boundary; an hour of
// extra rows costs nothing and keeps the schedule quiet.
const DefaultProtocolCleanupInterval = time.Hour

// protocolCleanupGrace is how long past its expiry a row must be before the
// sweep reaps it. Every timestamp in the table is written from the
// application's clock while the sweep compares against the database's, so a
// small allowance keeps a row a skewed clock still considers live out of the
// delete. It is deliberately wider than the lookup filter's own skew
// allowance: a row must never be reaped while a lookup can still accept it.
const protocolCleanupGrace = time.Hour

// protocolCleanupBatch bounds one DELETE, and protocolCleanupRunBound the
// total rows one run may remove: a backlog from a stopped deployment is
// cleared over several runs rather than in one long-running statement.
const (
	protocolCleanupBatch     = 10_000
	protocolCleanupRunBound  = 100_000
	protocolSkewAllowanceSec = 2 * 60
)

// ProtocolCleanupTask deletes the oauth_sessions rows whose expiry has
// passed. The interval it re-enqueues itself with rides in the payload, the
// way the other maintenance jobs carry theirs.
type ProtocolCleanupTask struct {
	IntervalMillis int64 `json:"interval_millis"`
}

// Interval is the wait between two runs.
func (t ProtocolCleanupTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the sweep runs on. A failed sweep is retried
// shortly; the rows it did not delete are still there for the next one.
func (t ProtocolCleanupTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        ProtocolCleanupName,
		MaxAttempts: 3,
		Timeout:     5 * time.Minute,
		Backoff:     time.Minute,
	}
}

// protocolCleanupProcessor deletes the expired rows in bounded batches and
// queues the next run. A row whose expiry is NULL — the shape a logout
// session without a client and a grant without a refresh window carry — is
// never swept: the protocol never judges those rows by the column, so the
// sweep does not either.
func protocolCleanupProcessor(ctx context.Context, task ProtocolCleanupTask, pool *datastore.Postgres) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("protocol_cleanup: queue client missing from context")
	}

	interval := task.Interval()
	if interval <= 0 {
		return errors.New("protocol_cleanup: interval must be positive")
	}

	cutoff := time.Now().Add(-protocolCleanupGrace)
	deleted, err := deleteExpiredProtocolSessions(ctx, pool, cutoff)
	if err != nil {
		return err
	}
	jtisDeleted, err := deleteExpiredJTIs(ctx, pool, cutoff)
	if err != nil {
		return err
	}
	if deleted > 0 || jtisDeleted > 0 {
		slog.InfoContext(ctx, "protocol: expired state deleted",
			"sessions", deleted, "jtis", jtisDeleted)
	}

	// The next run is queued before this one succeeds, so the schedule never
	// depends on the process that ran the last one.
	_, err = client.Add(ProtocolCleanupTask{
		IntervalMillis: task.IntervalMillis,
	}).Ctx(ctx).Wait(interval).Save()
	if err != nil {
		return err
	}
	return nil
}

// deleteExpiredProtocolSessions removes the rows whose expiry passed the
// cutoff, in bounded batches, and answers how many went. The ctid subquery
// is the one form a bounded DELETE takes in Postgres, and it rides the
// expires_at index.
func deleteExpiredProtocolSessions(ctx context.Context, pool *datastore.Postgres, cutoff time.Time) (int, error) {
	total := 0
	for {
		tag, err := pool.Exec(ctx,
			`DELETE FROM public.oauth_sessions
			 WHERE ctid IN (
				 SELECT ctid FROM public.oauth_sessions
				 WHERE expires_at IS NOT NULL AND expires_at < $1
				 LIMIT $2
			 )`, cutoff, protocolCleanupBatch)
		if err != nil {
			return total, fmt.Errorf("protocol_cleanup: delete: %w", err)
		}
		removed := int(tag.RowsAffected())
		total += removed
		if removed < protocolCleanupBatch || total >= protocolCleanupRunBound {
			return total, nil
		}
	}
}

// deleteExpiredJTIs reaps the claimed JWT IDs whose replay window passed,
// the same bounded-batch shape the sessions sweep takes. The expiry is
// NOT NULL here, so the sessions sweep's NULL carve-out does not apply.
func deleteExpiredJTIs(ctx context.Context, pool *datastore.Postgres, cutoff time.Time) (int, error) {
	total := 0
	for {
		tag, err := pool.Exec(ctx,
			`DELETE FROM public.oauth_jtis
			 WHERE ctid IN (
				 SELECT ctid FROM public.oauth_jtis
				 WHERE expires_at < $1
				 LIMIT $2
			 )`, cutoff, protocolCleanupBatch)
		if err != nil {
			return total, fmt.Errorf("protocol_cleanup: delete jtis: %w", err)
		}
		removed := int(tag.RowsAffected())
		total += removed
		if removed < protocolCleanupBatch || total >= protocolCleanupRunBound {
			return total, nil
		}
	}
}

// protocolCleanupSeed is the payload the first run of the sweep is seeded
// with.
func protocolCleanupSeed(interval time.Duration) ProtocolCleanupTask {
	return ProtocolCleanupTask{IntervalMillis: interval.Milliseconds()}
}
