package queue

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/tango/internal/datastore"
)

// The queue's own tables, created by database/migrations/00008. The engine
// reads and writes them, and nothing else in the process names them.
const (
	tasksTable     = "public.queue_tasks"
	completedTable = "public.queue_tasks_completed"
)

// Store is the database the queue runs on: the shared Postgres pool. The
// transaction callback receives the shared Querier surface, so the engine
// never opens a connection and never names a driver type.
type Store interface {
	datastore.Querier
	WithTx(ctx context.Context, fn func(ctx context.Context, tx datastore.Querier) error) error
}

// taskRow is a row of the pending table: a task added to a queue, ready or
// waiting, claimed or not.
type taskRow struct {
	ID             uuid.UUID
	Queue          string
	Payload        []byte
	Attempts       int
	Priority       int
	WaitUntil      *time.Time
	ClaimedAt      *time.Time
	CreatedAt      time.Time
	LastExecutedAt *time.Time
}

// completedRow is a row of the completed table: a task that exhausted its
// attempts, or succeeded and was retained by its queue's policy.
type completedRow struct {
	ID             uuid.UUID
	Queue          string
	Payload        []byte
	Attempts       int
	Succeeded      bool
	LastDuration   time.Duration
	Error          *string
	ExpiresAt      *time.Time
	CreatedAt      time.Time
	LastExecutedAt time.Time
}

// insertTasks inserts every task of one save operation. The caller owns the
// transaction, so a batch is all-or-nothing with whatever else it runs beside.
func insertTasks(ctx context.Context, q datastore.Querier, tasks []*taskRow) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(tasksTable)
	ib.Cols("id", "queue", "task", "attempts", "priority", "wait_until", "created_at")
	for _, t := range tasks {
		ib.Values(t.ID, t.Queue, t.Payload, t.Attempts, t.Priority, t.WaitUntil, t.CreatedAt)
	}

	query, args := ib.Build()
	_, err := q.Exec(ctx, query, args...)
	return err
}

