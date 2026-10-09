package onetimeaccess

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/jobs"
	"github.com/riipandi/saka/modules/identity/signin"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
)

// maxAttempts is how many wrong device pairs one token survives before the
// token dies. The budget rides the row, so every replica judges the same
// token the same way.
const maxAttempts = 5

// resendCooldown is how long the last issued code keeps a fresh email send
// out. An impatient caller — or a retry the network answered twice — holds
// the send instead of landing a second message on the same address, the
// same window the password recovery's resend keeps. The row stamps the send
// time on every issue, direct hand-off included, so the window reads the
// one fact both paths write.
const resendCooldown = time.Minute

// The failures the flow reports. The handler maps them to connect codes, so
// the wire form of a refusal lives with the transport, not here.
var (
	// ErrUserNotFound is an administrative request whose identifier names no
	// account.
	ErrUserNotFound = errors.New("onetimeaccess: account not found")

	// ErrFeatureDisabled is an email request the deployment's configuration
	// has switched off. The administrative and the public path switch
	// separately.
	ErrFeatureDisabled = errors.New("onetimeaccess: the email path is disabled")

	// ErrMailUnavailable is a request the running process cannot serve: no
	// SMTP host is configured, so enqueuing would only dead-letter a task.
	ErrMailUnavailable = errors.New("onetimeaccess: mailer is not configured")

	// ErrQueueUnavailable is a request the running process cannot serve: no
	// queue client is configured, so the message has nowhere to wait.
	ErrQueueUnavailable = errors.New("onetimeaccess: queue is not configured")

	// ErrTokenInvalid covers an unknown, expired, and spent code: answering
	// differently would tell a caller which half was wrong.
	ErrTokenInvalid = errors.New("onetimeaccess: the access code is invalid or expired")

	// ErrDeviceMismatch is an exchange whose device token does not match the
	// one the email request answered. The code stays spendable: the mistake
	// is the caller's, not the code's.
	ErrDeviceMismatch = errors.New("onetimeaccess: the device token does not match")

	// ErrResendTooSoon is an administrative email request inside the cooldown
	// the last issued code stamped. The public path never reports it — a
	// distinct refusal there would tell the caller the address is real —
	// it answers the same generic success and simply holds the send.
	ErrResendTooSoon = errors.New("onetimeaccess: an access code email was sent recently")
)

const (
	// defaultTTL is the window a request without one asks for: fifteen
	// minutes, the same window the public email path fixes.
	defaultTTL = 15 * time.Minute

	// shortCodeWindow is the boundary between the two code forms. A code that
	// lives fifteen minutes or less is six characters — short enough to type
	// from a phone reading the message — and anything longer is twelve,
	// because a longer-lived credential buys its entropy back.
	shortCodeWindow = 15 * time.Minute

	// shortCodeLength and longCodeLength are the two code forms.
	shortCodeLength = 6
	longCodeLength  = 12

	// deviceTokenLength is the entropy of the device token the public email
	// request answers with. It is never typed by a human — the frontend holds
	// it — and it is worthless without the code, which travels by email
	// alone.
	deviceTokenLength = 16
)

// Service issues and consumes the codes that sign an account in without its
// password.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// signin is the issuer the exchange opens the session through. The
	// session row, the last-login stamp, and the audit record are the
	// sign-in feature's rules; the exchange contributes the code it consumed
	// into the same transaction.
	signin *signin.Service
	audit  *fwaudit.Recorder
	mail   *fwmailer.Service
	queue  *queue.Client
	// baseURL is the origin the email's link is built against.
	baseURL string
	// emailAsAdminEnabled and emailAsUnauthenticatedEnabled are the two
	// switches the deployment's configuration holds, read once at
	// construction: a configuration change is a restart, and a procedure
	// whose answer depends on a flag should not watch it move mid-request.
	emailAsAdminEnabled           bool
	emailAsUnauthenticatedEnabled bool
	log                           *slog.Logger
	now                           func() time.Time
}

