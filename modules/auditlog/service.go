package auditlog

import (
	"context"
	"encoding/json/v2"
	"log/slog"
	"time"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/modules/identity/user"

	"uuid"
)

// The page the list procedures answer when the request names none. They are
// the same numbers the account lists use, so a client pages every list the
// same way.
const (
	DefaultPage  = 1
	DefaultLimit = 20
)

// Service reads the audit trail. It writes nothing: a record is written by the
// feature that caused it, through internal/audit, in that feature's
// transaction.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	log  *slog.Logger
}

// NewService builds the service over the pool.
func NewService(pool *datastore.Postgres, repo *Repository, log *slog.Logger) *Service {
	return &Service{pool: pool, repo: repo, log: log}
}

// View is one record as the procedures answer it. It is the row with the
// payload decoded: the actor is lifted out of the payload into fields of its
// own, and the rest travels as it was stored.
type View struct {
	ID            string
	CreatedAt     time.Time
	Event         string
	TriggerType   string
	ActionStatus  string
	UserID        string
	Username      string
	ActorID       string
	ActorUsername string
	IPAddress     string
	UserAgent     string
	Fingerprint   string
	Country       string
	City          string
	ResourceType  string
	ResourceID    string
	Payload       map[string]string
}

// Scope names whose records are read. The three listing procedures differ
// only in what they put here, which is why they share one query.
type Scope struct {
	// UserID limits the list to the records whose subject is this account.
	// Empty reads every account.
	UserID string
	// Event matches the event column exactly.
	Event string
	// Search matches the account's username and email.
	Search string
}

// List answers one page of records in the scope, newest first, with the
// pagination metadata the response carries.
func (s *Service) List(ctx context.Context, scope Scope, sortBy string, ascending bool, page, limit int) ([]View, webutil.Pagination, error) {
	page, limit = normalizePage(page, limit)
	params := webutil.PaginationParams{Page: page, Limit: limit}

	// The filter's identifier travels in the wire form; the rows key on the
	// UUID the column stores. A wire form that cannot decode filters
	// nothing — the zero UUID no row carries — because widening a failed
	// decode to "no filter" would hand the caller every account's records.
	filtered := scope.UserID
	if filtered != "" {
		if wire, err := user.ParseID(filtered); err == nil {
			filtered = user.IDToUUID(wire).String()
		} else {
			filtered = uuid.Nil().String()
		}
	}

	rows, total, err := s.repo.List(ctx, s.pool, Filter{
		Event:  scope.Event,
		UserID: filtered,
		Search: scope.Search,
	}, sortBy, ascending, params.Offset(), limit)
	if err != nil {
		return nil, webutil.Pagination{}, err
	}

	views := make([]View, 0, len(rows))
	for _, row := range rows {
		views = append(views, s.view(ctx, row))
	}
	return views, webutil.NewPagination(params, total), nil
}

// Options answers the filter facets: the events the table holds and the
// accounts that appear in it.
func (s *Service) Options(ctx context.Context) ([]string, []UserOption, error) {
	events, err := s.repo.Events(ctx, s.pool)
	if err != nil {
		return nil, nil, err
	}
	users, err := s.repo.Users(ctx, s.pool)
	if err != nil {
		return nil, nil, err
	}
	return events, users, nil
}

// view turns a row into the response's shape.
//
// A payload that cannot be decoded is not an error: the record is still worth
// reading, and the columns carry most of it. The failure is logged, because a
// payload this application wrote and cannot read back is a defect to fix
// rather than a caller's problem to report.
func (s *Service) view(ctx context.Context, row Row) View {
	payload := map[string]string{}
	if row.PayloadJSON != "" {
		if err := json.Unmarshal([]byte(row.PayloadJSON), &payload); err != nil {
			s.log.ErrorContext(ctx, "auditlog: payload not decoded",
				"id", row.ID, "event", row.Event, "err", err)
			payload = map[string]string{}
		}
	}
	return View{
		ID:            row.ID,
		CreatedAt:     row.CreatedAt,
		Event:         row.Event,
		TriggerType:   row.TriggerType,
		ActionStatus:  row.ActionStatus,
		UserID:        row.UserID,
		Username:      row.Username,
		ActorID:       payload[fwaudit.PayloadActorID],
		ActorUsername: payload[fwaudit.PayloadActorUsername],
		IPAddress:     row.IPAddress,
		UserAgent:     row.UserAgent,
		Fingerprint:   row.Fingerprint,
		Country:       row.Country,
		City:          row.City,
		ResourceType:  row.ResourceType,
		ResourceID:    row.ResourceID,
		Payload:       payload,
	}
}

// normalizePage answers the window a request asks for. An unset or
// out-of-range value becomes the default rather than an error: the page is a
// display choice, and a client that omits it wants the first page.
func normalizePage(page, limit int) (int, int) {
	if page < 1 {
		page = DefaultPage
	}
	if limit < 1 {
		limit = DefaultLimit
	}
	return page, limit
}
