package multifactor

// The second factor: TOTP authenticators enrolled per device, the recovery
// codes that stand in when every device is out of reach, and the pending
// bridge a password success writes between the first factor and the full
// session.
//
// The secret's sealing uses the same AES-256-GCM cipher the configuration's
// secret key backs — the one cipher the codebase has, per the crypto
// package's contract. A secret leaves the server exactly once, in the
// enrollment's answer; every later read answers metadata only.

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"uuid"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/pkg/crypto"
	"go.jetify.com/typeid"
)

// The fixed parameters the enrollments carry. The service decides them rather
// than the caller: SHA1 is the HMAC the RFC 6238 reference implementation
// uses and every authenticator app supports, and the six-digit thirty-second
// step is what every QR code a user has ever scanned means. Widening the
// choice trades compatibility for a knob nobody asked for.
const (
	defaultDigits    = 6
	defaultPeriod    = 30
	defaultAlgorithm = AlgorithmSHA1

	// recoveryCodeCount is the size of one recovery set.
	recoveryCodeCount = 10

	// recoveryCodeBytes is the entropy one code carries: twenty bytes over
	// a 32-symbol alphabet is 100 bits, and the grouping makes it typeable.
	recoveryCodeBytes = 10

	// pendingTTL is how long the password success's bridge stays spendable.
	// Five minutes covers a user opening their authenticator app; anything
	// longer is an attack window that pays nothing.
	pendingTTL = 5 * time.Minute

	// enrollTTL is how long an unconfirmed enrollment may sit in the table.
	// A ceremony that never finishes leaves its sealed secret to the sweeper.
	enrollTTL = 15 * time.Minute

	// maxEnrollments caps how many authenticators one account may hold,
	// confirmed and pending together. It is a table-health bound, not a
	// product limit: no user runs a dozen apps against one account.
	maxEnrollments = 10

	// maxAttempts is how many wrong codes one pending bridge survives before
	// the bridge dies. A fresh bridge costs a verified password, so guessing
	// the factor means re-running the first factor for every three guesses.
	maxAttempts = 3
)

// The failures a second factor reports. The handler maps them to connect
// codes; the service keeps the semantics.
var (
	// ErrCodeInvalid is a wrong second factor. The answer is the same for a
	// wrong TOTP code, a spent recovery code, and a code for an enrollment
	// the caller does not hold: the protocol's uniform not-found, applied
	// to proofs.
	ErrCodeInvalid = errors.New("multifactor: the code is not valid")

	// ErrPendingInvalid is a bridge that is absent, expired, or already spent.
	ErrPendingInvalid = errors.New("multifactor: the pending token is not valid")

	// ErrPendingExhausted is a bridge whose failure budget ran out.
	ErrPendingExhausted = errors.New("multifactor: the pending token is exhausted")

	// ErrEnrollmentNotFound is an enrollment the account does not hold.
	ErrEnrollmentNotFound = errors.New("multifactor: the enrollment is not found")

	// ErrEnrollmentLimit is an account at its authenticator cap.
	ErrEnrollmentLimit = errors.New("multifactor: the enrollment limit is reached")

	// ErrEnrollmentExpired is an unconfirmed enrollment past its ceremony window.
	ErrEnrollmentExpired = errors.New("multifactor: the enrollment has expired")

	// ErrNotConfirmed is a proof asked of an account holding no confirmed factor.
	ErrNotConfirmed = errors.New("multifactor: no confirmed authenticator is enrolled")

	// ErrProofRequired is a destructive procedure called without the code its
	// confirmation needs.
	ErrProofRequired = errors.New("multifactor: the confirmation code is required")

	// ErrNoRecoveryCodes is a regenerate or a recovery challenge answered by an
	// account that holds no recovery set.
	ErrNoRecoveryCodes = errors.New("multifactor: no recovery codes are enrolled")

	// ErrUserNotFound is a target account the issuer does not know — the
	// administrative disable's not-found, the same refusal an unfindable
	// enrollment answers.
	ErrUserNotFound = errors.New("multifactor: the account is not found")
)

// Service is the second factor's logic over its tables.
type Service struct {
	pool       *datastore.Postgres
	repo       *Repository
	cipher     cryptoCipher
	issuer     signinIssuer
	audit      *audit.Recorder
	log        *slog.Logger
	issuerName string
	now        func() time.Time
	notices    noticeEnqueuer
	// passkeys is the post-construction seam to the passkey feature's
	// assertion verification. Nil keeps the code-only challenge.
	passkeys PasskeyVerifier
	// exposeSecrets carries the development aid the listing honors; the
	// wiring passes it from the configuration's validated flag.
	exposeSecrets bool
}

// cryptoCipher is the sealing the service needs. The concrete type is the
// crypto package's Cipher; the interface keeps a test able to stand in.
type cryptoCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(encoded string) (string, error)
}

// signinIssuer is the session opening the completed sign-in runs through —
// the sign-in service itself, so the session rules live in one place.
type signinIssuer interface {
	IssueSession(ctx context.Context, db datastore.Querier, account *signin.Account, provider, event string, params signin.SessionParams) (signin.Result, error)
	FindAccountByID(ctx context.Context, id uuid.UUID) (*signin.Account, error)
}

// NewService builds the service. A nil cipher fails every sealing on use —
// the state a unit test that never enrolls is in — and a nil recorder writes
// no audit rows, both answered at the call site. The log is reserved for the
// unreadable-hash class of report; a service without one discards.
func NewService(pool *datastore.Postgres, cipher cryptoCipher, issuer signinIssuer, recorder *audit.Recorder, issuerName string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:       pool,
		repo:       NewRepository(),
		cipher:     cipher,
		issuer:     issuer,
		audit:      recorder,
		issuerName: issuerName,
		log:        log,
		now:        time.Now,
	}
}

