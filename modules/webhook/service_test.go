package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/fetcher"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"
)

// The hex the test cipher builds from: sixty-four hex characters, the shape
// the application secret carries.
const testKeyHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// testSecretKey derives a Cipher the way the area's provider does: from the
// application secret's hex.
func testSecretKey(t *testing.T) *crypto.Cipher {
	t.Helper()

	cipher, err := crypto.NewCipherFromHex(testKeyHex)
	require.NoError(t, err)
	return cipher
}

// testQueue builds the queue client over the pool, with the delivery queue
// registered to a processor that does nothing — the tests drive the
// deliveries through RunDelivery directly, so the queue is here to carry the
// enqueues the emission writes.
func testQueue(t *testing.T, pool *datastore.Postgres) *queue.Client {
	t.Helper()

	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		Logger:       slog.New(slog.DiscardHandler),
		NumWorkers:   1,
		ReleaseAfter: time.Minute,
	})
	require.NoError(t, err)
	client.Register(queue.NewQueue[DeliverTask](func(ctx context.Context, task DeliverTask) error {
		return nil
	}))
	return client
}

// testService builds the service over a migrated pool, with a real queue
// (for the enqueues) and, when a receiver is given, a real fetcher (for the
// attempts). The tests that read deliveries need the emission to have
// written rows, so nothing here is a stub.
func testService(t *testing.T, pool *datastore.Postgres, receiver *httptest.Server) *Service {
	t.Helper()

	var fetch *fetcher.Client
	if receiver != nil {
		fetchClient, err := fetcher.New(config.Default(), slog.New(slog.DiscardHandler))
		require.NoError(t, err)
		fetch = fetchClient
	}

	return NewService(
		pool,
		audit.NewRecorder(slog.New(slog.DiscardHandler)),
		testQueue(t, pool),
		fetch,
		testSecretKey(t),
		slog.New(slog.DiscardHandler),
	)
}

// migratedPool opens a database the migrations have built, so the webhook
// tables exist.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()
	return testutils.MigratedPostgres(t, "webhook_test")
}

// receiver builds a test server that records what a delivery looked like
// when it arrived, and answers the status the test asks for.
type receiver struct {
	server   *httptest.Server
	header   http.Header
	body     []byte
	requests int
}

// newReceiver answers a receiver that records one request's shape and
// answers with the status given.
func newReceiver(t *testing.T, status int) *receiver {
	t.Helper()

	r := &receiver{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.header = req.Header.Clone()
		r.body, _ = io.ReadAll(req.Body)
		r.requests++
		w.WriteHeader(status)
	}))
	t.Cleanup(r.server.Close)
	return r
}

// endpoint is a fixture registration: the receiver as its destination,
// subscribed to the events the caller names.
func endpoint(t *testing.T, service *Service, r *receiver, name string, events ...string) EndpointSchema {
	t.Helper()

	row, _, err := service.Create(t.Context(), CreateParams{
		Name:       name,
		Endpoint:   r.server.URL,
		Method:     http.MethodPost,
		Headers:    map[string]string{"X-Tenant": "hogwarts"},
		EventTypes: events,
	})
	require.NoError(t, err)
	return row
}

