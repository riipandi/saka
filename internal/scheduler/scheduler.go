// Package scheduler is saka's durable cron scheduler. robfig/cron owns the
// timing; Postgres owns the claim: every replica fires the cron time, and
// the one that moves the job's next_due forward — one row locked FOR UPDATE
// inside the same transaction as the enqueue — wins the tick. A process that
// was down does not know it missed anything: the row is simply still due.
//
// The scheduler never runs business logic. A claimed tick enqueues a task
// onto the queue, which owns execution, retries, backoff, and the archive;
// the tick's claim and the task's insert are one transaction, so a tick is
// never lost between the two and never enqueued twice.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/robfig/cron/v3"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/queue"
)

// jobsTable is the scheduler's own state table, created by
// database/migrations/00008_create_scheduler_tables.sql. The engine reads
// and writes it, and nothing else in the process names it.
const jobsTable = "public.scheduler_jobs"

// now returns the current time in a way tests can override.
var now = func() time.Time { return time.Now() }

// Construction errors, so a config that cannot run is told apart from a
// schedule that does not parse.
var (
	errMissingStore  = errors.New("scheduler: missing store")
	errMissingClient = errors.New("scheduler: missing queue client")
)

type (
	// Job is one scheduled enqueueing: a cron spec, and the task a claimed
	// tick puts on the queue. The name is the state row's key and must be
	// unique.
	Job struct {
		Name string
		Spec string
		Task queue.Task
		// Priority is handed to the enqueued task: a higher number is
		// claimed by the dispatcher first. Zero is the default.
		Priority int
	}

	// Config is the scheduler's construction options.
	Config struct {
		// Store is the shared Postgres pool the state rows live in.
		Store queue.Store
		// Client is the queue client claimed ticks enqueue onto.
		Client *queue.Client
		// Logger is the process logger. Nil discards every line.
		Logger *slog.Logger
		// Location resolves a spec that names no zone of its own. Nil is UTC.
		Location *time.Location
		// Jobs are the scheduled enqueues this scheduler runs.
		Jobs []Job
	}

	// Scheduler fires the registered jobs on robfig/cron and claims every
	// tick in Postgres, so exactly one replica enqueues it. It is built once
	// per process and lives and dies with the serve command.
	Scheduler struct {
		store    queue.Store
		client   *queue.Client
		log      *slog.Logger
		metrics  *schedulerMetrics
		location *time.Location
		jobs     []registeredJob
		cron     *cron.Cron
		running  atomic.Bool
	}

	// registeredJob pairs a job with the schedule parsed from its spec, so a
	// spec is parsed once and the next due time is computed from the same
	// object the run loop fires on.
	registeredJob struct {
		Job
		schedule cron.Schedule
	}
)

// New initializes a Scheduler. A spec that does not parse, a name that is
// missing or registered twice, and a task that is nil are wiring bugs, and
// the construction refuses them rather than letting a job silently never run.
func New(cfg Config) (*Scheduler, error) {
	switch {
	case cfg.Store == nil:
		return nil, errMissingStore
	case cfg.Client == nil:
		return nil, errMissingClient
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	location := cfg.Location
	if location == nil {
		location = time.UTC
	}

	seen := make(map[string]struct{}, len(cfg.Jobs))
	jobs := make([]registeredJob, 0, len(cfg.Jobs))
	for _, job := range cfg.Jobs {
		switch {
		case job.Name == "":
			return nil, fmt.Errorf("scheduler: job name is missing")
		case job.Task == nil:
			return nil, fmt.Errorf("scheduler: job %s has no task", job.Name)
		}
		if _, exists := seen[job.Name]; exists {
			return nil, fmt.Errorf("scheduler: job %s is registered twice", job.Name)
		}
		schedule, err := parseSchedule(job.Spec, location)
		if err != nil {
			return nil, fmt.Errorf("scheduler: job %s: %w", job.Name, err)
		}
		seen[job.Name] = struct{}{}
		jobs = append(jobs, registeredJob{Job: job, schedule: schedule})
	}

	metrics := newSchedulerMetrics()
	return &Scheduler{
		metrics:  metrics,
		store:    cfg.Store,
		client:   cfg.Client,
		log:      cfg.Logger,
		location: location,
		jobs:     jobs,
	}, nil
}

// parseSchedule parses a spec in the given location. The TZ prefix is how
// robfig lets a spec name its zone — WithLocation only sets the run loop's
// clock, not the parser's — so the zone is prepended here and the spec is
// parsed exactly once, in the same form the engine keeps.
func parseSchedule(spec string, location *time.Location) (cron.Schedule, error) {
	if spec == "" {
		return nil, errors.New("spec is missing")
	}
	return cron.ParseStandard("TZ=" + location.String() + " " + spec)
}

// Start seeds the jobs' state rows and runs the cron loop. To stop it
// gracefully, call Stop; the context cancels the fires, not the loop.
func (s *Scheduler) Start(ctx context.Context) {
	if s.running.Load() {
		return
	}
	s.running.Store(true)

	for i := range s.jobs {
		if err := s.seed(ctx, &s.jobs[i]); err != nil {
			s.log.ErrorContext(ctx, "scheduler: failed to seed job",
				"job", s.jobs[i].Name, "err", err.Error())
		}
	}

	s.cron = cron.New(cron.WithLocation(s.location))
	for i := range s.jobs {
		job := &s.jobs[i]
		s.cron.Schedule(job.schedule, cron.FuncJob(func() { s.fire(ctx, job) }))
	}
	s.cron.Start()
	s.log.InfoContext(ctx, "scheduler started", "jobs", len(s.jobs))
}

// Stop stops the cron loop and waits until the fires in flight finish —
// robfig's stop context closes when they do. True is returned when every
// fire finished in time.
func (s *Scheduler) Stop(ctx context.Context) bool {
	if !s.running.Load() {
		return true
	}
	stopCtx := s.cron.Stop()

	stopped := false
	select {
	case <-stopCtx.Done():
		stopped = true
	case <-ctx.Done():
	}
	s.running.Store(false)
	return stopped
}

// seed writes the job's state row when it does not exist yet, with the due
// time at the schedule's next firing. A row a previous deploy created keeps
// its due time: the schedule is already in motion.
func (s *Scheduler) seed(ctx context.Context, job *registeredJob) error {
	due := job.schedule.Next(now().In(s.location))

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(jobsTable)
	ib.Cols("name", "spec", "next_due")
	ib.Values(job.Name, job.Spec, due)
	ib.SQL("ON CONFLICT (name) DO NOTHING")

	query, args := ib.Build()
	_, err := s.store.Exec(ctx, query, args...)
	return err
}

// fire runs one cron firing: the tick is claimed and the task enqueued in
// one transaction, and only a committed claim wakes the dispatcher. A failed
// tick leaves next_due untouched, so the next firing retries it.
func (s *Scheduler) fire(ctx context.Context, job *registeredJob) {
	started := now()
	outcome, err := s.claimOutcome(ctx, job)
	if err != nil {
		s.log.ErrorContext(ctx, "scheduler: tick failed", "job", job.Name, "err", err.Error())
		outcome = tickError
	}
	s.metrics.recordTick(ctx, job.Name, outcome, time.Since(started))
	if err == nil && outcome == tickClaimed {
		// The transaction is committed, so the task is visible and the
		// dispatcher can claim it.
		s.client.Notify()
	}
}

// claimOutcome runs the claim transaction and reports what the tick became: a
// claimed tick advanced next_due and enqueued its task, a skipped one was
// claimed by another replica first, and a reseeded one rebuilt its state row.
// The outcome rides out of the transaction, because the counters record after
// it commits — a counter written inside would be rolled back with the claim.
func (s *Scheduler) claimOutcome(ctx context.Context, job *registeredJob) (string, error) {
	outcome := tickClaimed
	err := s.store.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		var claimErr error
		outcome, claimErr = s.claim(ctx, tx, job)
		return claimErr
	})
	return outcome, err
}

