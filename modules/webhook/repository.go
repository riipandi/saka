package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"uuid"

	"github.com/riipandi/tango/internal/datastore"
)

// Repository reads and writes the endpoint, delivery, and attempt rows.
// Every method takes the query surface, so the service passes either the
// pool, the transaction a change runs in, or the transaction an audit record
// was written in — the emission path rides the causing transaction.
type Repository struct{}

// NewRepository builds the repository.
func NewRepository() *Repository {
	return &Repository{}
}

// The endpoint columns, in scan order.
var endpointColumns = []string{
	"id", "name", "description", "endpoint", "method", "headers",
	"enabled", "secret_enc", "event_types", "created_at", "updated_at",
}

// endpointScanDests is the scan destinations of one endpoint row, in column
// order.
func endpointScanDests(row *EndpointSchema) []any {
	return []any{
		&row.ID, &row.Name, &row.Descr, &row.Endpoint, &row.Method, &row.Headers,
		&row.Enabled, &row.SecretEnc, &row.EventTypes, &row.CreatedAt, &row.UpdatedAt,
	}
}

// scanEndpoint reads one endpoint row.
func scanEndpoint(scan func(dest ...any) error) (EndpointSchema, error) {
	var row EndpointSchema
	if err := scan(endpointScanDests(&row)...); err != nil {
		return EndpointSchema{}, err
	}
	return row, nil
}

// CreateEndpoint stores the endpoint row and answers its identifier. The
// unique name index is the storage of the name rule, and the service reads
// the write's failure to answer a duplicate.
func (r *Repository) CreateEndpoint(ctx context.Context, db datastore.Querier, row EndpointSchema) (uuid.UUID, error) {
	row.ID = uuid.NewV7()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(EndpointTable)
	ib.Cols("id", "name", "description", "endpoint", "method", "headers", "enabled", "secret_enc", "event_types")
	if row.EventTypes == nil {
		// An empty subscription list is a real state — every event — and the
		// column is NOT NULL, so the nil the wire leaves is the empty list.
		row.EventTypes = []string{}
	}
	ib.Values(
		row.ID, row.Name, row.Descr, row.Endpoint, row.Method, row.Headers,
		row.Enabled, row.SecretEnc, row.EventTypes,
	)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return uuid.Nil(), err
	}
	return row.ID, nil
}

// GetEndpoint reads one endpoint by its identifier. An identifier that names
// no endpoint is the caller's not-found failure.
func (r *Repository) GetEndpoint(ctx context.Context, db datastore.Querier, id uuid.UUID) (EndpointSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(endpointColumns...)
	sb.From(EndpointTable)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanEndpoint(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return EndpointSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return EndpointSchema{}, fmt.Errorf("webhook: get endpoint: %w", err)
	}
	return row, nil
}

// ListEndpoints answers one page of the endpoints, newest first. The enabled
// filter narrows the page to one state; the event filter narrows it to the
// endpoints that would receive that event — an exact entry, the wildcard, or
// an empty subscription list.
func (r *Repository) ListEndpoints(ctx context.Context, db datastore.Querier, enabled *bool, event string, offset, limit int) ([]EndpointSchema, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(endpointColumns...)
	sb.From(EndpointTable)
	sb.Where(endpointFilters(sb, enabled, event))
	sb.OrderBy("created_at DESC", "id DESC")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("webhook: list endpoints: %w", err)
	}
	defer rows.Close()

	endpoints := []EndpointSchema{}
	for rows.Next() {
		row, err := scanEndpoint(rows.Scan)
		if err != nil {
			return nil, 0, fmt.Errorf("webhook: list endpoints: %w", err)
		}
		endpoints = append(endpoints, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("webhook: list endpoints: %w", err)
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(EndpointTable)
	cb.Where(endpointFilters(cb, enabled, event))
	query, args = cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("webhook: count endpoints: %w", err)
	}
	return endpoints, total, nil
}

