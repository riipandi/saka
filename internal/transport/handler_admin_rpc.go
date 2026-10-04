package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"connectrpc.com/connect"

	commonv1 "github.com/riipandi/saka/codegen/proto/go/saka/common/v1"
	systemv1 "github.com/riipandi/saka/codegen/proto/go/saka/system/v1"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/internal/scheduler"
)

// errInternal is the one wire answer an internal failure carries. The cause
// travels to the process log instead: a driver error's text — SQL fragments,
// constraint names, cipher failures — is not a caller's to read.
var errInternal = errors.New("internal error")

// The resource types the queue and scheduler records name, the way a module
// names its own.
const (
	resourceQueueTask    = "queue_task"
	resourceSchedulerJob = "scheduler_job"
)

// rpcQueueService answers the queue's administrative procedures over
// ConnectRPC. The engine owns every fact and every action; this layer maps
// the wire only — the same split the health service keeps, so the two
// surfaces of one fact cannot drift. Task ids leave as the `que_` TypeID
// their row's UUID encodes, the way an account's id leaves as `user_…`.
type rpcQueueService struct {
	client *queue.Client
	// db and audit write the record a destructive action leaves behind. The
	// action is a single statement, so the record rides the pool rather than
	// a transaction the handler does not own.
	db    datastore.Querier
	audit *audit.Recorder
	log   *slog.Logger
}

func newRPCQueueService(client *queue.Client, db datastore.Querier, audit *audit.Recorder, log *slog.Logger) *rpcQueueService {
	return &rpcQueueService{client: client, db: db, audit: audit, log: log}
}

// internalFailure logs the cause and answers the wire the one static message,
// so a driver error's text never reaches a caller.
func (s *rpcQueueService) internalFailure(ctx context.Context, what string, err error) error {
	s.log.ErrorContext(ctx, "queue: "+what, "err", err.Error())
	return connect.NewError(connect.CodeInternal, errInternal)
}

// record writes the audit entry one completed action leaves behind. The
// surface may be absent — a bare test router — and recording never is a
// condition of the action.
func (s *rpcQueueService) record(ctx context.Context, event, resourceID string, payload map[string]string) {
	s.audit.Record(ctx, s.db, audit.Entry{
		Event:        event,
		Trigger:      audit.TriggerUser,
		Status:       audit.StatusSuccess,
		ResourceType: resourceQueueTask,
		ResourceID:   resourceID,
		Payload:      payload,
	})
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
		return nil, s.internalFailure(ctx, "failed to list queues", err)
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
		return nil, s.internalFailure(ctx, "failed to list tasks", err)
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
		return nil, s.internalFailure(ctx, "failed to fetch task", err)
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
		return nil, s.internalFailure(ctx, "failed to list dead tasks", err)
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
		return nil, s.internalFailure(ctx, "failed to cancel task", err)
	}
	if !cancelled {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("queue: the task is claimed and can no longer be cancelled"))
	}
	// The record names the task the way the log's readers do — the row's own
	// identifier, the UUID the wire form encoded, because resource_id is a
	// UUID column.
	s.record(ctx, audit.EventQueueTaskCancelled, id.String(), nil)
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
	// The queue name narrows the replay the contract documents: absent, every
	// queue's dead pile returns; present, only the named queue's. The engine
	// owns the filter, so the response count and the rows that moved agree.
	replayed, err := s.client.ReplayDead(ctx, req.Msg.GetQueue())
	if err != nil {
		return nil, s.internalFailure(ctx, "failed to replay dead tasks", err)
	}
	payload := map[string]string{"replayed": strconv.FormatInt(replayed, 10)}
	if queue := req.Msg.GetQueue(); queue != "" {
		payload["queue"] = queue
	}
	s.record(ctx, audit.EventQueueDeadReplayed, "", payload)
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
		return nil, s.internalFailure(ctx, "failed to flush pending tasks", err)
	}
	s.record(ctx, audit.EventQueuePendingFlushed, "", map[string]string{
		"flushed": strconv.FormatInt(flushed, 10),
	})
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
		return nil, s.internalFailure(ctx, "failed to flush completed tasks", err)
	}
	s.record(ctx, audit.EventQueueCompletedFlushed, "", map[string]string{
		"flushed": strconv.FormatInt(flushed, 10),
	})
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
	db        datastore.Querier
	audit     *audit.Recorder
	log       *slog.Logger
}

func newRPCSchedulerService(s *scheduler.Scheduler, db datastore.Querier, audit *audit.Recorder, log *slog.Logger) *rpcSchedulerService {
	return &rpcSchedulerService{scheduler: s, db: db, audit: audit, log: log}
}

// internalFailure is the queue service's answer shared by both engines: the
// cause travels to the log, the wire carries the one static message.
func (s *rpcSchedulerService) internalFailure(ctx context.Context, what string, err error) error {
	s.log.ErrorContext(ctx, "scheduler: "+what, "err", err.Error())
	return connect.NewError(connect.CodeInternal, errInternal)
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
		return nil, s.internalFailure(ctx, "failed to list jobs", err)
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
		return nil, s.internalFailure(ctx, "failed to run job now", err)
	}
	// The record names the job by the state row's own identifier: the wire
	// id decoded back to the UUID the column holds.
	if raw, decodeErr := scheduler.UUIDFromWire(req.Msg.GetId()); decodeErr == nil {
		s.audit.Record(ctx, s.db, audit.Entry{
			Event:        audit.EventSchedulerJobRunNow,
			Trigger:      audit.TriggerUser,
			Status:       audit.StatusSuccess,
			ResourceType: resourceSchedulerJob,
			ResourceID:   raw.String(),
		})
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
func listMetadata(p webutil.Pagination) *commonv1.ListMetadata {
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