// claim decides the tick inside the caller's transaction. The row is locked
// FOR UPDATE and the clock it is read against is the database's own: two
// replicas that fire at the same moment line up on the lock, and the first
// commit moves next_due forward, which is what makes every later fire a
// no-op.
func (s *Scheduler) claim(ctx context.Context, tx datastore.Querier, job *registeredJob) (string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("next_due", "now()")
	sb.From(jobsTable)
	sb.Where(sb.Equal("name", job.Name))
	sb.ForUpdate()

	query, args := sb.Build()
	var due, dbNow time.Time
	err := tx.QueryRow(ctx, query, args...).Scan(&due, &dbNow)
	if errors.Is(err, datastore.ErrNoRows) {
		// The row was removed out from under the schedule — a manual delete,
		// a restored dump. It is re-seeded rather than fire-never-again.
		s.log.WarnContext(ctx, "scheduler: state row missing, re-seeding", "job", job.Name)
		return tickReseeded, s.reseed(ctx, tx, job)
	}
	if err != nil {
		return tickError, err
	}
	if due.After(dbNow) {
		// Another replica's fire already claimed this tick.
		return tickSkipped, nil
	}

	// The next due time advances from the old one, so the schedule keeps its
	// marks: a cron spec that was down for hours resumes at its next cron
	// time, and an interval spec catches up one slot at a time.
	next := job.schedule.Next(due)

	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(jobsTable)
	ub.Set(
		ub.Assign("next_due", next),
		ub.Assign("last_fired", dbNow),
		ub.Assign("updated_at", dbNow),
	)
	ub.Where(ub.Equal("name", job.Name))

	query, args = ub.Build()
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return tickError, err
	}

	if _, err := s.client.Add(job.Task).Ctx(ctx).Priority(job.Priority).Executor(tx).Save(); err != nil {
		return tickError, fmt.Errorf("enqueue task: %w", err)
	}

	s.log.InfoContext(ctx, "scheduler: tick claimed",
		"job", job.Name, "next", next.Format(time.RFC3339))
	return tickClaimed, nil
}

// reseed writes the job's state row through the caller's transaction, with
// the due time at the schedule's next firing; the tick that found nothing is
// not fired.
func (s *Scheduler) reseed(ctx context.Context, tx datastore.Querier, job *registeredJob) error {
	due := job.schedule.Next(now().In(s.location))

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(jobsTable)
	ib.Cols("name", "spec", "next_due")
	ib.Values(job.Name, job.Spec, due)
	ib.SQL("ON CONFLICT (name) DO NOTHING")

	query, args := ib.Build()
	_, err := tx.Exec(ctx, query, args...)
	if err == nil {
		s.metrics.recordReseed(ctx, job.Name)
	}
	return err
}
