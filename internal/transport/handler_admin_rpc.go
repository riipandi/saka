package transport

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"connectrpc.com/connect"

	commonv1 "github.com/riipandi/tango/codegen/proto/go/tango/common/v1"
	systemv1 "github.com/riipandi/tango/codegen/proto/go/tango/system/v1"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/internal/scheduler"
	"github.com/riipandi/tango/pkg/responder"
)

// rpcQueueService answers the queue's administrative procedures over
// ConnectRPC. The engine owns every fact and every action; this layer maps
// the wire only — the same split the health service keeps, so the two
// surfaces of one fact cannot drift. Task ids leave as the `que_` TypeID
// their row's UUID encodes, the way an account's id leaves as `user_…`.
type rpcQueueService struct {
	client *queue.Client
}

func newRPCQueueService(client *queue.Client) *rpcQueueService {
	return &rpcQueueService{client: client}
}

// ListQueues reports one page of registered queues with the pagination
// block the list responses carry.
func (s *rpcQueueService) ListQueues(ctx context.Context, req *connect.Request[systemv1.ListQueuesRequest]) (*connect.Response[systemv1.ListQueuesResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	views, pagination, err := s.client.Queues(
		ctx, req.Msg.GetSearch(), req.Msg.GetSortBy(),
		sortAscending(req.Msg.GetSortOrder(), true),
		int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
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
	return connect.NewResponse(&systemv1.ListQueuesResponse{
		Queues:   queues,
		Metadata: listMetadata(pagination),
		Status:   "success",
		Message:  "the queues were listed",
	}), nil
}

// ListTasks answers one page of the pending table with the pagination block
// the list responses carry.
func (s *rpcQueueService) ListTasks(ctx context.Context, req *connect.Request[systemv1.ListTasksRequest]) (*connect.Response[systemv1.ListTasksResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	views, pagination, err := s.client.Tasks(
		ctx, req.Msg.GetQueue(), req.Msg.GetSortBy(),
		sortAscending(req.Msg.GetSortOrder(), false),
		int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	tasks := make([]*systemv1.PendingTask, 0, len(views))
	for _, v := range views {
		task := &systemv1.PendingTask{
			Id:        queue.FormatID(v.ID),
			Queue:     v.Queue,
			Attempts:  boundedInt32(v.Attempts),
			Priority:  boundedInt32(v.Priority),
			State:     statusWordFor(statusOfClaim(v.Claimed)),
			CreatedAt: timestampOf(v.CreatedAt),
		}
		task.WaitUntil = timestampPtrOf(v.WaitUntil)
		task.ClaimedAt = timestampPtrOf(v.ClaimedAt)
		task.LastExecutedAt = timestampPtrOf(v.LastExecutedAt)
		tasks = append(tasks, task)
	}
	return connect.NewResponse(&systemv1.ListTasksResponse{
		Tasks:    tasks,
		Metadata: listMetadata(pagination),
		Status:   "success",
		Message:  "the tasks were listed",
	}), nil
}

// GetTask reports one task's state and, when the archive still holds it, its
// payload as the queue stored it. A malformed id names nothing and answers
// the not-found a bad account id answers.
func (s *rpcQueueService) GetTask(ctx context.Context, req *connect.Request[systemv1.GetTaskRequest]) (*connect.Response[systemv1.GetTaskResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	id, err := queue.UUIDFromWire(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("queue: no task answers that id"))
	}
	detail, exists, err := s.client.Detail(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !exists {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("queue: no task answers that id"))
	}

	response := &systemv1.GetTaskResponse{
		Id:      queue.FormatID(id),
		State:   statusWordFor(detail.Status),
		Status:  "success",
		Message: "the task was fetched",
		Payload: detail.Payload,
		Error:   detail.Error,
	}
	attempts := boundedInt32(detail.Attempts)
	response.Attempts = &attempts
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

// ListDeadTasks answers one page of the archive's failures with the
// pagination block the list responses carry.
func (s *rpcQueueService) ListDeadTasks(ctx context.Context, req *connect.Request[systemv1.ListDeadTasksRequest]) (*connect.Response[systemv1.ListDeadTasksResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	views, pagination, err := s.client.DeadTasks(
		ctx, req.Msg.GetQueue(), req.Msg.GetSortBy(),
		sortAscending(req.Msg.GetSortOrder(), true),
		int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	tasks := make([]*systemv1.DeadTask, 0, len(views))
	for _, v := range views {
		task := &systemv1.DeadTask{
			Id:             queue.FormatID(v.ID),
			Queue:          v.Queue,
			Attempts:       boundedInt32(v.Attempts),
			LastExecutedAt: timestampOf(v.LastExecutedAt),
			CreatedAt:      timestampOf(v.CreatedAt),
		}
		task.Error = v.Error
		task.ExpiresAt = timestampPtrOf(v.ExpiresAt)
		tasks = append(tasks, task)
	}
	return connect.NewResponse(&systemv1.ListDeadTasksResponse{
		Tasks:    tasks,
		Metadata: listMetadata(pagination),
		Status:   "success",
		Message:  "the dead tasks were listed",
	}), nil
}

// CancelTask removes one unclaimed task. A claimed task is in flight — its
// worker may already be halfway through it — and the answer says so rather
// than pretending it was stopped. A malformed id answers not_found, the way
// a malformed account id does.
func (s *rpcQueueService) CancelTask(ctx context.Context, req *connect.Request[systemv1.CancelTaskRequest]) (*connect.Response[systemv1.CancelTaskResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	id, err := queue.UUIDFromWire(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("queue: no task answers that id"))
	}
	cancelled, err := s.client.Cancel(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !cancelled {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("queue: the task is claimed and can no longer be cancelled"))
	}
	return connect.NewResponse(&systemv1.CancelTaskResponse{
		Status:  "success",
		Message: "the task was cancelled",
	}), nil
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
	return connect.NewResponse(&systemv1.ReplayDeadTasksResponse{
		Replayed: replayed,
		Status:   "success",
		Message:  fmt.Sprintf("replayed %d dead %s", replayed, pluralNoun(replayed, "task")),
	}), nil
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
	return connect.NewResponse(&systemv1.FlushPendingTasksResponse{
		Flushed: flushed,
		Status:  "success",
		Message: fmt.Sprintf("flushed %d waiting %s", flushed, pluralNoun(flushed, "task")),
	}), nil
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
	return connect.NewResponse(&systemv1.FlushCompletedTasksResponse{
		Flushed: flushed,
		Status:  "success",
		Message: fmt.Sprintf("flushed %d completed %s", flushed, pluralNoun(flushed, "record")),
	}), nil
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

// rpcSchedulerService answers the scheduler's administrative procedures.
// Job ids leave as the `scd_` TypeID their state row's UUID encodes.
type rpcSchedulerService struct {
	scheduler *scheduler.Scheduler
}

func newRPCSchedulerService(s *scheduler.Scheduler) *rpcSchedulerService {
	return &rpcSchedulerService{scheduler: s}
}

// ListJobs reports one page of registered jobs with the pagination block
// the list responses carry.
func (s *rpcSchedulerService) ListJobs(ctx context.Context, req *connect.Request[systemv1.ListSchedulerJobsRequest]) (*connect.Response[systemv1.ListSchedulerJobsResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	views, pagination, err := s.scheduler.Jobs(
		ctx, req.Msg.GetSearch(), req.Msg.GetSortBy(),
		sortAscending(req.Msg.GetSortOrder(), true),
		int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	jobs := make([]*systemv1.SchedulerJob, 0, len(views))
	for _, v := range views {
		job := &systemv1.SchedulerJob{
			Id:        scheduler.FormatID(v.ID),
			Name:      v.Name,
			Spec:      v.Spec,
			NextDue:   timestampOf(v.NextDue),
			UpdatedAt: timestampOf(v.UpdatedAt),
		}
		job.LastFired = timestampPtrOf(v.LastFired)
		jobs = append(jobs, job)
	}
	return connect.NewResponse(&systemv1.ListSchedulerJobsResponse{
		Jobs:     jobs,
		Metadata: listMetadata(pagination),
		Status:   "success",
		Message:  "the scheduler jobs were listed",
	}), nil
}

// RunNow enqueues a job's task immediately, without advancing the job's
// schedule. A malformed or unregistered id answers not_found.
func (s *rpcSchedulerService) RunNow(ctx context.Context, req *connect.Request[systemv1.RunSchedulerJobNowRequest]) (*connect.Response[systemv1.RunSchedulerJobNowResponse], error) {
	if err := s.engine(); err != nil {
		return nil, err
	}
	err := s.scheduler.RunNow(ctx, req.Msg.GetId())
	switch {
	case errors.Is(err, scheduler.ErrJobUnknown):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("scheduler: no job answers that id"))
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&systemv1.RunSchedulerJobNowResponse{
		Status:  "success",
		Message: "the job's task was enqueued now",
	}), nil
}

func (s *rpcSchedulerService) engine() error {
	if s.scheduler == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("scheduler unavailable"))
	}
	return nil
}

// pluralNoun picks the singular or plural ending for a counted message:
// the count rides in the sentence, and one is not many.
func pluralNoun(n int64, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// sortAscending resolves the wire's sort_order against each list's own
// default: the caller's choice wins, an absent one answers the direction
// the procedure documents.
func sortAscending(order string, absentAsc bool) bool {
	switch order {
	case "asc":
		return true
	case "desc":
		return false
	default:
		return absentAsc
	}
}

// listMetadata maps the responder's pagination onto the shared block. The
// wire fields are optional, so an unknown range is absent rather than zero.
func listMetadata(p responder.Pagination) *commonv1.ListMetadata {
	meta := &commonv1.ListMetadata{}
	set := func(dst **int32, src *int) {
		if src == nil {
			return
		}
		// The wire field is int32; a total beyond it saturates rather than
		// wrapping, and no page the rules allow can reach the bound.
		value := *src
		if value > math.MaxInt32 || value < math.MinInt32 {
			value = math.MaxInt32
		}
		*dst = new(int32(value))
	}
	set(&meta.Page, p.Page)
	set(&meta.Limit, p.Limit)
	set(&meta.TotalPages, p.TotalPages)
	set(&meta.TotalItems, p.TotalItems)
	set(&meta.FirstItemIndex, p.FirstItemIndex)
	set(&meta.LastItemIndex, p.LastItemIndex)
	return meta
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
