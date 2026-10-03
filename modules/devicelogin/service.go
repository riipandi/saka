package devicelogin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// userCodeCharset excludes the glyphs a printed code confuses — 0/O and
// 1/I/L — and the code renders as XXXX-XXXX, the hyphen a reading aid
// the comparisons strip.
const userCodeCharset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// ErrCodeUnknown is the one refusal every pairing surface answers: an
// unknown, expired, or already decided request never says which.
var ErrCodeUnknown = errors.New("devicelogin: the request is unknown, expired, or already decided")

// ErrTooManyPendingRequests is a creation beyond the pairing cap: one
// browser holds at most maxPendingRequests live requests, so a page
// cannot fill the table with rows nothing will ever decide.
var ErrTooManyPendingRequests = errors.New("devicelogin: the pairing cap for this browser is reached")

// Service is the pairing surface: a browser that cannot sign itself in
// creates a request, another browser approves it, and the creating
// browser's long poll answers the decision.
type Service struct {
	repo    *Repository
	users   *user.Service
	audit   *audit.Recorder
	baseURL string
}

func NewService(pool *datastore.Postgres, users *user.Service, recorder *audit.Recorder, baseURL string) *Service {
	return &Service{
		repo:    NewRepository(pool),
		users:   users,
		audit:   recorder,
		baseURL: baseURL,
	}
}

// CreatedView is the create answer: everything the device page renders
// and the poll rhythm follows. The device token is shown once — it
// rides the pairing cookie and never travels the body.
type CreatedView struct {
	ID                      string    `json:"id"`
	UserCode                string    `json:"user_code"`
	VerificationURI         string    `json:"verification_uri"`
	VerificationURIComplete string    `json:"verification_uri_complete"`
	ExpiresAt               time.Time `json:"expires_at"`
	Interval                int       `json:"interval"`
}

// Create opens one pairing request. The creating browser's IP and agent
// ride the row — the approving device renders them back before the
// decision, so the human sees what they are approving.
func (s *Service) Create(ctx context.Context, deviceToken, ipAddress, userAgent string) (CreatedView, error) {
	code, err := newUserCode()
	if err != nil {
		return CreatedView{}, fmt.Errorf("devicelogin: generate user code: %w", err)
	}

	req, err := s.repo.CreateWithinLimit(ctx, HashUserCode(code), HashDeviceToken(deviceToken), ipAddress, userAgent, time.Now().UTC().Add(requestLifetime), maxPendingRequests)
	if err != nil {
		return CreatedView{}, fmt.Errorf("devicelogin: create request: %w", err)
	}

	complete := s.baseURL + "/device?user_code=" + code
	return CreatedView{
		ID:                      req.ID.String(),
		UserCode:                FormatUserCode(code),
		VerificationURI:         s.baseURL + "/device",
		VerificationURIComplete: complete,
		ExpiresAt:               req.ExpiresAt,
		Interval:                pollInterval,
	}, nil
}