// NewService builds the service. The mailer and the queue are the
// infrastructure the composition root resolves; the signin service is the
// issuer the exchange opens the session through.
func NewService(cfg config.Config, pool *datastore.Postgres, issuer *signin.Service, recorder *fwaudit.Recorder, mail *fwmailer.Service, client *queue.Client, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:                          pool,
		repo:                          NewRepository(),
		signin:                        issuer,
		audit:                         recorder,
		mail:                          mail,
		queue:                         client,
		baseURL:                       cfg.App.BaseURL,
		emailAsAdminEnabled:           cfg.Auth.OneTimeAccessEmailAsAdminEnabled,
		emailAsUnauthenticatedEnabled: cfg.Auth.OneTimeAccessEmailAsUnauthenticatedEnabled,
		log:                           log,
		now:                           time.Now,
	}
}

// CreateToken issues a code for one account, for an administrator to hand
// over. The code exists in the return value alone: only its hash is stored,
// so it cannot be read back, and the caller is the last one to see it.
func (s *Service) CreateToken(ctx context.Context, userID string, ttlSeconds int32) (string, time.Time, error) {
	account, err := s.accountByID(ctx, userID)
	if err != nil {
		return "", time.Time{}, err
	}
	ttl := ttlOr(ttlSeconds)

	code, err := generateCode(ttl)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("onetimeaccess: code: %w", err)
	}
	expiresAt := s.now().Add(ttl)
	if err := s.repo.UpsertToken(ctx, s.pool, account.ID, crypto.HashHexToken(code), nil, expiresAt, s.now()); err != nil {
		return "", time.Time{}, err
	}
	return code, expiresAt, nil
}

// Exchange consumes a code and signs its holder in.
//
// The exchange deliberately runs one factor, not two: the email code is the
// recovery path an account walks when its password is the thing it cannot
// produce, and a second factor demanded beside it would turn a forgotten
// password into a locked-out account. The compensating controls are the
// code's own shape — six characters of a 58-symbol alphabet, bound to the
// device pair it was issued beside, spendable once, alive for the TTL, and
// ended by a wrong-device budget — plus the anti-enumeration every
// unauthenticated procedure here keeps.
//
// The whole exchange is one transaction: the code's spend, the session it
// opens, the last-login stamp, and the audit record commit together, so a
// rollback returns the code — a holder whose sign-in failed halfway can try
// again within the window, and a log never describes a session that did not
// open. A device token the code was issued beside must come back exact; a
// mismatch leaves the code spendable, because the caller's mistake is not the
// code's spend.
func (s *Service) Exchange(ctx context.Context, rawCode, deviceToken string, client fwaudit.ClientInfo) (signin.Result, error) {
	hash := crypto.HashHexToken(rawCode)
	var result signin.Result
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, err := s.repo.FindTokenByHash(ctx, tx, hash)
		if errors.Is(err, datastore.ErrNoRows) {
			return ErrTokenInvalid
		}
		if err != nil {
			return err
		}
		if !row.ExpiresAt.After(s.now()) {
			return ErrTokenInvalid
		}
		if row.DeviceToken != nil && subtle.ConstantTimeCompare([]byte(*row.DeviceToken), []byte(deviceToken)) != 1 {
			// The device pair failed: the wrong guess rides the row, and the
			// guess that fills the budget ends the token — a device-bound
			// code that keeps failing is one a third party is holding. The
			// delete carries the hash this caller resolved, so a code a
			// re-issue replaced is not swept by the stale read.
			live, budgetErr := s.repo.RegisterWrongAttempt(ctx, tx, row.ID, maxAttempts)
			if budgetErr != nil {
				return budgetErr
			}
			if !live {
				if _, delErr := s.repo.DeleteToken(ctx, tx, row.ID, hash, PurposeOneTimeAccess); delErr != nil {
					return delErr
				}
			}
			return ErrDeviceMismatch
		}

		spent, err := s.repo.DeleteToken(ctx, tx, row.ID, hash, PurposeOneTimeAccess)
		if err != nil {
			return err
		}
		if spent == 0 {
			// Another exchange reached the delete first, or a re-issue
			// replaced the row this caller read: the code is spent, and this
			// caller answers the same refusal an unknown one does.
			return ErrTokenInvalid
		}

		account, err := s.repo.FindAccountByID(ctx, tx, row.UserID)
		if errors.Is(err, datastore.ErrNoRows) {
			// The account the code named is gone. The code is already
			// spent, which is the right outcome — a code for an account
			// that no longer exists must not survive it.
			return ErrTokenInvalid
		}
		if err != nil {
			return err
		}

		// The second factor's fork runs before any session is opened, the
		// same question the password path asks: a code proves the mailbox,
		// and an account keeping a confirmed factor answers the challenge
		// before its session opens. The bridge mints inside this
		// transaction, so a rollback returns the code — a retry starts
		// clean.
		pending, owed, forkErr := s.signin.Challenge(ctx, account.ID, false)
		if forkErr != nil {
			return forkErr
		}
		if owed {
			result = signin.Result{
				MFARequired:         true,
				MFAPendingToken:     pending.Token,
				MFAPendingExpiresAt: pending.ExpiresAt,
			}
			return nil
		}

		result, err = s.signin.IssueSession(ctx, tx, &signin.Account{
			ID:              account.ID,
			Username:        account.Username,
			Email:           account.Email,
			DisplayName:     account.DisplayName,
			Disabled:        account.Disabled,
			RestrictionKind: account.RestrictionKind,
		}, signin.ProviderOneTimeAccess, audit.EventOneTimeAccessSignIn, signin.SessionParams{
			UserAgent:   client.UserAgent,
			IPAddress:   client.IPAddress,
			Fingerprint: client.Fingerprint,
		})
		return err
	})
	if err != nil {
		return signin.Result{}, err
	}
	return result, nil
}