// endpointFilters is the WHERE clause a list runs: the enabled state the
// caller asked for, and the subscription test — the wildcard, an empty
// subscription list, or an exact entry — when an event is named. The array
// predicates have no builder shape, so they ride as raw fragments whose one
// value travels through the builder's placeholder.
func endpointFilters(sb *sqlbuilder.SelectBuilder, enabled *bool, event string) string {
	conditions := []string{}
	if enabled != nil {
		conditions = append(conditions, sb.Equal("enabled", *enabled))
	}
	if event != "" {
		conditions = append(conditions, sb.Or(
			"cardinality(event_types) = 0",
			"'*' = ANY(event_types)",
			sb.Var(event)+" = ANY(event_types)",
		))
	}
	if len(conditions) == 0 {
		return "TRUE"
	}
	return sb.And(conditions...)
}

// UpdateEndpoint rewrites the fields the update names. The WHERE clause names
// the row alone; the service has already read it, and an endpoint deleted
// between the read and the write answers the read-back's not-found.
func (r *Repository) UpdateEndpoint(ctx context.Context, db datastore.Querier, id uuid.UUID, update EndpointUpdate, at time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(EndpointTable)
	assignments := []string{ub.Assign("updated_at", at)}
	if update.Descr != nil {
		assignments = append(assignments, ub.Assign("description", *update.Descr))
	}
	if update.Endpoint != nil {
		assignments = append(assignments, ub.Assign("endpoint", *update.Endpoint))
	}
	if update.Method != nil {
		assignments = append(assignments, ub.Assign("method", *update.Method))
	}
	if update.Headers != nil {
		assignments = append(assignments, ub.Assign("headers", *update.Headers))
	}
	if update.Enabled != nil {
		assignments = append(assignments, ub.Assign("enabled", *update.Enabled))
	}
	if update.EventTypes != nil {
		assignments = append(assignments, ub.Assign("event_types", *update.EventTypes))
	}
	ub.Set(assignments...)
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webhook: update endpoint: %w", err)
	}
	return nil
}

// EndpointUpdate carries the fields an update rewrites. A nil field keeps its
// stored value; a present one — even empty — replaces it. Headers travels as
// bytes because the column is JSONB and an empty set is a real state.
type EndpointUpdate struct {
	Descr      *string
	Endpoint   *string
	Method     *string
	Headers    *[]byte
	Enabled    *bool
	EventTypes *[]string
}