// Inspection is what the approving device reads before it decides: the
// request's own facts, rendered for a human's yes-or-no.
type Inspection struct {
	UserCode  string    `json:"user_code"`
	IPAddress string    `json:"ip_address"`
	UserAgent string    `json:"user_agent"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Inspect answers the request the presented code names. A code no live
// request holds is the unknown refusal — the answer never says which
// half failed.
func (s *Service) Inspect(ctx context.Context, code string) (Inspection, error) {
	req, err := s.repo.ByUserCode(ctx, HashUserCode(NormalizeUserCode(code)))
	if err != nil {
		return Inspection{}, ErrCodeUnknown
	}
	return Inspection{
		UserCode:  FormatUserCode(NormalizeUserCode(code)),
		IPAddress: req.IPAddress,
		UserAgent: req.UserAgent,
		ExpiresAt: req.ExpiresAt,
	}, nil
}

// Decide stamps the answer. It answers whether the request was live and
// undecided — a repeat decision is the same not-found, because a
// request decided once is decided forever. The caller names the account
// deciding: its wire-form id rides the audit record.
func (s *Service) Decide(ctx context.Context, code string, decision Decision, caller *jwtutils.Caller) error {
	normalized := NormalizeUserCode(code)
	req, err := s.repo.ByUserCode(ctx, HashUserCode(normalized))
	if err != nil {
		return ErrCodeUnknown
	}

	var approver *string
	if decision == DecisionApprove {
		// The row's user_id is a raw UUID column, so the wire form
		// converts at the boundary; a subject that cannot parse is the
		// guard's refusal, never a row.
		id, convErr := user.UUIDFromWire(caller.UserID)
		if convErr != nil {
			return ErrCodeUnknown
		}
		uuid := id.String()
		approver = &uuid
	}
	decided, err := s.repo.Decide(ctx, HashUserCode(normalized), approver, decision)
	if err != nil {
		return fmt.Errorf("devicelogin: decide: %w", err)
	}
	if !decided {
		return ErrCodeUnknown
	}

	event := audit.EventDeviceLoginDenied
	if decision == DecisionApprove {
		event = audit.EventDeviceLoginApproved
	}
	// The audit user_id is a raw UUID column, so the wire form converts
	// back; a caller whose subject cannot parse is refused before this
	// line by the guard's bearer middleware.
	approverUUID, _ := user.UUIDFromWire(caller.UserID)
	s.audit.Record(ctx, s.repo.pool, audit.Entry{
		Event:  event,
		Status: audit.StatusSuccess,
		UserID: approverUUID.String(),
		Payload: map[string]string{
			"request_id": req.ID.String(),
		},
	})
	return nil
}

// ExchangeOutcome is what one long poll turn answered.
type ExchangeOutcome struct {
	Status ExchangeStatus
	UserID string
}

// ExchangeStatus is the shape the REST exchange answers with: the poll
// may legitimately end with nothing, and the device distinguishes that
// from a refusal.
type ExchangeStatus string

const (
	ExchangePending ExchangeStatus = "pending"
	ExchangeDone    ExchangeStatus = "done"
)

// Exchange polls once. The REST handler spins it on a ticker for the
// long-poll window; each turn reads the row's state through the device
// token's hash — the pairing proof — and a past approval consumes the
// row, so of two concurrent exchanges exactly one carries the session.
func (s *Service) Exchange(ctx context.Context, id, deviceToken string) (ExchangeOutcome, error) {
	state, err := s.repo.PollStateByID(ctx, id, HashDeviceToken(deviceToken))
	if err != nil {
		return ExchangeOutcome{}, ErrCodeUnknown
	}

	switch state.Status {
	case StatusPending:
		return ExchangeOutcome{Status: ExchangePending}, nil
	case StatusDenied:
		return ExchangeOutcome{}, ErrCodeUnknown
	case StatusApproved:
		consumed, err := s.repo.Consume(ctx, id)
		if err != nil {
			return ExchangeOutcome{}, fmt.Errorf("devicelogin: consume: %w", err)
		}
		if !consumed {
			return ExchangeOutcome{}, ErrCodeUnknown
		}
		if state.UserID == nil {
			return ExchangeOutcome{}, ErrCodeUnknown
		}
		return ExchangeOutcome{Status: ExchangeDone, UserID: *state.UserID}, nil
	default:
		return ExchangeOutcome{}, ErrCodeUnknown
	}
}

// Account is the exchange's read of the account it signs in: the state
// checks the sign-in flows apply, applied here before the session mints.
type Account struct {
	ID       string
	Username string
}

// LoadAccount resolves and judges the account an approved exchange
// names. The id is the raw UUID the row stored; a disabled or banned
// account is refused like at sign-in. The answer carries the wire form
// the RPC mint reads.
func (s *Service) LoadAccount(ctx context.Context, rawID string) (Account, error) {
	id, err := user.IDFromUUIDString(rawID)
	if err != nil {
		return Account{}, ErrCodeUnknown
	}
	view, err := s.users.GetUser(ctx, id.String())
	if err != nil {
		return Account{}, ErrCodeUnknown
	}
	if view.Disabled || (view.BannedAt != nil && (view.BanExpires == nil || view.BanExpires.After(time.Now()))) {
		return Account{}, ErrCodeUnknown
	}
	return Account{ID: view.ID, Username: view.Username}, nil
}

// newUserCode draws eight unambiguous characters from the crypto source.
func newUserCode() (string, error) {
	out := make([]byte, 8)
	max := big.NewInt(int64(len(userCodeCharset)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = userCodeCharset[n.Int64()]
	}
	return string(out), nil
}

// FormatUserCode renders the code with its reading hyphen.
func FormatUserCode(code string) string {
	if len(code) != 8 {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// NormalizeUserCode strips the reading hyphen and folds the case, so a
// code typed in any rhythm resolves.
func NormalizeUserCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}

// NewDeviceToken draws the pairing secret the creating browser holds.
func NewDeviceToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// HashUserCode is the SHA-256 digest the rows key the presented code by.
func HashUserCode(code string) string {
	return HashDeviceToken(NormalizeUserCode(code))
}

// HashDeviceToken is the SHA-256 digest the rows key the presented
// pairing secret by.
func HashDeviceToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// TokenMatches answers whether the presented pairing secret matches the
// stored hash — the constant-time compare the cookie's value deserves.
func TokenMatches(hash, token string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(HashDeviceToken(token))) == 1
}