// TestCreateShowsTheSecretOnceAndRefusesADuplicateName pins the two halves
// of the create: the secret is the response's alone, and the name is the
// index's to police.
func TestCreateShowsTheSecretOnceAndRefusesADuplicateName(t *testing.T) {
	pool := migratedPool(t)
	r := newReceiver(t, http.StatusOK)
	service := testService(t, pool, r.server)

	row, secret, err := service.Create(t.Context(), CreateParams{
		Name:     "hogwarts-events",
		Endpoint: r.server.URL,
		Method:   http.MethodPost,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, secret, "the signing secret is the create response's alone")
	assert.NotContains(t, secret, "enc:", "the response carries the plaintext, not the sealed form")

	// The stored half is sealed: the ciphertext opens under the cipher and
	// the plaintext matches, so a database leak cannot forge a signature.
	stored, err := service.Get(t.Context(), row.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.SecretEnc)
	decrypted, err := testSecretKey(t).Decrypt(*stored.SecretEnc)
	require.NoError(t, err)
	assert.Equal(t, secret, decrypted)

	_, _, err = service.Create(t.Context(), CreateParams{
		Name:     "hogwarts-events",
		Endpoint: r.server.URL,
		Method:   http.MethodPost,
	})
	assert.ErrorIs(t, err, ErrEndpointExists)
}

// TestCreateRefusesTheSignatureSetNames pins the header rule: a registration
// that names a header the delivery contract owns is refused, because a
// custom header that could shadow the signature set would let an endpoint
// weaken what a receiver verifies against.
func TestCreateRefusesTheSignatureSetNames(t *testing.T) {
	pool := migratedPool(t)
	r := newReceiver(t, http.StatusOK)
	service := testService(t, pool, r.server)

	for _, name := range []string{"X-Signature", "x-signature", "Content-Type"} {
		label := strings.ToLower(strings.ReplaceAll(name, "-", ""))
		_, _, err := service.Create(t.Context(), CreateParams{
			Name:     "reserved-" + label,
			Endpoint: r.server.URL,
			Method:   http.MethodPost,
			Headers:  map[string]string{name: "forged"},
		})
		assert.ErrorIs(t, err, ErrReservedHeader, "a %s header must be refused", name)
	}
}

// TestEmissionMatchesSubscriptions pins the subscription test: an empty list
// receives every event, an exact entry receives its own, the wildcard
// receives everything, and a disabled endpoint receives nothing — and the
// emission rides the transaction the record rode, so a rollback would take
// its deliveries with it.
func TestEmissionMatchesSubscriptions(t *testing.T) {
	pool := migratedPool(t)
	r := newReceiver(t, http.StatusOK)
	service := testService(t, pool, r.server)

	_, _, err := service.Create(t.Context(), CreateParams{Name: "every-event", Endpoint: r.server.URL, Method: http.MethodPost})
	require.NoError(t, err)
	_, _, err = service.Create(t.Context(), CreateParams{Name: "sign-ins-only", Endpoint: r.server.URL, Method: http.MethodPost, EventTypes: []string{"session.signed_in"}})
	require.NoError(t, err)
	_, _, err = service.Create(t.Context(), CreateParams{Name: "wildcard", Endpoint: r.server.URL, Method: http.MethodPost, EventTypes: []string{"*"}})
	require.NoError(t, err)
	disabled := endpoint(t, service, r, "disabled", "*")
	off := false
	_, err = service.Update(t.Context(), disabled.ID, UpdateParams{Enabled: &off})
	require.NoError(t, err)

	// The emission runs inside the caller's transaction, the way the audit
	// recorder hands the record's surface over.
	err = pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		service.AuditRecorded(ctx, tx, audit.Entry{Event: audit.EventSignIn})
		return nil
	})
	require.NoError(t, err)

	_, total, err := service.repo.ListDeliveries(t.Context(), pool, nil, mustWireEvent(t, audit.EventSignIn), 0, 100)
	require.NoError(t, err)
	assert.Equal(t, 3, total, "the empty list, the exact entry, and the wildcard receive the event; the disabled endpoint does not")
	disabledDeliveries, _, err := service.repo.ListDeliveries(t.Context(), pool, &disabled.ID, mustWireEvent(t, audit.EventSignIn), 0, 100)
	require.NoError(t, err)
	assert.Empty(t, disabledDeliveries, "the disabled endpoint receives nothing")

	// A second emission of another event costs each subscriber one more
	// delivery: every happening is delivered, not folded.
	err = pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		service.AuditRecorded(ctx, tx, audit.Entry{Event: audit.EventAccountCreated})
		return nil
	})
	require.NoError(t, err)
	_, total, err = service.repo.ListDeliveries(t.Context(), pool, nil, mustWireEvent(t, audit.EventAccountCreated), 0, 100)
	require.NoError(t, err)
	assert.Equal(t, 2, total, "the other event finds the empty list and the wildcard; the exact subscriber does not")
}

