package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"encoding/json/v2"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/fetcher"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/responder"
)

// The failures the service defines. The handler maps them onto the codes the
// Connect protocol carries; the service defines what happened, not how it is
// answered.
var (
	// ErrEndpointNotFound is an identifier that names no endpoint.
	ErrEndpointNotFound = errors.New("webhook: endpoint not found")

	// ErrEndpointExists is a name the unique index already holds.
	ErrEndpointExists = errors.New("webhook: endpoint name already in use")

	// ErrReservedHeader is a registration that names a header the delivery
	// contract owns. A custom header that could shadow the signature set
	// would let an endpoint weaken what a receiver verifies against.
	ErrReservedHeader = errors.New("webhook: the headers cannot carry the signature set's names")

	// ErrSecretUnavailable is a create or rotation run without the
	// application secret the sealing needs. The endpoint is refused rather
	// than stored unsigned: a signing secret that never existed cannot be
	// rotated into existence later.
	ErrSecretUnavailable = errors.New("webhook: the application secret is not configured")

	// ErrUnknownEvent is a subscription or a filter naming an event the
	// audit catalog does not declare — a name no record can ever carry.
	ErrUnknownEvent = errors.New("webhook: unknown event name")
)

// The delivery engine's fixed terms: the attempts a delivery may cost and
// the receiver's deadline. The queue carries the same numbers in its config;
// these are what the attempt rows are judged against.
const (
	maxAttempts   = 5
	attemptCapSec = 30
)

// DeliverQueueName is the queue the deliveries run on.
const DeliverQueueName = "webhook_deliver"

// DeliverTask is one delivery: the row whose body every attempt signs and
// sends. The payload is the delivery's whole state — the body and the
// endpoint travel in the rows, so a replay of a lost task finds everything
// it needs.
type DeliverTask struct {
	DeliveryID string `json:"delivery_id"`
}

// Config returns the queue the deliveries run on. Five attempts at a
// thirty-second backoff over a thirty-second receiver deadline is the
// documented contract; a receiver that cannot take a delivery in five tries
// is down, and the attempt rows record the whole story.
func (t DeliverTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        DeliverQueueName,
		MaxAttempts: maxAttempts,
		Timeout:     attemptCapSec * time.Second,
		Backoff:     attemptCapSec * time.Second,
	}
}

// Service carries the rules of the webhook surface: how an endpoint is
// registered and sealed, how an event finds its endpoints, and how a
// delivery is signed and sent. The repository carries the SQL.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of every endpoint change, in the transaction
	// that changes the endpoint.
	audit *audit.Recorder
	// queue carries the deliveries. The emission path enqueues through the
	// transaction that caused the event; the Test path enqueues through the
	// pool.
	queue *queue.Client
	// fetch is the outbound client a delivery attempts through.
	fetch *fetcher.Client
	// cipher seals the signing secrets. It is nil when the application
	// secret is unset — the state a misconfigured deployment is in — and a
	// create or rotation is refused at the call site rather than stored
	// unsigned.
	cipher *crypto.Cipher
	log    *slog.Logger

	// now is the instant the service's decisions read. It is a field so a
	// test can hold the clock still without waiting out a window.
	now func() time.Time
}

// NewService builds the service over the shared pool.
func NewService(pool *datastore.Postgres, recorder *audit.Recorder, client *queue.Client, fetch *fetcher.Client, cipher *crypto.Cipher, log *slog.Logger) *Service {
	return &Service{
		pool:   pool,
		repo:   NewRepository(),
		audit:  recorder,
		queue:  client,
		fetch:  fetch,
		cipher: cipher,
		log:    log,
		now:    time.Now,
	}
}

// CreateParams carries the fields an endpoint is made of.
type CreateParams struct {
	Name        string
	Description string
	Endpoint    string
	Method      string
	Headers     map[string]string
	EventTypes  []string
}