// ---- The sign-in gate ----

// GateSignIn mints the pending bridge the verified password hands over. It
// is the sign-in package's seam: the fork there asks whether the account owes
// a challenge and calls this when it does. The write runs outside a
// transaction on purpose — the bridge is worthless without the password
// success that minted it, and that success carries no row.
func (s *Service) GateSignIn(ctx context.Context, userID uuid.UUID, remember bool) (signin.PendingSignIn, error) {
	outcome, err := s.BeginSignIn(ctx, userID, remember)
	if err != nil {
		return signin.PendingSignIn{}, err
	}
	return signin.PendingSignIn{Token: outcome.PendingToken, ExpiresAt: outcome.ExpiresAt}, nil
}

// KeepsConfirmedFactor answers whether the account holds a confirmed
// authenticator — the question the sign-in's fork runs on.
func (s *Service) KeepsConfirmedFactor(ctx context.Context, userID uuid.UUID) (bool, error) {
	count, err := s.repo.CountConfirmedTotp(ctx, s.pool, userID)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ---- internals ----

// ownedEnrollment reads the enrollment the identifier names and refuses the
// one another account holds — the same not-found either way.
func (s *Service) ownedEnrollment(ctx context.Context, userID uuid.UUID, totpID string) (TotpSchema, error) {
	id, err := uuid.Parse(rawUUIDFromWire(totpID))
	if err != nil {
		return TotpSchema{}, ErrEnrollmentNotFound
	}
	row, err := s.repo.GetTotp(ctx, s.pool, id)
	if errors.Is(err, datastore.ErrNoRows) {
		return TotpSchema{}, ErrEnrollmentNotFound
	}
	if err != nil {
		return TotpSchema{}, err
	}
	if row.UserID != userID {
		return TotpSchema{}, ErrEnrollmentNotFound
	}
	return row, nil
}

// recordRefusal writes the audit row a refused ceremony answers. A refusal
// is recorded where the success is: the attempt's pattern is what a reader
// of the log is looking for.
func (s *Service) recordRefusal(ctx context.Context, userID uuid.UUID, event string, id uuid.UUID, reason string) {
	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  event,
		Status: audit.StatusFailed,
		UserID: userID.String(),
		Payload: map[string]string{
			"totp_id": totpIDString(id),
			"reason":  reason,
		},
	})
}

// seal encrypts the secret for the row.
func (s *Service) seal(secret string) (string, error) {
	if s.cipher == nil {
		return "", errors.New("multifactor: no cipher is configured")
	}
	sealed, err := s.cipher.Encrypt(secret)
	if err != nil {
		return "", fmt.Errorf("multifactor: seal: %w", err)
	}
	return sealed, nil
}

// unseal decrypts the row's secret for a verify.
func (s *Service) unseal(sealed string) (string, error) {
	if s.cipher == nil {
		return "", errors.New("multifactor: no cipher is configured")
	}
	secret, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return "", fmt.Errorf("multifactor: unseal: %w", err)
	}
	return secret, nil
}

// verifyCode answers whether the code matches the secret now, within window
// steps of drift. It is the proof path: no step is recorded.
func verifyCode(secret, code string, period int, now time.Time, window int) bool {
	_, ok := verifyCodeStep(secret, code, period, now, window)
	return ok
}

// verifyCodeStep is verifyCode that also answers the time step the code
// matched, which the challenge path records to refuse its replay.
func verifyCodeStep(secret, code string, period int, now time.Time, window int) (int64, bool) {
	// The comparison is constant-time per candidate step: pquerna's Validate
	// runs subtle.ConstantTimeCompare, and this wrapper adds the step answer
	// the replay guard needs.
	step := now.Unix() / int64(period)
	for candidate := step - int64(window); candidate <= step+int64(window); candidate++ {
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(candidate*int64(period), 0), totp.ValidateOpts{
			Period:    uint(period),
			Skew:      0,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return candidate, true
		}
	}
	return 0, false
}

// isRecoveryShape answers whether the code is the recovery form — grouped
// alphanumeric blocks — rather than the six-to-eight digits a TOTP renders.
func isRecoveryShape(code string) bool {
	return strings.ContainsRune(code, '-')
}

// generateRecoveryCodes answers count codes in the form xxxx-xxxx-xxxx:
// twenty Base32 letters split by hyphens, an alphabet without 0/O and 1/I
// because every confusion a human reads is a lockout.
func generateRecoveryCodes(count int) ([]string, error) {
	out := make([]string, count)
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	for i := range count {
		chars, err := crypto.RandomString(recoveryCodeBytes*2, alphabet)
		if err != nil {
			return nil, fmt.Errorf("multifactor: recovery code: %w", err)
		}
		var b strings.Builder
		for j := range recoveryCodeBytes * 2 {
			b.WriteByte(chars[j])
			if j%4 == 3 && j < recoveryCodeBytes*2-1 {
				b.WriteByte('-')
			}
		}
		out[i] = b.String()
	}
	return out, nil
}

// totpIDString renders the row's UUID in its wire form.
func totpIDString(raw uuid.UUID) string {
	id, err := typeid.FromUUID[TotpID](raw.String())
	if err != nil {
		return ""
	}
	return id.String()
}

// rawUUIDFromWire parses the wire form back to the row's UUID. An identifier
// that is not the enrollment shape answers empty, which the caller's parse
// refuses as not-found — the contract's disclosure rule.
func rawUUIDFromWire(wire string) string {
	if id, err := typeid.Parse[TotpID](wire); err == nil {
		return id.UUID()
	}
	return wire
}