// TestRunDeliverySignsTheBodyAndRecordsTheAttempt pins the delivery
// contract: the receiver sees the signature headers, the digest verifies
// against the secret and the exact body bytes, and the attempt row records
// the outcome.
func TestRunDeliverySignsTheBodyAndRecordsTheAttempt(t *testing.T) {
	pool := migratedPool(t)
	r := newReceiver(t, http.StatusOK)
	service := testService(t, pool, r.server)

	row := endpoint(t, service, r, "receiver", "session.signed_in")
	deliveryID := emitOne(t, service, row.ID, audit.EventSignIn)

	require.NoError(t, service.RunDelivery(t.Context(), deliveryID))
	require.Equal(t, 1, r.requests)

	// The signature verifies the way a receiver computes it: the digest of
	// the timestamp and the exact body bytes, under the secret shown once.
	secret := storedSecret(t, service, row.ID)
	value := strings.TrimPrefix(r.header.Get(SignatureHeader), "t=")
	timestamp, sig, ok := strings.Cut(value, ",v1=")
	require.True(t, ok, "the signature header carries t=<unix>,v1=<hex>")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(r.body)
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), sig)
	assert.Equal(t, mustWireEvent(t, audit.EventSignIn), r.header.Get(EventHeader))
	assert.Equal(t, FormatEndpointID(row.ID), r.header.Get(IDHeader), "the endpoint's wire form rides the header")
	assert.Equal(t, "hogwarts", r.header.Get("X-Tenant"), "the custom headers ride along")
	assert.Equal(t, ContentType, r.header.Get(TypeHeader))

	// The delivery is done, and the attempt row tells the story.
	delivery, err := service.repo.GetDelivery(t.Context(), pool, mustParse(t, deliveryID))
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, delivery.Status)
	require.NotNil(t, delivery.DeliveredAt)
	assert.Equal(t, 1, delivery.AttemptCount)

	attempts, err := service.repo.LatestAttempts(t.Context(), pool, []uuid.UUID{delivery.ID})
	require.NoError(t, err)
	attempt, ok := attempts[delivery.ID]
	require.True(t, ok)
	assert.Equal(t, http.StatusOK, *attempt.ResponseStatus)
	assert.NotNil(t, attempt.DurationMS)
}

// TestRunDeliveryRetriesThenFails pins the retry terms: a failed attempt is
// the error the queue retries on, the attempt count climbs with each one,
// and the fifth failure is where the delivery's story ends.
func TestRunDeliveryRetriesThenFails(t *testing.T) {
	pool := migratedPool(t)
	r := newReceiver(t, http.StatusInternalServerError)
	service := testService(t, pool, r.server)

	row := endpoint(t, service, r, "downstream", "session.signed_in")
	deliveryID := emitOne(t, service, row.ID, audit.EventSignIn)

	for attempt := 1; attempt <= maxAttempts-1; attempt++ {
		err := service.RunDelivery(t.Context(), deliveryID)
		require.Error(t, err, "a non-2xx answer is the retry signal")
		delivery, getErr := service.repo.GetDelivery(t.Context(), pool, mustParse(t, deliveryID))
		require.NoError(t, getErr)
		assert.Equal(t, StatusPending, delivery.Status, "a delivery inside its attempt budget is still pending")
		assert.Equal(t, attempt, delivery.AttemptCount)
	}

	// The last attempt marks the delivery failed and ends the story: the
	// task answers quietly, because the delivery row now carries the truth
	// and the queue has no more retries to spend.
	require.NoError(t, service.RunDelivery(t.Context(), deliveryID))
	delivery, err := service.repo.GetDelivery(t.Context(), pool, mustParse(t, deliveryID))
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, delivery.Status)
	assert.Equal(t, maxAttempts, delivery.AttemptCount)
}

// TestRotateSecretAffectsNewDeliveriesOnly pins the rotation: the old secret
// stops working, the new one signs, and the stored ciphertext is never
// answered again — only the rotation response carried the new plaintext.
func TestRotateSecretAffectsNewDeliveriesOnly(t *testing.T) {
	pool := migratedPool(t)
	r := newReceiver(t, http.StatusOK)
	service := testService(t, pool, r.server)

	row, first, err := service.Create(t.Context(), CreateParams{Name: "rotating", Endpoint: r.server.URL, Method: http.MethodPost})
	require.NoError(t, err)
	rotated, second, err := service.RotateSecret(t.Context(), row.ID)
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "every rotation mints a new secret")
	assert.Equal(t, rotated.ID, row.ID)

	deliveryID := emitOne(t, service, row.ID, audit.EventSignIn)
	require.NoError(t, service.RunDelivery(t.Context(), deliveryID))
	require.Equal(t, 1, r.requests)

	// The receiver verifies the new signature against the new secret: the
	// stored ciphertext opens to the plaintext the response showed.
	secret := storedSecret(t, service, row.ID)
	assert.Equal(t, second, secret, "the rotation's shown secret is the one the row seals")
	timestamp, sig, ok := strings.Cut(strings.TrimPrefix(r.header.Get(SignatureHeader), "t="), ",v1=")
	require.True(t, ok)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(r.body)
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), sig)
}