// DeleteEndpoint removes one endpoint. The deliveries survive: their foreign
// key is ON DELETE SET NULL, so the history outlives the destination.
func (r *Repository) DeleteEndpoint(ctx context.Context, db datastore.Querier, id uuid.UUID) (bool, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(EndpointTable)
	dbt.Where(dbt.Equal("id", id))

	query, args := dbt.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("webhook: delete endpoint: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RotateSecret replaces an endpoint's sealed signing secret. The stored
// ciphertext is overwritten, not kept — rotation is how a leaked secret
// stops working, and keeping the old half would keep the leak alive.
func (r *Repository) RotateSecret(ctx context.Context, db datastore.Querier, id uuid.UUID, sealed string, at time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(EndpointTable)
	ub.Set(ub.Assign("secret_enc", sealed), ub.Assign("updated_at", at))
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webhook: rotate secret: %w", err)
	}
	return nil
}

// MatchEndpointIDs answers the identifiers of the enabled endpoints
// subscribed to one event: an exact entry, the wildcard, or an empty
// subscription list. It runs inside the transaction that caused the event,
// so a rollback takes its deliveries with it. The identifiers are all the
// emission needs — the runner re-reads the endpoint it delivers to — so the
// sealed secrets never leave their rows for a fan-out that may deliver none.
func (r *Repository) MatchEndpointIDs(ctx context.Context, db datastore.Querier, event string) ([]uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(EndpointTable)
	sb.Where(sb.Equal("enabled", true), endpointSubscription(sb, event))

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("webhook: match endpoints: %w", err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("webhook: match endpoints: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// endpointSubscription is the subscription test one event runs against every
// endpoint: the wildcard, an empty list, or an exact entry. The array
// predicates have no builder shape, so they ride as raw fragments whose one
// value travels through the builder's placeholder.
func endpointSubscription(sb *sqlbuilder.SelectBuilder, event string) string {
	return sb.Or(
		"cardinality(event_types) = 0",
		"'*' = ANY(event_types)",
		sb.Var(event)+" = ANY(event_types)",
	)
}

// CreateDelivery stores one delivery row and answers its identifier. The
// body is written once, as the exact bytes every attempt will sign.
func (r *Repository) CreateDelivery(ctx context.Context, db datastore.Querier, row DeliverySchema) (uuid.UUID, error) {
	row.ID = uuid.NewV7()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(DeliveryTable)
	ib.Cols("id", "webhook_id", "event", "body", "status")
	ib.Values(row.ID, row.WebhookID, row.Event, row.Body, StatusPending)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return uuid.Nil(), fmt.Errorf("webhook: create delivery: %w", err)
	}
	return row.ID, nil
}

// GetDelivery reads one delivery by its identifier.
func (r *Repository) GetDelivery(ctx context.Context, db datastore.Querier, id uuid.UUID) (DeliverySchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(deliveryColumns)
	sb.From(DeliveryTable)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanDelivery(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return DeliverySchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return DeliverySchema{}, fmt.Errorf("webhook: get delivery: %w", err)
	}
	return row, nil
}

// deliveryColumns is the delivery column list, in scan order. A constant
// rather than a slice, because it is never filtered or qualified.
const deliveryColumns = "id, webhook_id, event, body, status, attempt_count, created_at, delivered_at"

// scanDelivery reads one delivery row.
func scanDelivery(scan func(dest ...any) error) (DeliverySchema, error) {
	var row DeliverySchema
	err := scan(
		&row.ID, &row.WebhookID, &row.Event, &row.Body, &row.Status,
		&row.AttemptCount, &row.CreatedAt, &row.DeliveredAt,
	)
	if err != nil {
		return DeliverySchema{}, err
	}
	return row, nil
}

// ListDeliveries answers one page of deliveries, newest first. An endpoint
// identifier scopes the page to one endpoint's deliveries; a nil one is the
// administrative view. An event name narrows both.
func (r *Repository) ListDeliveries(ctx context.Context, db datastore.Querier, webhookID *uuid.UUID, event string, offset, limit int) ([]DeliverySchema, int, error) {
	conditions := func(sb *sqlbuilder.SelectBuilder) []string {
		list := []string{}
		if webhookID != nil {
			list = append(list, sb.Equal("webhook_id", *webhookID))
		}
		if event != "" {
			list = append(list, sb.Equal("event", event))
		}
		return list
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(deliveryColumns)
	sb.From(DeliveryTable)
	sb.Where(conditions(sb)...)
	sb.OrderBy("created_at DESC", "id DESC")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("webhook: list deliveries: %w", err)
	}
	defer rows.Close()

	deliveries := []DeliverySchema{}
	for rows.Next() {
		row, err := scanDelivery(rows.Scan)
		if err != nil {
			return nil, 0, fmt.Errorf("webhook: list deliveries: %w", err)
		}
		deliveries = append(deliveries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("webhook: list deliveries: %w", err)
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(DeliveryTable)
	cb.Where(conditions(cb)...)
	query, args = cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("webhook: count deliveries: %w", err)
	}
	return deliveries, total, nil
}

// CreateAttempt stores one attempt's record and bumps the delivery's attempt
// count in the same statement batch — the count is the delivery's own
// summary of its rows, and the two cannot drift apart.
func (r *Repository) CreateAttempt(ctx context.Context, db datastore.Querier, row AttemptSchema) error {
	row.ID = uuid.NewV7()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(AttemptTable)
	ib.Cols("id", "delivery_id", "attempt_number", "response_status", "error", "duration_ms", "response")
	ib.Values(row.ID, row.DeliveryID, row.AttemptNumber, row.ResponseStatus, row.Error, row.DurationMS, row.Response)

	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(DeliveryTable)
	ub.Set(ub.Assign("attempt_count", row.AttemptNumber))
	ub.Where(ub.Equal("id", row.DeliveryID))

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webhook: create attempt: %w", err)
	}
	query, args = ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webhook: bump attempt count: %w", err)
	}
	return nil
}

// CompleteDelivery stamps a delivery succeeded: the status, the instant an
// attempt proved it, and the attempt count the summary carries.
func (r *Repository) CompleteDelivery(ctx context.Context, db datastore.Querier, id uuid.UUID, attemptNumber int, at time.Time) error {
	return updateDelivery(ctx, db, id, StatusSucceeded, attemptNumber, &at)
}

// FailDelivery stamps a delivery failed — the queue's retries are spent, and
// no attempt will follow.
func (r *Repository) FailDelivery(ctx context.Context, db datastore.Querier, id uuid.UUID, attemptNumber int) error {
	return updateDelivery(ctx, db, id, StatusFailed, attemptNumber, nil)
}

// updateDelivery is the status write both outcomes share. The WHERE clause
// names the pending state alone — single-use state lives in the UPDATE's
// WHERE — so a replayed task that races an earlier answer rewrites nothing:
// zero rows answered is the same success, not a failure.
func updateDelivery(ctx context.Context, db datastore.Querier, id uuid.UUID, status string, attemptNumber int, deliveredAt *time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(DeliveryTable)
	ub.Set(ub.Assign("status", status), ub.Assign("attempt_count", attemptNumber), ub.Assign("delivered_at", deliveredAt))
	ub.Where(ub.Equal("id", id), ub.Equal("status", StatusPending))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("webhook: mark delivery %s: %w", status, err)
	}
	if tag.RowsAffected() == 0 {
		return datastore.ErrNoRows
	}
	return nil
}

// LatestAttempts answers, per delivery, the attempt with the highest number
// the page's deliveries hold. The page is small and the map is keyed by the
// delivery, so the handler can ride the latest attempt on each row.
func (r *Repository) LatestAttempts(ctx context.Context, db datastore.Querier, deliveryIDs []uuid.UUID) (map[uuid.UUID]AttemptSchema, error) {
	if len(deliveryIDs) == 0 {
		return map[uuid.UUID]AttemptSchema{}, nil
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "delivery_id", "attempt_number", "response_status", "error", "duration_ms", "response", "created_at")
	sb.From(AttemptTable)
	sb.Where(sb.In("delivery_id", toAny(deliveryIDs)...))
	sb.OrderBy("delivery_id", "attempt_number")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("webhook: list attempts: %w", err)
	}
	defer rows.Close()

	latest := map[uuid.UUID]AttemptSchema{}
	for rows.Next() {
		var row AttemptSchema
		if err := rows.Scan(
			&row.ID, &row.DeliveryID, &row.AttemptNumber, &row.ResponseStatus,
			&row.Error, &row.DurationMS, &row.Response, &row.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("webhook: list attempts: %w", err)
		}
		latest[row.DeliveryID] = row
	}
	return latest, rows.Err()
}

// toAny lifts a typed slice into the variadic values the IN clause takes.
func toAny(ids []uuid.UUID) []any {
	values := make([]any, len(ids))
	for i, id := range ids {
		values[i] = id
	}
	return values
}

// PruneAttempts deletes the attempts older than the retention window. A
// delivery's own summary — its status and attempt count — survives the
// pruning, so the page a reader sees stays truthful about what happened.
func (r *Repository) PruneAttempts(ctx context.Context, db datastore.Querier, before time.Time) (int64, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(AttemptTable)
	dbt.Where(dbt.LessThan("created_at", before))

	query, args := dbt.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("webhook: prune attempts: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PruneDeliveries deletes the terminal deliveries older than the retention
// window; the attempts cascade with their delivery. Pending rows are never
// candidates — the check rides the DELETE's WHERE, so a delivery that
// reaches its terminal state during the sweep is simply not this run's row.
func (r *Repository) PruneDeliveries(ctx context.Context, db datastore.Querier, before time.Time) (int64, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(DeliveryTable)
	dbt.Where(dbt.LessThan("created_at", before), "status <> "+dbt.Var(StatusPending))

	query, args := dbt.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("webhook: prune deliveries: %w", err)
	}
	return tag.RowsAffected(), nil
}
