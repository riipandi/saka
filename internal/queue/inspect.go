// The inspection surface: the read-only API an operations screen answers
// through. The engine's own calls (claim, complete, replay) live in the
// client and dispatcher; these methods only read the two tables and the
// registry, so an administrative view can never race a task's lifecycle
// into a wrong answer — worst case it is one tick stale.

package queue

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/riipandi/tango/pkg/responder"
)

// QueueView is one registered queue's configuration and live counts.
type QueueView struct {
	// Name is the queue's registered name.
	Name string
	// MaxAttempts is the attempt budget a task of the queue runs under.
	MaxAttempts int
	// Timeout is the per-execution context deadline, zero when none.
	Timeout time.Duration
	// Retention is how long completed tasks stay archived, nil when the
	// queue does not retain them.
	Retention *Retention
	// Pending is the number of unclaimed tasks in the queue.
	Pending int64
	// Dead is the number of failed tasks the archive holds.
	Dead int64
}

// TaskView is one row of the pending table: a task waiting for a worker,
// running, or past its wait.
type TaskView struct {
	// ID is the task's identifier.
	ID uuid.UUID
	// Queue is the queue the task belongs to.
	Queue string
	// Attempts is the number of times the task has been claimed.
	Attempts int
	// Priority is the claim order weight; a higher number is claimed first.
	Priority int
	// Claimed reports a task a worker holds the claim on.
	Claimed bool
	// WaitUntil is when the task becomes claimable, nil when it already is.
	WaitUntil *time.Time
	// ClaimedAt is when the running task was claimed, nil when waiting.
	ClaimedAt *time.Time
	// CreatedAt is when the task was enqueued.
	CreatedAt time.Time
	// LastExecutedAt is when the last claim was released, nil on a task no
	// attempt has touched.
	LastExecutedAt *time.Time
}

// DeadView is one archived failure: a task that exhausted its attempts and
// kept its payload for the replay.
type DeadView struct {
	// ID is the archived record's identifier — not the task's original one.
	ID uuid.UUID
	// Queue is the queue the task belonged to.
	Queue string
	// Attempts is the attempt budget the task exhausted.
	Attempts int
	// Error is the failure message of the last attempt, nil when the task
	// failed without one.
	Error *string
	// ExpiresAt is when the archive retires the record, nil when the queue
	// retains it forever.
	ExpiresAt *time.Time
	// LastExecutedAt is when the last attempt ran.
	LastExecutedAt time.Time
	// CreatedAt is when the task was originally enqueued.
	CreatedAt time.Time
}

// TaskDetail is one task's full view across the two tables: its state, and
// the payload as the queue stored it.
type TaskDetail struct {
	// Queue is the queue the task belongs to, empty when the task is not in
	// the pending table.
	Queue string
	// Status is the task's state.
	Status TaskStatus
	// Attempts is the number of times the task has been claimed.
	Attempts int
	// Payload is the task's own JSON document, nil when the archive no
	// longer holds it.
	Payload []byte
	// Error is the failure message of the last attempt, nil on a task that
	// never failed.
	Error *string
	// LastDuration is how long the last execution took.
	LastDuration time.Duration
	// CreatedAt is when the task was enqueued.
	CreatedAt time.Time
	// LastExecutedAt is when the last attempt ran.
	LastExecutedAt time.Time
}

// Queues answers one page of the registered queues — the count on every
// queue the search admits, ordered by the key the caller named and paged
// the way the account list pages.
func (c *Client) Queues(ctx context.Context, search, sortBy string, ascending bool, page, limit int) ([]QueueView, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)

	configs := c.queues.all()
	views := make([]QueueView, 0, len(configs))
	for _, cfg := range configs {
		if search != "" && !strings.Contains(strings.ToLower(cfg.Name), strings.ToLower(search)) {
			continue
		}
		pending, err := countPending(ctx, c.store, cfg.Name)
		if err != nil {
			return nil, responder.Pagination{}, err
		}
		dead, err := countDead(ctx, c.store, cfg.Name)
		if err != nil {
			return nil, responder.Pagination{}, err
		}
		views = append(views, QueueView{
			Name:        cfg.Name,
			MaxAttempts: cfg.MaxAttempts,
			Timeout:     cfg.Timeout,
			Retention:   cfg.Retention,
			Pending:     pending,
			Dead:        dead,
		})
	}
	sortQueueViews(views, sortBy, ascending)

	total := len(views)
	start, end := pageBounds(page, limit, total)
	views = views[start:end]
	return views, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// queueSortKeys is the whitelist a queue page's sort key resolves through.
