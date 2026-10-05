package queue

import (
	"context"
	"time"
	"uuid"

	"github.com/riipandi/saka/framework/datastore"
)

// Task is a task that can be placed into a queue for execution. Any type
// qualifies once it declares the queue it belongs to; the payload fields are
// the task's own, marshalled with encoding/json/v2.
type Task interface {
	// Config returns the configuration options for the queue this task will
	// be placed in.
	Config() QueueConfig
}

// TaskAddOp facilitates adding Tasks to the queue.
type TaskAddOp struct {
	client   *Client
	ctx      context.Context
	tasks    []Task
	wait     *time.Time
	priority int
	executor datastore.Querier
}

// Ctx sets the context of the operation.
func (t *TaskAddOp) Ctx(ctx context.Context) *TaskAddOp {
	t.ctx = ctx
	return t
}

// At sets the time the task should not be executed until.
func (t *TaskAddOp) At(processAt time.Time) *TaskAddOp {
	t.wait = &processAt
	return t
}

// Wait instructs the task to wait a given duration before it is executed.
func (t *TaskAddOp) Wait(duration time.Duration) *TaskAddOp {
	at := now().Add(duration)
	return t.At(at)
}

// Priority sets the execution priority of the tasks: a higher number is
// claimed before a lower one, ties keeping insertion order. Zero is the
// default, and negative values are refused at save time.
func (t *TaskAddOp) Priority(priority int) *TaskAddOp {
	t.priority = priority
	return t
}

// Executor writes the tasks through an open transaction, so a task is
// enqueued with the database change that caused it or not at all. The caller
// commits the transaction and then calls Notify on the client: the dispatcher
// cannot observe a commit it does not own.
func (t *TaskAddOp) Executor(tx datastore.Querier) *TaskAddOp {
	t.executor = tx
	return t
}

// Save saves the tasks, so they can be queued for execution, and returns
// their IDs in the order the tasks were given.
func (t *TaskAddOp) Save() ([]string, error) {
	return t.client.save(t)
}

// TaskStatus is the state of a task, as reported by Status.
type TaskStatus int

const (
	// TaskStatusPending indicates the task is awaiting execution.
	TaskStatusPending TaskStatus = iota
	// TaskStatusRunning indicates the task is being executed.
	TaskStatusRunning
	// TaskStatusSuccess indicates the task completed successfully.
	TaskStatusSuccess
	// TaskStatusFailure indicates the task execution failed.
	TaskStatusFailure
	// TaskStatusNotFound indicates the task was not found in the database.
	TaskStatusNotFound
)

// newTask builds the row one payload inserts as. The ID is generated in the
// application — time-sortable, so the pending table reads in insertion order
// without a second index — because a caller that enqueues inside its own
// transaction wants to reference the task before any commit has happened.
func newTask(task Task, payload []byte, wait *time.Time, priority int) (*taskRow, error) {
	if priority < 0 {
		return nil, errNegativePriority
	}
	return &taskRow{
		ID:        uuid.NewV7(),
		Queue:     task.Config().Name,
		Payload:   payload,
		Priority:  priority,
		WaitUntil: wait,
		CreatedAt: now(),
	}, nil
}