// claimReady claims the earliest tasks a dispatcher may run right now: never
// claimed, or claimed long enough ago that their worker is considered lost,
// and past their wait. Priority wins over age — a higher number is claimed
// first, ties keeping insertion order. The claim is one statement, so two
// dispatchers — two processes sharing the database — never execute the same
// task twice: a row is locked while it is read and updated, and a competing
// dispatcher skips the locked rows and takes the next ones.
func claimReady(ctx context.Context, q datastore.Querier, at time.Time, reclaimBefore time.Time, limit int) ([]*taskRow, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(tasksTable)
	sb.Where(
		sb.Or(sb.IsNull("claimed_at"), sb.LT("claimed_at", reclaimBefore)),
		sb.Or(sb.IsNull("wait_until"), sb.LE("wait_until", at)),
	)
	sb.OrderBy("priority DESC", "wait_until ASC NULLS FIRST", "id ASC")
	sb.Limit(limit)
	sb.ForUpdate()
	sb.SkipLocked()

	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(tasksTable)
	ub.Set(ub.Assign("claimed_at", at), ub.Incr("attempts"))
	ub.Where(ub.In("id", sb))
	ub.Returning("id", "queue", "task", "attempts", "priority", "wait_until", "created_at", "last_executed_at")

	query, args := ub.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*taskRow
	for rows.Next() {
		t := &taskRow{}
		if err := rows.Scan(&t.ID, &t.Queue, &t.Payload, &t.Attempts, &t.Priority,
			&t.WaitUntil, &t.CreatedAt, &t.LastExecutedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// peekNext reports the wait of the next task the dispatcher could act on,
// whichever comes first among the ready and the delayed. exists is false
// when nothing is claimable; a nil wait with exists true means a task is
// ready now but was not claimed, which happens while every worker is busy.
func peekNext(ctx context.Context, q datastore.Querier, deadline time.Time) (bool, *time.Time, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("wait_until")
	sb.From(tasksTable)
	sb.Where(sb.Or(sb.IsNull("claimed_at"), sb.LT("claimed_at", deadline)))
	sb.OrderBy("wait_until ASC NULLS FIRST", "id ASC")
	sb.Limit(1)

	query, args := sb.Build()
	var wait *time.Time
	err := q.QueryRow(ctx, query, args...).Scan(&wait)
	if errors.Is(err, datastore.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, wait, nil
}

// requeueTask releases a claimed task back to the queue: a failed attempt
// waits for its backoff, a task whose queue was never registered waits for
// its next look.
func requeueTask(ctx context.Context, q datastore.Querier, id uuid.UUID, waitUntil time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(tasksTable)
	ub.Set(
		ub.Assign("claimed_at", nil),
		ub.Assign("wait_until", waitUntil),
		ub.Assign("last_executed_at", time.Now()),
	)
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	_, err := q.Exec(ctx, query, args...)
	return err
}

// deleteTask removes a task from the pending table.
func deleteTask(ctx context.Context, q datastore.Querier, id uuid.UUID) error {
	db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db.DeleteFrom(tasksTable)
	db.Where(db.Equal("id", id))

	query, args := db.Build()
	_, err := q.Exec(ctx, query, args...)
	return err
}

// cancelTask removes an unclaimed task and reports whether it was still
// cancellable. A claimed task is in flight — its worker may already be
// halfway through it — and cannot be revoked from here, so the caller
// reports it as too late rather than pretending it was stopped.
func cancelTask(ctx context.Context, q datastore.Querier, id uuid.UUID) (bool, error) {
	db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db.DeleteFrom(tasksTable)
	db.Where(db.Equal("id", id), db.IsNull("claimed_at"))

	query, args := db.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// insertCompleted archives a finished task. The caller owns the transaction,
// so a task never leaves the pending table without arriving in this one.
func insertCompleted(ctx context.Context, q datastore.Querier, c *completedRow) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(completedTable)
	ib.Cols(
		"id", "queue", "task", "attempts", "last_duration_micro", "succeeded",
		"error", "expires_at", "last_executed_at", "created_at")
	ib.Values(
		c.ID, c.Queue, c.Payload, c.Attempts, c.LastDuration.Microseconds(), c.Succeeded,
		c.Error, c.ExpiresAt, c.LastExecutedAt, c.CreatedAt,
	)

	query, args := ib.Build()
	_, err := q.Exec(ctx, query, args...)
	return err
}

// deleteExpiredCompleted removes the completed records their retention has
// expired and reports how many went.
func deleteExpiredCompleted(ctx context.Context, q datastore.Querier, now time.Time) (int64, error) {
	db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db.DeleteFrom(completedTable)
	db.Where(db.IsNotNull("expires_at"), db.LE("expires_at", now))

	query, args := db.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// countPending reports how many unclaimed tasks a queue holds. The seeding of
// a recurring job reads it, so a restart never adds a second periodic task.
func countPending(ctx context.Context, q datastore.Querier, queue string) (int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(tasksTable)
	sb.Where(sb.Equal("queue", queue), sb.IsNull("claimed_at"))

	query, args := sb.Build()
	var count int64
	err := q.QueryRow(ctx, query, args...).Scan(&count)
	return count, err
}

// flushPending removes every unclaimed task and reports how many went.
// Claimed tasks are in flight or awaiting release, so they finish their
// lifecycle instead of vanishing under a worker.
func flushPending(ctx context.Context, q datastore.Querier) (int64, error) {
	db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db.DeleteFrom(tasksTable)
	db.Where(db.IsNull("claimed_at"))

	query, args := db.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// flushCompleted removes every completed record, retention notwithstanding.
func flushCompleted(ctx context.Context, q datastore.Querier) (int64, error) {
	db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db.DeleteFrom(completedTable)

	query, args := db.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// deadRows lists the archived tasks a replay can bring back: the ones that
// exhausted their attempts and kept their payload, not yet expired under
// their queue's retention. A dead task whose payload is gone cannot be
// replayed, and it stays for the cleanup to expire.
func deadRows(ctx context.Context, q datastore.Querier, at time.Time) ([]*completedRow, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "queue", "task", "attempts", "last_duration_micro", "succeeded", "error", "expires_at", "last_executed_at", "created_at")
	sb.From(completedTable)
	sb.Where(
		sb.Equal("succeeded", false),
		sb.IsNotNull("task"),
		sb.Or(sb.IsNull("expires_at"), sb.GT("expires_at", at)),
	)
	sb.OrderBy("id ASC")

	query, args := sb.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dead []*completedRow
	for rows.Next() {
		c := &completedRow{}
		if err := rows.Scan(&c.ID, &c.Queue, &c.Payload, &c.Attempts, &c.LastDuration,
			&c.Succeeded, &c.Error, &c.ExpiresAt, &c.LastExecutedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		dead = append(dead, c)
	}
	return dead, rows.Err()
}

// countDead reports how many dead tasks a queue's archive holds: the ones
// that exhausted their attempts, whether or not their payload survived.
func countDead(ctx context.Context, q datastore.Querier, queue string) (int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(completedTable)
	sb.Where(sb.Equal("queue", queue), sb.Equal("succeeded", false))

	query, args := sb.Build()
	var count int64
	err := q.QueryRow(ctx, query, args...).Scan(&count)
	return count, err
}

// pendingSortColumns is the whitelist a pending page's sort key resolves
// through; the map is the schema of the ORDER BY, one entry per column the
// wire may name. The tie-break keeps pagination stable: two rows created in
// the same instant still page without repeating or dropping.
var pendingSortColumns = map[string]string{
	"created_at": "created_at",
	"priority":   "priority",
	"attempts":   "attempts",
	"wait_until": "wait_until",
}

// listPending answers one page of the pending table, with the total the
// filter admits. A claimed row is a running task; the page carries the
// claim so the caller tells the two states apart.
func listPending(ctx context.Context, q datastore.Querier, queue, sortBy string, ascending bool, offset, limit int) ([]*taskRow, int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "queue", "task", "attempts", "priority", "wait_until", "claimed_at", "created_at", "last_executed_at")
	sb.From(tasksTable)
	if queue != "" {
		sb.Where(sb.Equal("queue", queue))
	}
	sb.OrderBy(datastore.ListOrder(pendingSortColumns, sortBy, "created_at", ascending), "id ASC")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var tasks []*taskRow
	for rows.Next() {
		t := &taskRow{}
		if err := rows.Scan(&t.ID, &t.Queue, &t.Payload, &t.Attempts, &t.Priority,
			&t.WaitUntil, &t.ClaimedAt, &t.CreatedAt, &t.LastExecutedAt); err != nil {
			return nil, 0, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(tasksTable)
	if queue != "" {
		cb.Where(cb.Equal("queue", queue))
	}
	query, args = cb.Build()
	var total int64
	if err := q.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

// deadSortColumns is the whitelist a dead page's sort key resolves through,
// the same contract the pending page keeps.
var deadSortColumns = map[string]string{
	"created_at":       "created_at",
	"attempts":         "attempts",
	"last_executed_at": "last_executed_at",
}

// listDead answers one page of the archive's failures — the rows a replay
// could bring back — with the total the filter admits. The payload stays in
// the database: the page is a listing, not an inspection.
func listDead(ctx context.Context, q datastore.Querier, queue, sortBy string, ascending bool, offset, limit int, at time.Time) ([]*completedRow, int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "queue", "attempts", "error", "expires_at", "last_executed_at", "created_at")
	sb.From(completedTable)
	sb.Where(
		sb.Equal("succeeded", false),
		sb.Or(sb.IsNull("expires_at"), sb.GT("expires_at", at)),
	)
	if queue != "" {
		sb.Where(sb.Equal("queue", queue))
	}
	sb.OrderBy(datastore.ListOrder(deadSortColumns, sortBy, "created_at", ascending), "id ASC")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var dead []*completedRow
	for rows.Next() {
		c := &completedRow{}
		if err := rows.Scan(&c.ID, &c.Queue, &c.Attempts, &c.Error, &c.ExpiresAt,
			&c.LastExecutedAt, &c.CreatedAt); err != nil {
			return nil, 0, err
		}
		dead = append(dead, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(completedTable)
	cb.Where(
		cb.Equal("succeeded", false),
		cb.Or(cb.IsNull("expires_at"), cb.GT("expires_at", at)),
	)
	if queue != "" {
		cb.Where(cb.Equal("queue", queue))
	}
	query, args = cb.Build()
	var total int64
	if err := q.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return dead, total, nil
}

// taskDetail reads one task's full row: the pending row when it still is
// one, else the archived row. exists is false when neither table names the
// id.
func taskDetail(ctx context.Context, q datastore.Querier, id uuid.UUID) (pending *taskRow, completed *completedRow, exists bool, err error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "queue", "task", "attempts", "priority", "wait_until", "claimed_at", "created_at", "last_executed_at")
	sb.From(tasksTable)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	if rows.Next() {
		t := &taskRow{}
		if scanErr := rows.Scan(&t.ID, &t.Queue, &t.Payload, &t.Attempts, &t.Priority,
			&t.WaitUntil, &t.ClaimedAt, &t.CreatedAt, &t.LastExecutedAt); scanErr != nil {
			return nil, nil, false, scanErr
		}
		return t, nil, true, rows.Err()
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, nil, false, rowsErr
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("id", "queue", "task", "attempts", "succeeded", "last_duration_micro", "error", "expires_at", "created_at", "last_executed_at")
	cb.From(completedTable)
	cb.Where(cb.Equal("id", id))

	query, args = cb.Build()
	completed = &completedRow{}
	err = q.QueryRow(ctx, query, args...).Scan(
		&completed.ID, &completed.Queue, &completed.Payload, &completed.Attempts,
		&completed.Succeeded, &completed.LastDuration, &completed.Error,
		&completed.ExpiresAt, &completed.CreatedAt, &completed.LastExecutedAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	return nil, completed, true, nil
}

// deleteCompletedByIDs removes archived tasks by their IDs. The replay owns
// a transaction, so a dead task never stays archived while its re-queued
// form is already pending. One statement per ID: the dead pile is small, and
// a slice of uuid values has no binary encoding pgx will agree to.
func deleteCompletedByIDs(ctx context.Context, q datastore.Querier, ids []uuid.UUID) error {
	for _, id := range ids {
		db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
		db.DeleteFrom(completedTable)
		db.Where(db.Equal("id", id))

		query, args := db.Build()
		if _, err := q.Exec(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

// taskStatus reports the state of one task across the two tables. The second
// query only runs when the first found nothing, so a completed task costs two
// lookups and everything else one.
func taskStatus(ctx context.Context, q datastore.Querier, id uuid.UUID) (TaskStatus, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("claimed_at")
	sb.From(tasksTable)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	var claimed *time.Time
	err := q.QueryRow(ctx, query, args...).Scan(&claimed)
	switch {
	case err == nil:
		if claimed != nil {
			return TaskStatusRunning, nil
		}
		return TaskStatusPending, nil
	case !errors.Is(err, datastore.ErrNoRows):
		return TaskStatusNotFound, err
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("succeeded")
	cb.From(completedTable)
	cb.Where(cb.Equal("id", id))

	query, args = cb.Build()
	var succeeded bool
	err = q.QueryRow(ctx, query, args...).Scan(&succeeded)
	switch {
	case err == nil:
		if succeeded {
			return TaskStatusSuccess, nil
		}
		return TaskStatusFailure, nil
	case !errors.Is(err, datastore.ErrNoRows):
		return TaskStatusNotFound, err
	}
	return TaskStatusNotFound, nil
}
