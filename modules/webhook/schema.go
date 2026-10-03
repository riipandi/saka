package webhook

import (
	"encoding/json/v2"
	"fmt"
	"time"

	"uuid"

	"go.jetify.com/typeid"
)

// The tables the webhook feature owns. The migrations own the schema; these
// constants are how Go code names it, so a table rename touches one line.
// ResourceWebhook is the resource type an audit record names when the change
// is about an endpoint.
const ResourceWebhook = "webhook"

// EventTest is the wire name the Test delivery carries. It is not an audit
// event — no record causes it — but it lives in the catalog, so a receiver
// can prove its side of the contract without waiting for something to
// actually happen.
const EventTest = "webhook.test"

// Delivery statuses, the values the column's check accepts.
const (
	StatusPending   = "pending"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// The delivery contract's fixed terms. They are constants rather than
// configuration because the documented contract states them, and a
// deployment that needs different numbers needs a different contract.
const (
	// BodyLimit caps a delivery's canonical body. A payload that outgrows it
	// is skipped at emission — a webhook is a signal, not a document channel.
	BodyLimit = 1 << 20

	// SignatureHeader carries the timestamp and digest a receiver verifies.
	SignatureHeader = "X-Signature"
	// EventHeader names the event the body describes.
	EventHeader = "X-Webhook-Event"
	// IDHeader names the endpoint the delivery was made for.
	IDHeader = "X-Webhook-Id"
	// TypeHeader is the content type every delivery carries.
	TypeHeader = "Content-Type"
	// ContentType is the media type of the canonical body.
	ContentType = "application/json"
)

// reservedHeaders are the header names a registration may not set: they are
// the delivery contract's own, and a custom header that could shadow one
// would let an endpoint weaken the signature a receiver verifies against.
// The comparison is case-insensitive, the way header names travel.
var reservedHeaders = map[string]bool{
	"content-type":    true,
	"x-signature":     true,
	"x-webhook-event": true,
	"x-webhook-id":    true,
}

// EndpointIDPrefix is the TypeID prefix of an endpoint row's identifier. The
// id leaves the server in API responses and in the X-Webhook-Id header every
// delivery carries, so the reader of a receiver's log line can tell what it
// names without a lookup.
type EndpointIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (EndpointIDPrefix) Prefix() string { return "whk" }

// EndpointID is the typed identifier of one row of entity.TableWebhookEndpoints, in its wire
// form. The column stays a UUID; the conversion lives here and nowhere else.
type EndpointID = typeid.TypeID[EndpointIDPrefix]

// DeliveryIDPrefix is the TypeID prefix of a delivery row's identifier.
type DeliveryIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (DeliveryIDPrefix) Prefix() string { return "whd" }

// DeliveryID is the typed identifier of one row of entity.TableWebhookDeliveries, in its wire
// form.
type DeliveryID = typeid.TypeID[DeliveryIDPrefix]

// AttemptIDPrefix is the TypeID prefix of an attempt row's identifier.
type AttemptIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (AttemptIDPrefix) Prefix() string { return "wha" }

// AttemptID is the typed identifier of one row of entity.TableWebhookDeliveryAttempts, in its wire
// form.
type AttemptID = typeid.TypeID[AttemptIDPrefix]

// FormatEndpointID renders the endpoint's wire form. The rows the database
// holds always carry a valid UUID, so the render cannot fail; an invalid one
// answers the empty string, which no consumer should mistake for an id.
func FormatEndpointID(raw uuid.UUID) string {
	id, err := typeid.FromUUID[EndpointID](raw.String())
	if err != nil {
		return ""
	}
	return id.String()
}

// ParseEndpointID reads the endpoint's wire form back. It is the boundary a
// request crosses: an identifier that arrives without the prefix names no
// endpoint, the not-found the caller refuses.
func ParseEndpointID(wire string) (uuid.UUID, error) {
	parsed, err := typeid.Parse[EndpointID](wire)
	if err != nil {
		return uuid.Nil(), fmt.Errorf("webhook: %w", err)
	}
	return uuid.UUID(parsed.UUIDBytes()), nil
}

// FormatDeliveryID renders the delivery's wire form.
func FormatDeliveryID(raw uuid.UUID) string {
	id, err := typeid.FromUUID[DeliveryID](raw.String())
	if err != nil {
		return ""
	}
	return id.String()
}

// FormatAttemptID renders the attempt's wire form.
func FormatAttemptID(raw uuid.UUID) string {
	id, err := typeid.FromUUID[AttemptID](raw.String())
	if err != nil {
		return ""
	}
	return id.String()
}

// EndpointSchema is one row of entity.TableWebhookEndpoints. The secret lives sealed: its
// plaintext exists in exactly one response (the create and the rotation),
// and a nil column is an endpoint that predates a signing requirement.
type EndpointSchema struct {
	ID         uuid.UUID
	Name       string
	Descr      *string
	Endpoint   *string
	Method     string
	Headers    []byte // JSONB; nil and empty both mean no custom headers
	Enabled    bool
	SecretEnc  *string
	EventTypes []string
	CreatedAt  time.Time
	UpdatedAt  *time.Time
}

// DeliverySchema is one row of entity.TableWebhookDeliveries. The body is the exact canonical
// bytes every attempt signs and sends; a retry never re-encodes it.
type DeliverySchema struct {
	ID           uuid.UUID
	WebhookID    *uuid.UUID
	Event        string
	Body         []byte
	Status       string
	AttemptCount int
	CreatedAt    time.Time
	DeliveredAt  *time.Time
}

// AttemptSchema is one row of entity.TableWebhookDeliveryAttempts: one try's redacted record. The
// response body is never stored — the metadata an operator needs is.
type AttemptSchema struct {
	ID             uuid.UUID
	DeliveryID     uuid.UUID
	AttemptNumber  int
	ResponseStatus *int
	Error          *string
	DurationMS     *int
	Response       []byte // JSONB; nil means the attempt carries no metadata
	CreatedAt      time.Time
}

// CustomHeaders decodes the endpoint's custom headers. A malformed or absent
// set is no set: the column is written by this package alone, so a decode
// failure is a corrupt row, and an endpoint that cannot state its headers
// cannot carry extra ones.
func (e EndpointSchema) CustomHeaders() map[string]string {
	if len(e.Headers) == 0 {
		return nil
	}
	headers := map[string]string{}
	if err := json.Unmarshal(e.Headers, &headers); err != nil {
		return nil
	}
	return headers
}