// RequestEmailAsAdmin sends a code to one account's address, for an
// administrator whose target cannot reach the sign-in page. The code never
// passes through the caller: it travels by email alone, so the operator asks
// for a sign-in for the account rather than receives a credential for it.
func (s *Service) RequestEmailAsAdmin(ctx context.Context, userID string, ttlSeconds int32) error {
	if !s.emailAsAdminEnabled {
		return ErrFeatureDisabled
	}
	account, err := s.accountByID(ctx, userID)
	if err != nil {
		return err
	}
	// The cooldown is a visible refusal on the administrative path: the
	// administrator knows the account is real, and holding the send silently
	// would read as success while the older code went on standing. An
	// impatient operator must not turn into a mailbomb either.
	if inside, waitErr := s.resendOnCooldown(ctx, account.ID); waitErr != nil {
		return waitErr
	} else if inside {
		return ErrResendTooSoon
	}
	return s.issueEmailCode(ctx, account, ttlOr(ttlSeconds), nil)
}

// RequestEmail sends a code to the address the caller names, from the sign-in
// page.
//
// An address no account holds answers the same success a known one does — the
// code simply has nowhere to go — so the procedure cannot tell a caller which
// addresses exist. The device token the answer carries is real either way,
// because the shape of the answer is the only thing the caller reads.
func (s *Service) RequestEmail(ctx context.Context, email string) (string, error) {
	if !s.emailAsUnauthenticatedEnabled {
		return "", ErrFeatureDisabled
	}

	deviceToken, err := generateDeviceToken()
	if err != nil {
		return "", fmt.Errorf("onetimeaccess: device token: %w", err)
	}

	account, err := s.repo.FindAccountByEmail(ctx, s.pool, email)
	if errors.Is(err, datastore.ErrNoRows) {
		// The refusal that names nothing: the address is unknown, and the
		// answer says success.
		return deviceToken, nil
	}
	if err != nil {
		return "", err
	}
	// A request inside the cooldown holds the send and answers the same
	// generic success, decoy device token and all: a distinct refusal would
	// tell the caller the address is real and freshly targeted, which is the
	// one bit every other path here refuses to give. The code the earlier
	// request sent is still standing, so the account loses nothing.
	if inside, waitErr := s.resendOnCooldown(ctx, account.ID); waitErr != nil {
		return "", waitErr
	} else if inside {
		return deviceToken, nil
	}
	if err := s.issueEmailCode(ctx, account, defaultTTL, &deviceToken); err != nil {
		return "", err
	}
	return deviceToken, nil
}