// The rows live in memory, so the sort happens here; the counts make every
// key worth sorting by.
var queueSortKeys = map[string]func(QueueView) string{
	"name":    func(v QueueView) string { return v.Name },
	"pending": func(v QueueView) string { return fmt.Sprintf("%020d", v.Pending) },
	"dead":    func(v QueueView) string { return fmt.Sprintf("%020d", v.Dead) },
}

// sortQueueViews orders the summaries by the named key, unknown keys
// falling back to the name; a numeric key padded as text keeps one
// comparison for strings and numbers alike.
func sortQueueViews(views []QueueView, sortBy string, ascending bool) {
	key, ok := queueSortKeys[sortBy]
	if !ok {
		key = queueSortKeys["name"]
	}
	sort.SliceStable(views, func(i, j int) bool {
		a, b := key(views[i]), key(views[j])
		if ascending {
			return a < b
		}
		return a > b
	})
}

// pageBounds clips a page window to the rows that exist. A page past the
// end answers an empty window rather than an error — the pagination block
// already tells the caller the total.
func pageBounds(page, limit, total int) (int, int) {
	start := (page - 1) * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return start, end
}

// Tasks answers one page of the pending table, with the pagination block the
// list responses carry. An empty queue name answers every queue's rows.
func (c *Client) Tasks(ctx context.Context, queue, sortBy string, ascending bool, page, limit int) ([]TaskView, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)

	rows, total, err := listPending(ctx, c.store, queue, sortBy, ascending, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	views := make([]TaskView, 0, len(rows))
	for _, t := range rows {
		views = append(views, TaskView{
			ID:             t.ID,
			Queue:          t.Queue,
			Attempts:       t.Attempts,
			Priority:       t.Priority,
			Claimed:        t.ClaimedAt != nil,
			WaitUntil:      t.WaitUntil,
			ClaimedAt:      t.ClaimedAt,
			CreatedAt:      t.CreatedAt,
			LastExecutedAt: t.LastExecutedAt,
		})
	}
	return views, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, int(total)), nil
}

// DeadTasks answers one page of the archive's failures, with the pagination
// block the list responses carry. An empty queue name answers every queue's
// dead.
func (c *Client) DeadTasks(ctx context.Context, queue, sortBy string, ascending bool, page, limit int) ([]DeadView, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)

	rows, total, err := listDead(ctx, c.store, queue, sortBy, ascending, responder.Offset(page, limit), limit, now())
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	views := make([]DeadView, 0, len(rows))
	for _, d := range rows {
		views = append(views, DeadView{
			ID:             d.ID,
			Queue:          d.Queue,
			Attempts:       d.Attempts,
			Error:          d.Error,
			ExpiresAt:      d.ExpiresAt,
			LastExecutedAt: d.LastExecutedAt,
			CreatedAt:      d.CreatedAt,
		})
	}
	return views, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, int(total)), nil
}

// Detail answers one task's full view across the two tables. The payload is
// the stored form opened — every payload a sealing client wrote carries the
// prefix, so the answer is always the task's own JSON. A task neither table
// names answers not-found without an error.
func (c *Client) Detail(ctx context.Context, id uuid.UUID) (TaskDetail, bool, error) {
	pending, completed, exists, err := taskDetail(ctx, c.store, id)
	if err != nil || !exists {
		return TaskDetail{Status: TaskStatusNotFound}, false, err
	}

	if pending != nil {
		payload, openErr := c.decrypt(pending.Payload)
		if openErr != nil {
			return TaskDetail{Status: TaskStatusNotFound}, false, openErr
		}
		return TaskDetail{
			Queue:     pending.Queue,
			Status:    statusOf(pending.ClaimedAt != nil),
			Attempts:  pending.Attempts,
			Payload:   payload,
			CreatedAt: pending.CreatedAt,
		}, true, nil
	}

	payload, openErr := c.decrypt(completed.Payload)
	if openErr != nil {
		return TaskDetail{Status: TaskStatusNotFound}, false, openErr
	}
	status := TaskStatusFailure
	if completed.Succeeded {
		status = TaskStatusSuccess
	}
	return TaskDetail{
		Queue:          completed.Queue,
		Status:         status,
		Attempts:       completed.Attempts,
		Payload:        payload,
		Error:          completed.Error,
		LastDuration:   completed.LastDuration,
		CreatedAt:      completed.CreatedAt,
		LastExecutedAt: completed.LastExecutedAt,
	}, true, nil
}

// statusOf maps the claim a pending row holds to the state word the wire
// carries.
func statusOf(claimed bool) TaskStatus {
	if claimed {
		return TaskStatusRunning
	}
	return TaskStatusPending
}