// TestDeleteKeepsTheDeliveries pins the delete: the endpoint goes, the
// deliveries survive with the identifier nulled, and a delivery whose
// endpoint is gone is marked failed rather than retried.
func TestDeleteKeepsTheDeliveries(t *testing.T) {
	pool := migratedPool(t)
	r := newReceiver(t, http.StatusOK)
	service := testService(t, pool, r.server)

	row := endpoint(t, service, r, "departing", "session.signed_in")
	deliveryID := emitOne(t, service, row.ID, audit.EventSignIn)

	require.NoError(t, service.Delete(t.Context(), row.ID))
	_, err := service.Get(t.Context(), row.ID)
	assert.ErrorIs(t, err, ErrEndpointNotFound)

	delivery, err := service.repo.GetDelivery(t.Context(), pool, mustParse(t, deliveryID))
	require.NoError(t, err)
	assert.Nil(t, delivery.WebhookID, "the delivery outlives its destination")

	// The runner answers quietly: there is no endpoint to retry against.
	assert.NoError(t, service.RunDelivery(t.Context(), deliveryID))
	failed, err := service.repo.GetDelivery(t.Context(), pool, mustParse(t, deliveryID))
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, failed.Status)
}

// TestTheTestDeliveryRidesItsOwnEvent pins the Test procedure: it queues one
// `webhook.test` delivery to the endpoint it is pointed at, subscription or
// not.
func TestTheTestDeliveryRidesItsOwnEvent(t *testing.T) {
	pool := migratedPool(t)
	r := newReceiver(t, http.StatusOK)
	service := testService(t, pool, r.server)

	row := endpoint(t, service, r, "proving", "session.signed_in")

	require.NoError(t, service.Test(t.Context(), row.ID))
	deliveries, total, err := service.repo.ListDeliveries(t.Context(), pool, &row.ID, EventTest, 0, 100)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	assert.Equal(t, EventTest, deliveries[0].Event)

	// The endpoint is subscribed to sign_in alone, so the test event proves
	// the plumbing rather than the subscription.
	_, allTotal, err := service.repo.ListDeliveries(t.Context(), pool, &row.ID, "", 0, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, allTotal)
}

// TestTheWireFormsRoundTripThroughTheTypeIDPrefixes pins the boundary: the
// endpoint, delivery, and attempt identifiers render with their own prefixes
// on the wire and parse back to the UUID the column stores, and a wire form
// without the prefix names nothing.
func TestTheWireFormsRoundTripThroughTheTypeIDPrefixes(t *testing.T) {
	raw := uuid.NewV7()

	assert.Regexp(t, `^whk_[0-9a-z]{26}$`, FormatEndpointID(raw))
	assert.Regexp(t, `^whd_[0-9a-z]{26}$`, FormatDeliveryID(raw))
	assert.Regexp(t, `^wha_[0-9a-z]{26}$`, FormatAttemptID(raw))

	parsed, err := ParseEndpointID(FormatEndpointID(raw))
	require.NoError(t, err)
	assert.Equal(t, raw, parsed)

	_, err = ParseEndpointID(raw.String())
	assert.Error(t, err, "a bare UUID is not the wire form")
}

// emitOne runs one emission inside a transaction and answers the delivery
// the endpoint received. It is the shape every audit record takes on the way
// to a delivery.
func emitOne(t *testing.T, service *Service, webhookID uuid.UUID, event string) string {
	t.Helper()

	var deliveryID string
	err := service.pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		service.AuditRecorded(ctx, tx, audit.Entry{Event: event})
		rows, _, listErr := service.repo.ListDeliveries(ctx, tx, &webhookID, mustWireEvent(t, event), 0, 1)
		if listErr != nil {
			return listErr
		}
		if len(rows) == 0 {
			return errors.New("webhook: the emission produced no delivery")
		}
		deliveryID = rows[0].ID.String()
		return nil
	})
	require.NoError(t, err)
	return deliveryID
}

// mustWireEvent maps an audit event onto the wire name the catalog delivers
// it under. The catalog is expected to carry every audit event; a test that
// reaches an unmapped one is testing an emission that cannot happen.
func mustWireEvent(t *testing.T, source string) string {
	t.Helper()
	event, ok := EventForSource(source)
	require.True(t, ok, "the catalog maps every audit event")
	return event.Name
}

// storedSecret opens the endpoint's sealed signing secret, the way the
// delivery runner does before it signs.
func storedSecret(t *testing.T, service *Service, id uuid.UUID) string {
	t.Helper()

	row, err := service.Get(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, row.SecretEnc)
	secret, err := testSecretKey(t).Decrypt(*row.SecretEnc)
	require.NoError(t, err)
	return secret
}

// mustParse turns a delivery identifier into the key the rows carry.
func mustParse(t *testing.T, id string) uuid.UUID {
	t.Helper()

	parsed, err := uuid.Parse(id)
	require.NoError(t, err)
	return parsed
}
