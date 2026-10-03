package auditlog

import (
	"context"
	"errors"
	"fmt"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
)

// Repository reads the audit records. It writes nothing: a record is written
// by the feature that caused it, through internal/audit, so this type has no
// insert and no delete. Retention is a scheduled job's own query.
type Repository struct{}

// NewRepository builds the repository.
func NewRepository() *Repository {
	return &Repository{}
}

// Filter is the administrator's view's filter. An empty field is no filter,
// which is the state the first page of the list is in.
type Filter struct {
	// Event matches the event column exactly.
	Event string
	// UserID matches the account the action is about.
	UserID string
	// Search is a term matched against the account's username and email, so
	// an administrator can find activity by naming an account rather than by
	// looking its identifier up first.
	Search string
}

// listQuery is the select every list shares: the record's columns, plus the
// username of the account the action is about.
//
// The join is a LEFT join, and it is spelled with the option because
// `JoinWithOption` is the only door to it: `sb.Join` is an INNER join, which
// would drop every record whose account is gone — exactly the records the
// `ON DELETE SET NULL` column exists to keep. A record outlives its account,
// so it stays in the log with no username rather than disappearing with it.
func listQuery() *sqlbuilder.SelectBuilder {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(columns...)
	sb.From(entity.TableAuditLogs + " a")
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableUsers+" u", "u.id = a.user_id")
	return sb
}

// applyFilter narrows a list. It is applied to the page query and to the
// count query separately rather than sharing built conditions, because a
// builder's condition and its argument list are one thing: a condition built
// on one builder carries no argument on another.
func applyFilter(sb *sqlbuilder.SelectBuilder, filter Filter) {
	if filter.Event != "" {
		sb.Where(sb.Equal("a.event", filter.Event))
	}
	if filter.UserID != "" {
		sb.Where(sb.Equal("a.user_id", filter.UserID))
	}
	if filter.Search != "" {
		pattern := "%" + filter.Search + "%"
		sb.Where(sb.Or(
			sb.ILike("u.username", pattern),
			sb.ILike("u.email", pattern),
		))
	}
}

// auditSortColumns is the whitelist a list's sort key resolves through. The
// names are the wire values the list requests validate against.
var auditSortColumns = map[string]string{
	"event":      "a.event",
	"username":   "lower(u.username)",
	"ip_address": "a.ip_address",
	"created_at": "a.created_at",
}

// List answers one page of the records the filter admits, ordered as the
// caller asked (absent a choice, newest first), with the total the
// pagination metadata needs.
//
// A filter with no field set is every record, which is what makes this the
// one query behind all three listing procedures: `List` passes the caller's
// own identifier, `ListForUser` the account the administrator named, and
// `ListAll` whatever the administrator filtered by.
func (r *Repository) List(ctx context.Context, db datastore.Querier, filter Filter, sortBy string, ascending bool, offset, limit int) ([]Row, int, error) {
	sb := listQuery()
	applyFilter(sb, filter)
	// The identifier breaks a tie on the same instant, so a page boundary
	// falls in one place however many records share a timestamp.
	sb.OrderBy(datastore.ListOrder(auditSortColumns, sortBy, "created_at", ascending), "a.id")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("auditlog: list: %w", err)
	}
	defer rows.Close()

	records := []Row{}
	for rows.Next() {
		row, scanErr := scanRow(rows.Scan)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("auditlog: list: %w", scanErr)
		}
		records = append(records, row)
	}
	if rowErr := rows.Err(); rowErr != nil {
		return nil, 0, fmt.Errorf("auditlog: list: %w", rowErr)
	}

	total, err := r.count(ctx, db, filter)
	if err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

// count answers how many records the filter admits, which the pagination
// metadata needs.
func (r *Repository) count(ctx context.Context, db datastore.Querier, filter Filter) (int, error) {
	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableAuditLogs + " a")
	// The join is present only when the filter needs it, and it is a LEFT
	// join for the same reason the page's is: a count that dropped the
	// records with no account would describe a set the caller cannot page to.
	if filter.Search != "" {
		cb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableUsers+" u", "u.id = a.user_id")
	}
	applyFilter(cb, filter)

	query, args := cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("auditlog: count: %w", err)
	}
	return total, nil
}

// FindByID reads one record, which the procedures do not expose but a test
// and a future detail view need.
func (r *Repository) FindByID(ctx context.Context, db datastore.Querier, id string) (Row, error) {
	sb := listQuery()
	sb.Where(sb.Equal("a.id", id))

	query, args := sb.Build()
	row, err := scanRow(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return Row{}, datastore.ErrNoRows
	}
	if err != nil {
		return Row{}, fmt.Errorf("auditlog: find: %w", err)
	}
	return row, nil
}

// Events answers the distinct event names the table holds, sorted. It is what
// a filter control is built from, so the control offers the events that
// occur rather than a list the client keeps in step with the server.
func (r *Repository) Events(ctx context.Context, db datastore.Querier) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("DISTINCT event")
	sb.From(entity.TableAuditLogs)
	sb.OrderBy("event")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("auditlog: events: %w", err)
	}
	defer rows.Close()

	events := []string{}
	for rows.Next() {
		var event string
		if err := rows.Scan(&event); err != nil {
			return nil, fmt.Errorf("auditlog: events: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auditlog: events: %w", err)
	}
	return events, nil
}

// Users answers the accounts that appear in the table, so a filter control
// offers the accounts that have activity rather than every account.
//
// A record whose account was deleted has no identifier left to select, so it
// contributes nothing here; its records are still readable in the list, where
// the username column simply comes back empty.
func (r *Repository) Users(ctx context.Context, db datastore.Querier) ([]UserOption, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("DISTINCT u.id::text", "u.username")
	sb.From(entity.TableAuditLogs + " a")
	// An INNER join is right here, unlike the list: an option is a selectable
	// account, so a record with no account contributes none.
	sb.Join(entity.TableUsers+" u", "u.id = a.user_id")
	sb.OrderBy("u.username")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("auditlog: users: %w", err)
	}
	defer rows.Close()

	options := []UserOption{}
	for rows.Next() {
		var option UserOption
		if err := rows.Scan(&option.ID, &option.Username); err != nil {
			return nil, fmt.Errorf("auditlog: users: %w", err)
		}
		options = append(options, option)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auditlog: users: %w", err)
	}
	return options, nil
}

// scanRow reads one row. The nullable columns arrive already cast to their
// empty form by the select list, so the only column that needs decoding later
// is the payload.
func scanRow(scan func(dest ...any) error) (Row, error) {
	var row Row
	err := scan(
		&row.ID, &row.CreatedAt, &row.Event, &row.TriggerType, &row.ActionStatus,
		&row.UserID, &row.Username, &row.IPAddress, &row.UserAgent, &row.Fingerprint,
		&row.Country, &row.City, &row.ResourceType, &row.ResourceID,
		&row.PayloadJSON,
	)
	if err != nil {
		return Row{}, err
	}
	return row, nil
}