// Create registers an endpoint and mints its signing secret. The secret is
// sealed for the row and returned in the plaintext the response shows once;
// it exists nowhere else, so a lost one means a rotation.
func (s *Service) Create(ctx context.Context, params CreateParams) (EndpointSchema, string, error) {
	if err := checkHeaders(params.Headers); err != nil {
		return EndpointSchema{}, "", err
	}
	if err := checkEvents(params.EventTypes); err != nil {
		return EndpointSchema{}, "", err
	}
	secret, sealed, err := s.mintSecret()
	if err != nil {
		return EndpointSchema{}, "", err
	}

	var row EndpointSchema
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		id, createErr := s.repo.CreateEndpoint(ctx, tx, EndpointSchema{
			Name:       params.Name,
			Descr:      nullable(params.Description),
			Endpoint:   &params.Endpoint,
			Method:     params.Method,
			Headers:    headersJSON(params.Headers),
			Enabled:    true,
			SecretEnc:  &sealed,
			EventTypes: params.EventTypes,
		})
		if errUniqueViolation(createErr) {
			return ErrEndpointExists
		}
		if createErr != nil {
			return fmt.Errorf("webhook: create: %w", createErr)
		}

		created, readErr := s.repo.GetEndpoint(ctx, tx, id)
		if readErr != nil {
			return readErr
		}
		row = created

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventWebhookCreated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceWebhook,
			ResourceID:   id.String(),
			Payload: map[string]string{
				"name":     params.Name,
				"endpoint": params.Endpoint,
			},
		})
		return nil
	})
	if err != nil {
		return EndpointSchema{}, "", err
	}
	return row, secret, nil
}

// List answers one page of the endpoints, newest first. The filters narrow
// the page; the signing secrets are never present.
func (s *Service) List(ctx context.Context, enabled *bool, event string, page, limit int) ([]EndpointSchema, responder.Pagination, error) {
	if err := checkEventFilter(event); err != nil {
		return nil, responder.Pagination{}, err
	}
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)
	rows, total, err := s.repo.ListEndpoints(ctx, s.pool, enabled, event, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	return rows, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// Get answers one endpoint.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (EndpointSchema, error) {
	return s.getEndpoint(ctx, s.pool, id)
}

// getEndpoint is the read every identifier-bearing procedure starts from. A
// row that is gone is the caller's not-found failure.
func (s *Service) getEndpoint(ctx context.Context, db datastore.Querier, id uuid.UUID) (EndpointSchema, error) {
	row, err := s.repo.GetEndpoint(ctx, db, id)
	if errors.Is(err, datastore.ErrNoRows) {
		return EndpointSchema{}, ErrEndpointNotFound
	}
	if err != nil {
		return EndpointSchema{}, err
	}
	return row, nil
}

// UpdateParams carries the fields an update rewrites. A nil field keeps its
// stored value; a present one — even empty — replaces it.
type UpdateParams struct {
	Description *string
	Endpoint    *string
	Method      *string
	Headers     *map[string]string
	Enabled     *bool
	EventTypes  *[]string
}

// Update rewrites the fields the caller named, in the transaction that also
// writes the record of the rewrite.
func (s *Service) Update(ctx context.Context, id uuid.UUID, params UpdateParams) (EndpointSchema, error) {
	if params.Headers != nil {
		if err := checkHeaders(*params.Headers); err != nil {
			return EndpointSchema{}, err
		}
	}
	if params.EventTypes != nil {
		if err := checkEvents(*params.EventTypes); err != nil {
			return EndpointSchema{}, err
		}
	}

	var row EndpointSchema
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		current, getErr := s.getEndpoint(ctx, tx, id)
		if getErr != nil {
			return getErr
		}

		update := EndpointUpdate{Descr: params.Description, Endpoint: params.Endpoint, Method: params.Method, Enabled: params.Enabled}
		if params.Headers != nil {
			sealed := headersJSON(*params.Headers)
			update.Headers = &sealed
		}
		if params.EventTypes != nil {
			update.EventTypes = params.EventTypes
		}
		if updateErr := s.repo.UpdateEndpoint(ctx, tx, id, update, s.now()); updateErr != nil {
			return updateErr
		}

		updated, readErr := s.repo.GetEndpoint(ctx, tx, id)
		if readErr != nil {
			return readErr
		}
		row = updated

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventWebhookUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceWebhook,
			ResourceID:   id.String(),
			Payload: map[string]string{
				"name": current.Name,
			},
		})
		return nil
	})
	if err != nil {
		return EndpointSchema{}, err
	}
	return row, nil
}

