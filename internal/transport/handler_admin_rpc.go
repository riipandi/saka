package transport

import (
	"context"
	"errors"
	"math"
	"time"
	"uuid"

	"connectrpc.com/connect"

	systemv1 "github.com/riipandi/tango/codegen/proto/go/tango/system/v1"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/internal/scheduler"
)

// rpcQueueService answers the queue's administrative procedures over
// ConnectRPC. The engine owns every fact and every action; this layer maps
// the wire only — the same split the health service keeps, so the two
// surfaces of one fact cannot drift.
type rpcQueueService struct {
	client *queue.Client
}

func newRPCQueueService(client *queue.Client) *rpcQueueService {
	return &rpcQueueService{client: client}
}

// ListQueues reports one summary per registered queue, in name order.
func (s *rpcQueueService) ListQueues(ctx context.Context, _ *connect.Request[systemv1.ListQueuesRequest]) (*connect.Response[systemv1.ListQueuesResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	views, err := s.client.Queues(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	queues := make([]*systemv1.QueueSummary, 0, len(views))
	for _, v := range views {
		summary := &systemv1.QueueSummary{
			Name:        v.Name,
			MaxAttempts: boundedInt32(v.MaxAttempts),
			TimeoutMs:   v.Timeout.Milliseconds(),
			Pending:     v.Pending,
			Dead:        v.Dead,
		}
		if v.Retention != nil {
			retentionSeconds := int64(v.Retention.Duration / time.Second)
			summary.RetentionSeconds = &retentionSeconds
			summary.RetentionOnlyFailed = v.Retention.OnlyFailed
		}
		queues = append(queues, summary)
	}
	return connect.NewResponse(&systemv1.ListQueuesResponse{Queues: queues}), nil
}

// ListTasks answers one page of the pending table, newest first.
func (s *rpcQueueService) ListTasks(ctx context.Context, req *connect.Request[systemv1.ListTasksRequest]) (*connect.Response[systemv1.ListTasksResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	views, total, err := s.client.Tasks(ctx, req.Msg.GetQueue(), int(req.Msg.GetOffset()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	tasks := make([]*systemv1.PendingTask, 0, len(views))
	for _, v := range views {
		task := &systemv1.PendingTask{
			Id:        v.ID.String(),
			Queue:     v.Queue,
			Attempts:  boundedInt32(v.Attempts),
			Priority:  boundedInt32(v.Priority),
			Status:    statusWordFor(statusOfClaim(v.Claimed)),
			CreatedAt: timestampOf(v.CreatedAt),
		}
		task.WaitUntil = timestampPtrOf(v.WaitUntil)
		task.ClaimedAt = timestampPtrOf(v.ClaimedAt)
		task.LastExecutedAt = timestampPtrOf(v.LastExecutedAt)
		tasks = append(tasks, task)
	}
	return connect.NewResponse(&systemv1.ListTasksResponse{Tasks: tasks, Total: total}), nil
}

// GetTask reports one task's state and, when the archive still holds it, its
// payload as the queue stored it.
func (s *rpcQueueService) GetTask(ctx context.Context, req *connect.Request[systemv1.GetTaskRequest]) (*connect.Response[systemv1.GetTaskResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("queue: task id is not a UUID"))
	}
	detail, exists, err := s.client.Detail(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !exists {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("queue: no task answers that id"))
	}

	response := &systemv1.GetTaskResponse{
		Id:     id.String(),
		Status: statusWordFor(detail.Status),
	}
	attempts := boundedInt32(detail.Attempts)
	response.Attempts = &attempts
	response.Payload = detail.Payload
	response.Error = detail.Error
	if detail.LastDuration > 0 {
		ms := milliseconds(detail.LastDuration)
		response.LastDurationMs = &ms
	}
	if !detail.CreatedAt.IsZero() {
		response.CreatedAt = timestampOf(detail.CreatedAt)
	}
	if detail.Queue != "" {
		response.Queue = &detail.Queue
	}
	if !detail.LastExecutedAt.IsZero() {
		response.LastExecutedAt = timestampOf(detail.LastExecutedAt)
	}
	return connect.NewResponse(response), nil
}

// ListDeadTasks answers one page of the archive's failures, oldest first.
func (s *rpcQueueService) ListDeadTasks(ctx context.Context, req *connect.Request[systemv1.ListDeadTasksRequest]) (*connect.Response[systemv1.ListDeadTasksResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	views, total, err := s.client.DeadTasks(ctx, req.Msg.GetQueue(), int(req.Msg.GetOffset()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	tasks := make([]*systemv1.DeadTask, 0, len(views))
	for _, v := range views {
		task := &systemv1.DeadTask{
			Id:             v.ID.String(),
			Queue:          v.Queue,
			Attempts:       boundedInt32(v.Attempts),
			LastExecutedAt: timestampOf(v.LastExecutedAt),
			CreatedAt:      timestampOf(v.CreatedAt),
		}
		task.Error = v.Error
		task.ExpiresAt = timestampPtrOf(v.ExpiresAt)
		tasks = append(tasks, task)
	}
	return connect.NewResponse(&systemv1.ListDeadTasksResponse{Tasks: tasks, Total: total}), nil
}

// CancelTask removes one unclaimed task. A claimed task is in flight — its
// worker may already be halfway through it — and the answer says so rather
// than pretending it was stopped.
func (s *rpcQueueService) CancelTask(ctx context.Context, req *connect.Request[systemv1.CancelTaskRequest]) (*connect.Response[systemv1.CancelTaskResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("queue: task id is not a UUID"))
	}
	cancelled, err := s.client.Cancel(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !cancelled {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("queue: the task is claimed and can no longer be cancelled"))
	}
	return connect.NewResponse(&systemv1.CancelTaskResponse{}), nil
}

// ReplayDeadTasks re-enqueues the dead tasks one queue — or every queue —
// keeps, each under a fresh identity with a fresh attempt budget.
func (s *rpcQueueService) ReplayDeadTasks(ctx context.Context, req *connect.Request[systemv1.ReplayDeadTasksRequest]) (*connect.Response[systemv1.ReplayDeadTasksResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	// The replay reaches through the engine's own all-queues pass: a
	// queue-narrowed replay filters the answer, the engine's replay is the
	// transaction the archive deletes under.
	replayed, err := s.client.ReplayDead(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&systemv1.ReplayDeadTasksResponse{Replayed: replayed}), nil
}

// FlushPendingTasks removes every unclaimed task.
func (s *rpcQueueService) FlushPendingTasks(ctx context.Context, _ *connect.Request[systemv1.FlushPendingTasksRequest]) (*connect.Response[systemv1.FlushPendingTasksResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	flushed, err := s.client.Flush(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&systemv1.FlushPendingTasksResponse{Flushed: flushed}), nil
}

// FlushCompletedTasks removes every archived record, retention
// notwithstanding.
func (s *rpcQueueService) FlushCompletedTasks(ctx context.Context, _ *connect.Request[systemv1.FlushCompletedTasksRequest]) (*connect.Response[systemv1.FlushCompletedTasksResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	flushed, err := s.client.FlushCompleted(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&systemv1.FlushCompletedTasksResponse{Flushed: flushed}), nil
}

// engine refuses a handler the composition built without its client: the
// registry builds the two together, so a working run cannot reach this —
// the same answer the health service gives a nil checker.
func (s *rpcQueueService) engine() error {
	if s.client == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("queue engine unavailable"))
	}
	return nil
}

// boundedInt32 narrows a count to the wire width without a silent overflow:
// attempts and priorities are small by every invariant the engine holds, and
// a count that somehow grew past the wire's width reports the width rather
// than wrapping negative.
func boundedInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

// rpcSchedulerService answers the scheduler's administrative procedures.
type rpcSchedulerService struct {
	scheduler *scheduler.Scheduler
}

func newRPCSchedulerService(s *scheduler.Scheduler) *rpcSchedulerService {
	return &rpcSchedulerService{scheduler: s}
}

// ListJobs reports one entry per registered job, in name order.
func (s *rpcSchedulerService) ListJobs(ctx context.Context, _ *connect.Request[systemv1.ListSchedulerJobsRequest]) (*connect.Response[systemv1.ListSchedulerJobsResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	views, err := s.scheduler.Jobs(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	jobs := make([]*systemv1.SchedulerJob, 0, len(views))
	for _, v := range views {
		job := &systemv1.SchedulerJob{
			Name:      v.Name,
			Spec:      v.Spec,
			NextDue:   timestampOf(v.NextDue),
			UpdatedAt: timestampOf(v.UpdatedAt),
		}
		job.LastFired = timestampPtrOf(v.LastFired)
		jobs = append(jobs, job)
	}
	return connect.NewResponse(&systemv1.ListSchedulerJobsResponse{Jobs: jobs}), nil
}

// RunNow enqueues a job's task immediately, without advancing the job's
// schedule.
func (s *rpcSchedulerService) RunNow(ctx context.Context, req *connect.Request[systemv1.RunSchedulerJobNowRequest]) (*connect.Response[systemv1.RunSchedulerJobNowResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	err := s.scheduler.RunNow(ctx, req.Msg.GetName())
	switch {
	case errors.Is(err, scheduler.ErrJobUnknown):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("scheduler: no job answers that name"))
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&systemv1.RunSchedulerJobNowResponse{}), nil
}

func (s *rpcSchedulerService) engine() error {
	if s.scheduler == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("scheduler unavailable"))
	}
	return nil
}