// resendOnCooldown reports whether an account's last issued code is still
// inside the resend window. An account that carries no row has never been
// issued one, and a row without a stamp predates the window's own record.
func (s *Service) resendOnCooldown(ctx context.Context, userID uuid.UUID) (bool, error) {
	row, err := s.repo.FindTokenByUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.LastSentAt == nil {
		return false, nil
	}
	return s.now().Before(row.LastSentAt.Add(resendCooldown)), nil
}

// issueEmailCode issues the code, records it beside the device token the
// caller holds — nil for the administrative send, which pairs none — and
// hands the message to the queue. The retry schedule the task carries is
// tighter than the verification email's, because a code that outlives its own
// delivery is not a convenience, it is a hole.
func (s *Service) issueEmailCode(ctx context.Context, account Account, ttl time.Duration, deviceToken *string) error {
	if !s.mail.Configured() {
		return ErrMailUnavailable
	}
	if s.queue == nil {
		return ErrQueueUnavailable
	}

	code, err := generateCode(ttl)
	if err != nil {
		return fmt.Errorf("onetimeaccess: code: %w", err)
	}
	now := s.now()
	if err := s.repo.UpsertToken(ctx, s.pool, account.ID, crypto.HashHexToken(code), deviceToken, now.Add(ttl), now); err != nil {
		return err
	}
	if _, err := s.queue.Add(jobs.OneTimeAccessEmailTask{
		UserID:      account.ID.String(),
		Email:       account.Email,
		DisplayName: account.DisplayName,
		Token:       code,
		TTLSeconds:  int64(ttl.Seconds()),
	}).Save(); err != nil {
		return fmt.Errorf("onetimeaccess: enqueue: %w", err)
	}

	// The message is enqueued and the record is written after the queue
	// accepted it, on the pool rather than a transaction: the enqueue is
	// already committed, and a record inside a transaction that rolled back
	// after it would understate what happened.
	s.audit.Record(ctx, s.pool, fwaudit.Entry{
		Event:  audit.EventOneTimeAccessEmailSent,
		Status: fwaudit.StatusSuccess,
		UserID: account.ID.String(),
		Payload: map[string]string{
			"email": account.Email,
		},
	})
	s.log.InfoContext(ctx, "onetimeaccess: access code emailed",
		slog.String("user_id", account.ID.String()), slog.Duration("ttl", ttl))
	return nil
}

// accountByID reads the account an administrative request names.
func (s *Service) accountByID(ctx context.Context, userID string) (Account, error) {
	wire, err := user.ParseID(userID)
	if err != nil {
		return Account{}, ErrUserNotFound
	}
	id := user.IDToUUID(wire)
	account, err := s.repo.FindAccountByID(ctx, s.pool, id)
	if errors.Is(err, datastore.ErrNoRows) {
		return Account{}, ErrUserNotFound
	}
	if err != nil {
		return Account{}, err
	}
	return account, nil
}

// ttlOr answers the window the request carried, or the default when it
// carried none.
func ttlOr(ttlSeconds int32) time.Duration {
	if ttlSeconds <= 0 {
		return defaultTTL
	}
	return time.Duration(ttlSeconds) * time.Second
}

// generateCode draws a code from the unambiguous alphabet, six characters for
// a code that lives fifteen minutes or less and twelve for anything longer.
func generateCode(ttl time.Duration) (string, error) {
	length := longCodeLength
	if ttl <= shortCodeWindow {
		length = shortCodeLength
	}
	return crypto.RandomString(length, crypto.AlphabetUnambiguous)
}

// generateDeviceToken draws the device token the public email request answers
// with.
func generateDeviceToken() (string, error) {
	return crypto.RandomString(deviceTokenLength, crypto.AlphabetAlphanumeric)
}
