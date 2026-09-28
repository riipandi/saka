package devicelogin

import (
	"time"

	"uuid"
)

// The table the pairing requests live in. The migrations own the schema;
// these constants are how Go code names it, so a rename touches one line.
const requestTable = "public.device_login_requests"

// The pairing lifetime and the exchange rhythm. A request lives five
// minutes, the long poll holds the device for twenty-five seconds before
// answering pending, and the device waits the interval the create answer
// names between polls.
const (
	requestLifetime    = 5 * time.Minute
	longPollDuration   = 25 * time.Second
	pollInterval       = 3 // seconds, named to the device in the create answer
	maxPendingRequests = 8 // live requests one creating browser may hold
)

// RequestStatus is the lifecycle of a pairing request: the creating
// browser polls until the second device decides, and the first exchange
// past the approval consumes the row.
type RequestStatus string

const (
	StatusPending  RequestStatus = "pending"
	StatusApproved RequestStatus = "approved"
	StatusDenied   RequestStatus = "denied"
	StatusConsumed RequestStatus = "consumed"
)

// Request is one row of the pairing table. The raw user code and device
// token are never stored — the caller's hashes are — and the account id
// only fills in when the request is approved.
type Request struct {
	ID        uuid.UUID
	UserID    *uuid.UUID
	Status    RequestStatus
	IPAddress string
	UserAgent string
	CreatedAt time.Time
	ExpiresAt time.Time
	DecidedAt *time.Time
}

// Decision is what the approving device answered with.
type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionDeny    Decision = "deny"
)
