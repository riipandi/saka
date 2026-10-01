// The inspection surface: the read-and-trigger API an operations screen
// answers through. The engine's own claim logic never runs here — these
// methods read the state table the fires maintain, and one of them enqueues
// a job's task on demand without touching the schedule the claim advances.

package scheduler

import (
	"context"
	"errors"
	"time"

	"github.com/huandu/go-sqlbuilder"
)

// errJobUnknown reports a RunNow for a name the process never registered. It
// is a caller's mistake, not a scheduler fault.
var ErrJobUnknown = errors.New("scheduler: job is not registered")

// JobView is one registered job's durable state.
type JobView struct {
	// Name is the job's registered name.
	Name string
	// Spec is the cron expression the job fires on.
	Spec string
	// NextDue is when the claim cursor says the job fires next.
	NextDue time.Time
	// LastFired is when the job last won a claim, nil when it never fired.
	LastFired *time.Time
	// UpdatedAt is when the state row last moved.
	UpdatedAt time.Time
}

// Jobs answers every registered job's state row, in name order. A row the
// seeding has not written yet — a scheduler built but never started — is
// absent from the answer.
func (s *Scheduler) Jobs(ctx context.Context) ([]JobView, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("name", "spec", "next_due", "last_fired", "updated_at")
	sb.From(jobsTable)
	sb.OrderBy("name ASC")

	query, args := sb.Build()
	rows, err := s.store.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]JobView, 0, len(s.jobs))
	for rows.Next() {
		var v JobView
		if err := rows.Scan(&v.Name, &v.Spec, &v.NextDue, &v.LastFired, &v.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, v)
	}
	return jobs, rows.Err()
}

// RunNow enqueues a job's task immediately, without advancing the job's
// schedule: the next tick fires on time, and this enqueue runs beside the
// schedule rather than instead of it. An unknown name answers
// errJobUnknown.
func (s *Scheduler) RunNow(ctx context.Context, name string) error {
	for i := range s.jobs {
		job := &s.jobs[i]
		if job.Name != name {
			continue
		}
		op := s.client.Add(job.Task)
		if job.Priority != 0 {
			op = op.Priority(job.Priority)
		}
		if _, err := op.Save(); err != nil {
			return err
		}
		return nil
	}
	return ErrJobUnknown
}