// Delete removes one endpoint. The deliveries survive with the endpoint's
// identifier nulled, and the record of the removal commits with it.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, getErr := s.getEndpoint(ctx, tx, id)
		if getErr != nil {
			return getErr
		}

		deleted, delErr := s.repo.DeleteEndpoint(ctx, tx, id)
		if delErr != nil {
			return delErr
		}
		if !deleted {
			return ErrEndpointNotFound
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventWebhookDeleted,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceWebhook,
			ResourceID:   id.String(),
			Payload: map[string]string{
				"name": row.Name,
			},
		})
		return nil
	})
}

// RotateSecret replaces an endpoint's signing secret. The new plaintext is
// the answer, shown once; deliveries made after the rotation sign with it.
func (s *Service) RotateSecret(ctx context.Context, id uuid.UUID) (EndpointSchema, string, error) {
	secret, sealed, err := s.mintSecret()
	if err != nil {
		return EndpointSchema{}, "", err
	}

	var row EndpointSchema
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		current, getErr := s.getEndpoint(ctx, tx, id)
		if getErr != nil {
			return getErr
		}
		if rotateErr := s.repo.RotateSecret(ctx, tx, id, sealed, s.now()); rotateErr != nil {
			return rotateErr
		}

		rotated, readErr := s.repo.GetEndpoint(ctx, tx, id)
		if readErr != nil {
			return readErr
		}
		row = rotated

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventWebhookSecretRotated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceWebhook,
			ResourceID:   id.String(),
			Payload: map[string]string{
				"name": current.Name,
			},
		})
		return nil
	})
	if err != nil {
		return EndpointSchema{}, "", err
	}
	return row, secret, nil
}

// Test queues one test delivery to an endpoint: the event a receiver can
// prove its side of the contract with, without waiting for something to
// actually happen. The delivery rides the endpoint's subscription not at
// all — a test goes where it is pointed.
func (s *Service) Test(ctx context.Context, id uuid.UUID) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, getErr := s.getEndpoint(ctx, tx, id)
		if getErr != nil {
			return getErr
		}

		body, bodyErr := json.Marshal(testBody{Event: EventTest, WebhookID: id.String(), OccurredAt: s.now().UTC().Format(time.RFC3339Nano)})
		if bodyErr != nil {
			return fmt.Errorf("webhook: test body: %w", bodyErr)
		}

		deliveryID, deliveryErr := s.repo.CreateDelivery(ctx, tx, DeliverySchema{
			WebhookID: &row.ID,
			Event:     EventTest,
			Body:      body,
		})
		if deliveryErr != nil {
			return deliveryErr
		}
		if _, saveErr := s.queue.Add(DeliverTask{DeliveryID: deliveryID.String()}).Ctx(ctx).Executor(tx).Save(); saveErr != nil {
			return saveErr
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventWebhookTested,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceWebhook,
			ResourceID:   id.String(),
			Payload: map[string]string{
				"name": row.Name,
			},
		})
		return nil
	})
}

