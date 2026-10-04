// The inspection surface: the read-and-trigger API an operations screen
// answers through. The engine's own claim logic never runs here — these
// methods read the state table the fires maintain, and one of them enqueues
// a job's task on demand without touching the schedule the claim advances.

package scheduler

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/internal/database/entity"
)

// ErrJobUnknown reports a RunNow for an id no registered job answers. It is
// a caller's mistake, not a scheduler fault — a malformed identifier and an
// absent row are both the not-found the wire refuses as.
var ErrJobUnknown = errors.New("scheduler: job is not registered")

// JobView is one registered job's durable state.
type JobView struct {
	// ID is the state row's UUID; FormatID renders it for the wire.
	ID uuid.UUID
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

// jobSortColumns is the whitelist a job page's sort key resolves through;
// the map is the schema of the ORDER BY, one entry per column the wire may
// name.
var jobSortColumns = map[string]string{
	"name":       "name",
	"next_due":   "next_due",
	"updated_at": "updated_at",
}

// Jobs answers one page of the state rows — the search admits, the named
// key orders, and the pagination block counts. A row the seeding has not
// written yet — a scheduler built but never started — is absent from the
// answer.
func (s *Scheduler) Jobs(ctx context.Context, search, sortBy string, ascending bool, page, limit int) ([]JobView, webutil.Pagination, error) {
	page, limit = webutil.NormalizePage(page, limit, webutil.DefaultPageSize, webutil.MaxPageSize)

	condition := func(sb *sqlbuilder.SelectBuilder) {
		if search != "" {
			pattern := "%" + search + "%"
			sb.Where(sb.Or(sb.ILike("name", pattern), sb.ILike("spec", pattern)))
		}
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "name", "spec", "next_due", "last_fired", "updated_at")
	sb.From(entity.TableSchedulerJobs)
	condition(sb)
	sb.OrderBy(datastore.ListOrder(jobSortColumns, sortBy, "name", ascending))
	sb.Limit(limit).Offset(webutil.Offset(page, limit))

	query, args := sb.Build()
	rows, err := s.store.Query(ctx, query, args...)
	if err != nil {
		return nil, webutil.Pagination{}, err
	}
	defer rows.Close()

	jobs := make([]JobView, 0, limit)
	for rows.Next() {
		var v JobView
		if err := rows.Scan(&v.ID, &v.Name, &v.Spec, &v.NextDue, &v.LastFired, &v.UpdatedAt); err != nil {
			return nil, webutil.Pagination{}, err
		}
		jobs = append(jobs, v)
	}
	if err := rows.Err(); err != nil {
		return nil, webutil.Pagination{}, err
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableSchedulerJobs)
	condition(cb)
	query, args = cb.Build()
	var total int64
	if err := s.store.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, webutil.Pagination{}, err
	}

	return jobs, webutil.NewPagination(webutil.PaginationParams{Page: page, Limit: limit}, int(total)), nil
}

// RunNow enqueues a job's task immediately, without advancing the job's
// schedule: the next tick fires on time, and this enqueue runs beside the
// schedule rather than instead of it. The wire id resolves through the
// state row to the registered name, so a stale row an unregistered job left
// behind answers ErrJobUnknown as well.
func (s *Scheduler) RunNow(ctx context.Context, wire string) error {
	raw, err := UUIDFromWire(wire)
	if err != nil {
		return ErrJobUnknown
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("name")
	sb.From(entity.TableSchedulerJobs)
	sb.Where(sb.Equal("id", raw))

	query, args := sb.Build()
	var name string
	err = s.store.QueryRow(ctx, query, args...).Scan(&name)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrJobUnknown
	}
	if err != nil {
		return err
	}

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