// ListDeliveries answers one page of an endpoint's deliveries, newest first,
// each riding its latest attempt when one exists.
func (s *Service) ListDeliveries(ctx context.Context, webhookID uuid.UUID, page, limit int) ([]DeliveryView, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)
	rows, total, err := s.repo.ListDeliveries(ctx, s.pool, &webhookID, "", responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	views, err := s.attachAttempts(ctx, rows)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	return views, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// ListAllDeliveries answers one page of every delivery the deployment holds,
// newest first, narrowed by the event filter when one is given.
func (s *Service) ListAllDeliveries(ctx context.Context, event string, page, limit int) ([]DeliveryView, responder.Pagination, error) {
	if err := checkEventFilter(event); err != nil {
		return nil, responder.Pagination{}, err
	}
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)
	rows, total, err := s.repo.ListDeliveries(ctx, s.pool, nil, event, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	views, err := s.attachAttempts(ctx, rows)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	return views, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// DeliveryView is one delivery with the attempt that ran last, the shape a
// list answers with. The body never rides the view: it is the delivery's
// stored bytes, and the wire view names what happened, not what was sent.
type DeliveryView struct {
	Delivery DeliverySchema
	Attempt  *AttemptSchema
}

// attachAttempts joins the page's deliveries to their latest attempts.
func (s *Service) attachAttempts(ctx context.Context, rows []DeliverySchema) ([]DeliveryView, error) {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	latest, err := s.repo.LatestAttempts(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}

	views := make([]DeliveryView, 0, len(rows))
	for _, row := range rows {
		view := DeliveryView{Delivery: row}
		if attempt, ok := latest[row.ID]; ok {
			view.Attempt = &attempt
		}
		views = append(views, view)
	}
	return views, nil
}

// AuditRecorded is the emission seam: every audit record the deployment
// writes is a candidate delivery. The endpoints that subscribe to the event
// each get one delivery row, and every row rides the transaction the record
// rode — a rollback takes its deliveries with it, so a receiver is never
// told about a change that did not happen.
//
// It never fails the caller. An emission is a side effect of the record the
// feature asked for, and a failure here costs the delivery, not the change;
// the failure is logged so a broken pipeline is visible.
func (s *Service) AuditRecorded(ctx context.Context, db datastore.Querier, entry audit.Entry) {
	if s == nil || s.queue == nil {
		return
	}
	// The catalog is the emission filter: a record is delivered only under
	// the dot name the catalog maps its audit event to. An unmapped source
	// is a record the surface never offered — quiet by design, visible in
	// the debug log so a forgotten mapping can be found.
	event, mapped := EventForSource(entry.Event)
	if !mapped {
		s.log.DebugContext(ctx, "webhook: audit event not in the catalog, not emitted", "event", entry.Event)
		return
	}
	body, err := json.Marshal(canonicalBody(event.Name, entry, s.now()))
	if err != nil {
		// A map of strings cannot fail to marshal, so this guards the
		// encoding rather than a caller's data.
		s.log.ErrorContext(ctx, "webhook: emission body not built", "event", entry.Event, "err", err)
		return
	}
	if len(body) > BodyLimit {
		s.log.WarnContext(ctx, "webhook: emission body skipped, over the limit", "event", entry.Event, "bytes", len(body))
		return
	}

	err = datastore.WithSavepoint(ctx, db, func(ctx context.Context, tx datastore.Querier) error {
		endpoints, matchErr := s.repo.MatchEndpoints(ctx, tx, event.Name)
		if matchErr != nil {
			return matchErr
		}
		for _, endpoint := range endpoints {
			deliveryID, createErr := s.repo.CreateDelivery(ctx, tx, DeliverySchema{
				WebhookID: &endpoint.ID,
				Event:     event.Name,
				Body:      body,
			})
			if createErr != nil {
				return createErr
			}
			if _, saveErr := s.queue.Add(DeliverTask{DeliveryID: deliveryID.String()}).Ctx(ctx).Executor(tx).Save(); saveErr != nil {
				return saveErr
			}
		}
		return nil
	})
	if err != nil {
		// The event is the one field that identifies which emission went
		// nowhere, so it is what the line carries.
		s.log.ErrorContext(ctx, "webhook: emissions not enqueued", "event", entry.Event, "err", err)
	}
}

// RunDelivery performs one delivery attempt: it loads the delivery and its
// endpoint, signs the stored body, sends it, and records the attempt. A
// non-2xx answer or a transport failure is the attempt's failure, and the
// queue's retry owns the next one — the error is what the retry is made of.
//
// A delivery whose endpoint is gone, disabled, or unsigned cannot be
// delivered by any number of retries, so those are marked failed and the
// task ends quietly rather than burning the queue's attempts on a fact.
func (s *Service) RunDelivery(ctx context.Context, deliveryID string) error {
	id, parseErr := uuid.Parse(deliveryID)
	if parseErr != nil {
		return fmt.Errorf("webhook: delivery id: %w", parseErr)
	}

	delivery, getErr := s.repo.GetDelivery(ctx, s.pool, id)
	if errors.Is(getErr, datastore.ErrNoRows) {
		// The delivery is gone — a caller's retry of a deleted row. The
		// queue's task ends, and there is nothing left to mark.
		return nil
	}
	if getErr != nil {
		return getErr
	}

	endpoint := s.endpointFor(ctx, delivery)
	if endpoint == nil {
		return nil
	}
	if !endpoint.Enabled {
		return s.failQuietly(ctx, delivery, "the endpoint is disabled")
	}
	if endpoint.SecretEnc == nil {
		return s.failQuietly(ctx, delivery, "the endpoint carries no signing secret")
	}
	if s.cipher == nil {
		return s.failQuietly(ctx, delivery, "the application secret is not configured")
	}
	secret, sealErr := s.cipher.Decrypt(*endpoint.SecretEnc)
	if sealErr != nil {
		return fmt.Errorf("webhook: unseal secret: %w", sealErr)
	}

	attemptNumber := delivery.AttemptCount + 1
	started := s.now()
	signature := Sign(secret, started, delivery.Body)

	headers := make(http.Header)
	for name, value := range endpoint.CustomHeaders() {
		// The signature set cannot be overridden, so the contract's headers
		// are written after the custom ones and win by order, not by trust.
		if !reservedHeaders[strings.ToLower(name)] {
			headers.Set(name, value)
		}
	}
	headers.Set(TypeHeader, ContentType)
	headers.Set(EventHeader, delivery.Event)
	headers.Set(IDHeader, FormatEndpointID(endpoint.ID))
	headers.Set(SignatureHeader, signature)

	res, err := s.fetch.Do(ctx, fetcher.Request{
		Method:  endpoint.Method,
		URL:     deref(endpoint.Endpoint),
		Headers: headers,
		Body:    delivery.Body,
	})
	duration := int(time.Since(started).Milliseconds())

	attempt := AttemptSchema{
		DeliveryID:    delivery.ID,
		AttemptNumber: attemptNumber,
	}
	if err != nil {
		cause := err.Error()
		attempt.Error = &cause
	} else {
		status := res.StatusCode
		attempt.ResponseStatus = &status
		attempt.DurationMS = &duration
		if kind := res.Header.Get("Content-Type"); kind != "" {
			metadata, metaErr := json.Marshal(map[string]string{"content_type": kind})
			if metaErr == nil {
				attempt.Response = metadata
			}
		}
	}
	if attemptErr := s.repo.CreateAttempt(ctx, s.pool, attempt); attemptErr != nil {
		return attemptErr
	}

	if err == nil && res.StatusCode >= 200 && res.StatusCode < 300 {
		return s.repo.CompleteDelivery(ctx, s.pool, delivery.ID, attemptNumber, s.now())
	}
	if attemptNumber >= maxAttempts {
		return s.repo.FailDelivery(ctx, s.pool, delivery.ID, attemptNumber)
	}
	// The failure is the queue's retry signal, so the answer names what the
	// attempt died of without discarding the attempt row's own record.
	if err != nil {
		return fmt.Errorf("webhook: deliver: %w", err)
	}
	return fmt.Errorf("webhook: %s answered %d", deref(endpoint.Endpoint), res.StatusCode)
}

// endpointFor reads the delivery's endpoint. A delivery whose endpoint row is
// gone — deleted, or the identifier nulled by the delete — cannot be
// delivered by any number of retries, so it is marked failed and the task
// ends quietly.
func (s *Service) endpointFor(ctx context.Context, delivery DeliverySchema) *EndpointSchema {
	if delivery.WebhookID == nil {
		s.markUndeliverable(ctx, delivery, "the endpoint is gone")
		return nil
	}
	row, err := s.repo.GetEndpoint(ctx, s.pool, *delivery.WebhookID)
	if errors.Is(err, datastore.ErrNoRows) {
		s.markUndeliverable(ctx, delivery, "the endpoint is gone")
		return nil
	}
	if err != nil {
		// A read that failed for a reason the next attempt might not repeat
		// leaves the delivery pending: the queue's retry is the right
		// answer, and the line keeps the failure visible.
		s.log.ErrorContext(ctx, "webhook: endpoint not read", "delivery_id", delivery.ID, "err", err)
		return nil
	}
	return &row
}

// markUndeliverable marks a delivery that no retry can save. The attempt row
// is written first, because the reason the delivery died is the one thing an
// operator will ask for; a write that itself fails is logged, since the
// delivery's pending row is the better loss than the task's error.
func (s *Service) markUndeliverable(ctx context.Context, delivery DeliverySchema, reason string) {
	if err := s.failQuietly(ctx, delivery, reason); err != nil {
		s.log.ErrorContext(ctx, "webhook: undeliverable delivery not marked",
			"delivery_id", delivery.ID, "err", err)
	}
}

// failQuietly marks a delivery that no retry can save and answers nil, so
// the queue's task ends instead of burning its attempts on a fact. The
// attempt row is written first, because the reason the delivery died is the
// one thing an operator will ask for.
func (s *Service) failQuietly(ctx context.Context, delivery DeliverySchema, reason string) error {
	attemptNumber := delivery.AttemptCount + 1
	cause := reason
	attempt := AttemptSchema{
		DeliveryID:    delivery.ID,
		AttemptNumber: attemptNumber,
		Error:         &cause,
	}
	if attemptErr := s.repo.CreateAttempt(ctx, s.pool, attempt); attemptErr != nil {
		return attemptErr
	}
	return s.repo.FailDelivery(ctx, s.pool, delivery.ID, attemptNumber)
}

// PruneAttempts deletes the attempts older than the retention window. The
// delivery rows keep their summary — status and attempt count — so the
// story a list tells stays true while the per-attempt detail ages out.
func (s *Service) PruneAttempts(ctx context.Context) (int64, error) {
	return s.repo.PruneAttempts(ctx, s.pool, s.now().AddDate(0, 0, -attemptRetentionDays))
}

// attemptRetentionDays is how long an attempt row is kept.
const attemptRetentionDays = 7

// mintSecret draws a signing secret and seals it. The plaintext exists in
// the create and rotation responses alone; the row carries the sealed half.
func (s *Service) mintSecret() (string, string, error) {
	if s.cipher == nil {
		return "", "", ErrSecretUnavailable
	}
	secret, err := crypto.RandomString(32, crypto.AlphabetAlphanumeric)
	if err != nil {
		return "", "", fmt.Errorf("webhook: generate secret: %w", err)
	}
	sealed, err := s.cipher.Encrypt(secret)
	if err != nil {
		return "", "", fmt.Errorf("webhook: seal secret: %w", err)
	}
	return secret, sealed, nil
}

// Sign computes the signature one delivery attempt carries: HMAC-SHA256 over
// the unix timestamp concatenated with the exact canonical body bytes,
// rendered as `t=<unix>,v1=<hex>`. A receiver recomputes the digest over the
// body it received and the timestamp inside the header, within a five-minute
// skew each way.
func Sign(secret string, at time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(at.Unix(), 10) + "."))
	mac.Write(body)
	return "t=" + strconv.FormatInt(at.Unix(), 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// canonicalBody is the delivery's canonical body: the record the audit log
// wrote, encoded deterministically under the wire name the catalog maps it
// to. The map keys are sorted by the encoder, so the same record always
// produces the same bytes — the property a signature over the body relies
// on.
func canonicalBody(event string, entry audit.Entry, at time.Time) eventBody {
	trigger := entry.Trigger
	if trigger == "" {
		trigger = audit.TriggerUser
	}
	status := entry.Status
	if status == "" {
		status = audit.StatusSuccess
	}
	return eventBody{
		Event:        event,
		Trigger:      trigger,
		Status:       status,
		UserID:       entry.UserID,
		ResourceType: entry.ResourceType,
		ResourceID:   entry.ResourceID,
		Payload:      entry.Payload,
		IPAddress:    entry.Client.IPAddress,
		UserAgent:    entry.Client.UserAgent,
		OccurredAt:   at.UTC().Format(time.RFC3339Nano),
	}
}

// eventBody is the JSON shape a delivery's body carries. It is the audit
// record's own vocabulary, so a receiver reads the same field names the log
// does.
type eventBody struct {
	Event        string            `json:"event"`
	Trigger      string            `json:"trigger"`
	Status       string            `json:"status"`
	UserID       string            `json:"user_id,omitzero"`
	ResourceType string            `json:"resource_type,omitzero"`
	ResourceID   string            `json:"resource_id,omitzero"`
	Payload      map[string]string `json:"payload,omitzero"`
	IPAddress    string            `json:"ip_address,omitzero"`
	UserAgent    string            `json:"user_agent,omitzero"`
	OccurredAt   string            `json:"occurred_at"`
}

// testBody is the JSON shape a test delivery's body carries.
type testBody struct {
	Event      string `json:"event"`
	WebhookID  string `json:"webhook_id"`
	OccurredAt string `json:"occurred_at"`
}

// checkHeaders refuses a registration that names a header the delivery
// contract owns. The comparison is case-insensitive, the way header names
// travel.
func checkHeaders(headers map[string]string) error {
	for name := range headers {
		if reservedHeaders[strings.ToLower(name)] {
			return ErrReservedHeader
		}
	}
	return nil
}

// checkEvents refuses a subscription naming an event outside the webhook
// catalog. The wildcard is the one name the catalog does not declare, and
// the empty list is not a subscription to check — an endpoint that lists no
// events receives them all.
func checkEvents(events []string) error {
	for _, event := range events {
		if event != "*" && !IsEvent(event) {
			return fmt.Errorf("%w: %s", ErrUnknownEvent, event)
		}
	}
	return nil
}

// checkEventFilter refuses a list filter naming an event outside the
// webhook catalog. An empty filter is no filter, and the wildcard narrows
// to the endpoints subscribed to everything.
func checkEventFilter(event string) error {
	if event == "" {
		return nil
	}
	return checkEvents([]string{event})
}

// EventCatalog returns the webhook event catalog: the dot-named events a
// receiver subscribes to, each with the audit event it maps from and the
// sentence that says what happened. The catalog lives beside the emission
// that uses it; this surface serves it so a client chooses from what can
// actually be delivered.
func (s *Service) EventCatalog() []Event {
	return EventCatalog()
}

// headersJSON encodes the custom headers the column stores. An empty set is
// an empty object rather than a NULL, so a read never has to tell the two
// apart.
func headersJSON(headers map[string]string) []byte {
	if len(headers) == 0 {
		return []byte("{}")
	}
	encoded, err := json.Marshal(headers)
	if err != nil {
		// A map of strings cannot fail to marshal, so this guards the
		// encoding rather than a caller's data.
		return []byte("{}")
	}
	return encoded
}

// nullable turns an absent note into the NULL its column stores.
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// deref reads the pointer the optional column carries. An endpoint row the
// service wrote always carries a URL, so the nil case is a corrupt row, and
// an empty string is what the fetch will refuse.
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// errUniqueViolation reports whether the write failed on a unique index, the
// way the name index answers a duplicate endpoint name.
func errUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
