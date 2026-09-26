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
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/signin"
	"go.jetify.com/typeid"
	"uuid"
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
)

// attemptBudget tracks the wrong codes one pending bridge has eaten. The
// budget rides the bridge's life in the service because the table carries no
// column for it — a bridge is a five-minute object, and the budget dying
// with the process is acceptable for the window it guards. The map's key is
// the pending row's identifier.
var attemptBudget = map[uuid.UUID]int{}

// ProvisioningTTL answers the ceremony window the enrollment response
// reports, so a client may render a countdown without reaching into the
// service's constants.
const ProvisioningTTL = enrollTTL

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

// ---- Enrollment ----

// BeginTotpEnrollmentResult is the enrollment's one-time answer: the
// identifier, the Base32 secret, and the provisioning URI.
type BeginTotpEnrollmentResult struct {
	TotpID     string
	Name       string
	Secret     string
	OTPAuthURI string
	ExpiresAt  time.Time
}

// BeginTotpEnrollment writes an unconfirmed authenticator and answers the
// secret. The row is refused a confirm past its window, so a QR code shown
// today is not a credential next week.
func (s *Service) BeginTotpEnrollment(ctx context.Context, userID uuid.UUID, name string) (BeginTotpEnrollmentResult, error) {
	now := s.now()

	count, err := s.repo.ListTotp(ctx, s.pool, userID)
	if err != nil {
		return BeginTotpEnrollmentResult{}, err
	}
	if len(count) >= maxEnrollments {
		return BeginTotpEnrollmentResult{}, ErrEnrollmentLimit
	}

	// The label the authenticator renders — "Tango: user@example.com" — is
	// the account the ceremony is for. An account the issuer cannot read is
	// a caller the guard should have refused, so the failure is internal.
	account, err := s.issuer.FindAccountByID(ctx, userID)
	if err != nil {
		return BeginTotpEnrollmentResult{}, fmt.Errorf("multifactor: enrollment account: %w", err)
	}

	// pquerna's generator produces a 20-byte secret in Base32, the size the
	// authenticator apps' QR readers all assume.
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuerName,
		AccountName: account.Email,
		Period:      defaultPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
		SecretSize:  20,
		Rand:        rand.Reader,
	})
	if err != nil {
		return BeginTotpEnrollmentResult{}, fmt.Errorf("multifactor: generate secret: %w", err)
	}

	sealed, err := s.seal(key.Secret())
	if err != nil {
		return BeginTotpEnrollmentResult{}, err
	}

	id := uuid.NewV7()
	row := TotpSchema{
		ID:        id,
		UserID:    userID,
		Name:      name,
		Secret:    sealed,
		Digits:    defaultDigits,
		Period:    defaultPeriod,
		Algorithm: defaultAlgorithm,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateTotp(ctx, s.pool, row); err != nil {
		return BeginTotpEnrollmentResult{}, err
	}

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventMfaEnrollmentStarted,
		Status: audit.StatusSuccess,
		UserID: userID.String(),
		Payload: map[string]string{
			"totp_id": totpIDString(row.ID),
		},
	})

	return BeginTotpEnrollmentResult{
		TotpID:     totpIDString(row.ID),
		Name:       name,
		Secret:     key.Secret(),
		OTPAuthURI: key.URL(),
		ExpiresAt:  now.Add(enrollTTL),
	}, nil
}

// ConfirmTotpEnrollmentResult is the confirmation's answer.
type ConfirmTotpEnrollmentResult struct {
	TotpID        string
	RecoveryCodes []string
}

// ConfirmTotpEnrollment activates the enrollment once the app's code proves
// the secret, and — on the account's first confirmed authenticator — writes
// the recovery set the answer carries in the clear exactly once.
func (s *Service) ConfirmTotpEnrollment(ctx context.Context, userID uuid.UUID, totpID, code string) (ConfirmTotpEnrollmentResult, error) {
	now := s.now()

	row, err := s.ownedEnrollment(ctx, userID, totpID)
	if err != nil {
		return ConfirmTotpEnrollmentResult{}, err
	}
	if row.ConfirmedAt != nil {
		return ConfirmTotpEnrollmentResult{}, ErrEnrollmentNotFound
	}
	if now.Sub(row.CreatedAt) > enrollTTL {
		return ConfirmTotpEnrollmentResult{}, ErrEnrollmentExpired
	}

	secret, err := s.unseal(row.Secret)
	if err != nil {
		return ConfirmTotpEnrollmentResult{}, err
	}
	if !verifyCode(secret, code, int(row.Period), now, 1) {
		s.recordRefusal(ctx, userID, audit.EventMfaEnrollmentFailed, row.ID, "code_mismatch")
		return ConfirmTotpEnrollmentResult{}, ErrCodeInvalid
	}

	first, err := s.repo.CountConfirmedTotp(ctx, s.pool, userID)
	if err != nil {
		return ConfirmTotpEnrollmentResult{}, err
	}

	// The clear codes cross from the transaction that wrote their hashes to
	// the answer through this variable: WithTx's closure carries nothing out
	// but its error, and the answer must show them exactly once.
	var clearCodes []string

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if confirmErr := s.repo.ConfirmTotp(ctx, tx, row.ID, userID, now); confirmErr != nil {
			return confirmErr
		}
		// The set rides the first confirmation: an account that holds no
		// confirmed factor holds no recovery codes, and the ceremony's one
		// clear answer is where they belong.
		if first == 0 {
			codes, codeErr := generateRecoveryCodes(recoveryCodeCount)
			if codeErr != nil {
				return codeErr
			}
			if writeErr := s.writeRecoverySet(ctx, tx, userID, codes, now); writeErr != nil {
				return writeErr
			}
			clearCodes = codes
		}
		return nil
	})
	if err != nil {
		return ConfirmTotpEnrollmentResult{}, err
	}

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventMfaEnrollmentConfirmed,
		Status: audit.StatusSuccess,
		UserID: userID.String(),
		Payload: map[string]string{
			"totp_id":     totpIDString(row.ID),
			"device_name": row.Name,
		},
	})

	return ConfirmTotpEnrollmentResult{
		TotpID:        totpIDString(row.ID),
		RecoveryCodes: clearCodes,
	}, nil
}

// EnrollmentView is one enrollment's metadata — the shape a settings page
// renders. It never carries a secret.
type EnrollmentView struct {
	TotpID      string
	Name        string
	ConfirmedAt *time.Time
	LastUsedAt  *time.Time
	CreatedAt   time.Time
}

// ListTotpEnrollments answers the account's authenticators.
func (s *Service) ListTotpEnrollments(ctx context.Context, userID uuid.UUID) ([]EnrollmentView, error) {
	rows, err := s.repo.ListTotp(ctx, s.pool, userID)
	if err != nil {
		return nil, err
	}
	out := make([]EnrollmentView, 0, len(rows))
	for _, row := range rows {
		out = append(out, EnrollmentView{
			TotpID:      totpIDString(row.ID),
			Name:        row.Name,
			ConfirmedAt: row.ConfirmedAt,
			LastUsedAt:  row.LastUsedAt,
			CreatedAt:   row.CreatedAt,
		})
	}
	return out, nil
}

// DeleteTotpEnrollment removes one authenticator. A removal that would leave
// the account without a confirmed factor requires the proof the caller still
// holds one — a code from another authenticator or a recovery code.
func (s *Service) DeleteTotpEnrollment(ctx context.Context, userID uuid.UUID, totpID, code string) error {
	row, err := s.ownedEnrollment(ctx, userID, totpID)
	if err != nil {
		return err
	}

	// An unconfirmed row is a mistyped start: no proof, no ceremony, just a
	// delete. Its sealed secret dies with it.
	if row.ConfirmedAt == nil {
		return s.repo.DeleteTotp(ctx, s.pool, row.ID, userID)
	}

	remaining, err := s.repo.CountConfirmedTotp(ctx, s.pool, userID)
	if err != nil {
		return err
	}
	if remaining > 1 {
		// Another confirmed factor stays behind, so the account cannot lock
		// itself out of this removal.
		return s.repo.DeleteTotp(ctx, s.pool, row.ID, userID)
	}

	// The last factor's removal is the disable path's risk: prove the caller
	// holds it or refuse.
	if code == "" {
		return ErrProofRequired
	}
	if err := s.verifyProof(ctx, userID, code); err != nil {
		return err
	}
	if err := s.repo.DeleteTotp(ctx, s.pool, row.ID, userID); err != nil {
		return err
	}
	// The set's device is gone: the recovery codes answer a factor that no
	// longer exists, so they go with it.
	if err := s.repo.DeleteAllRecoveryForUser(ctx, s.pool, userID); err != nil {
		return err
	}

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventMfaDisabled,
		Status: audit.StatusSuccess,
		UserID: userID.String(),
		Payload: map[string]string{
			"totp_id": totpIDString(row.ID),
			"reason":  "last_device_removed",
		},
	})
	return nil
}

// ---- The pending bridge and the challenge ----

// SignInOutcome is what a password success answers when the account keeps a
// confirmed factor. The bridge is opaque to its holder.
type SignInOutcome struct {
	PendingToken string
	UserID       uuid.UUID
	Remember     bool
	ExpiresAt    time.Time
}

// BeginSignIn writes the bridge a verified password mints. The caller is the
// sign-in service, after its credential check and its account-state check —
// the disabled and banned refusals happened before this ran.
func (s *Service) BeginSignIn(ctx context.Context, userID uuid.UUID, remember bool) (SignInOutcome, error) {
	now := s.now()

	token, err := randomToken(32)
	if err != nil {
		return SignInOutcome{}, err
	}
	hash := hashToken(token)

	expires := now.Add(pendingTTL)
	row := PendingSchema{
		ID:        uuid.NewV7(),
		UserID:    userID,
		TokenHash: hash,
		Remember:  remember,
		ExpiresAt: expires,
		CreatedAt: now,
	}
	if err := s.repo.CreatePending(ctx, s.pool, row); err != nil {
		return SignInOutcome{}, err
	}

	return SignInOutcome{
		PendingToken: token,
		UserID:       userID,
		Remember:     remember,
		ExpiresAt:    expires,
	}, nil
}

// CompleteSignInResult is the token pair the finished sign-in answers — the
// sign-in service's own Result, verbatim, so a client treats the two alike.
type CompleteSignInResult = signin.Result

// CompleteSignIn spends the bridge plus the second factor on the session.
// The bridge dies whatever way the call ends: success consumes it, a wrong
// code eats the failure budget, an expired bridge is swept on read.
func (s *Service) CompleteSignIn(ctx context.Context, pendingToken, code string, session signin.SessionParams) (CompleteSignInResult, error) {
	now := s.now()

	hash := hashToken(pendingToken)
	pending, err := s.repo.FindLivePending(ctx, s.pool, hash, now)
	if errors.Is(err, datastore.ErrNoRows) {
		return CompleteSignInResult{}, ErrPendingInvalid
	}
	if err != nil {
		return CompleteSignInResult{}, err
	}

	// The failure budget is checked before any proof runs, so a bridge one
	// guess from death cannot spend a guess it no longer owns.
	if attemptBudget[pending.ID] >= maxAttempts {
		_ = s.repo.DeletePending(ctx, s.pool, pending.ID)
		return CompleteSignInResult{}, ErrPendingExhausted
	}

	if _, verifiedErr := s.verifyChallenge(ctx, pending.UserID, code, now); verifiedErr != nil {
		attemptBudget[pending.ID]++
		if attemptBudget[pending.ID] >= maxAttempts {
			_ = s.repo.DeletePending(ctx, s.pool, pending.ID)
			return CompleteSignInResult{}, ErrPendingExhausted
		}
		return CompleteSignInResult{}, verifiedErr
	}

	account, err := s.issuer.FindAccountByID(ctx, pending.UserID)
	if err != nil {
		return CompleteSignInResult{}, err
	}

	var result CompleteSignInResult
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		// The bridge dies in the transaction the session opens in: a
		// rollback returns the bridge, and the same code cannot open two
		// sessions because the TOTP step check refuses its replay.
		if deleteErr := s.repo.DeletePending(ctx, tx, pending.ID); deleteErr != nil {
			return deleteErr
		}
		var issueErr error
		result, issueErr = s.issuer.IssueSession(ctx, tx, account, signin.ProviderTOTP, audit.EventMfaSignIn, signin.SessionParams{
			UserAgent:   session.UserAgent,
			IPAddress:   session.IPAddress,
			Fingerprint: session.Fingerprint,
			Remember:    pending.Remember,
		})
		return issueErr
	})
	if err != nil {
		return CompleteSignInResult{}, err
	}

	delete(attemptBudget, pending.ID)
	return result, nil
}

// verifyChallenge answers whether the code is the account's second factor: a
// TOTP code from any confirmed authenticator, or one unused recovery code.
// It marks what it consumes.
func (s *Service) verifyChallenge(ctx context.Context, userID uuid.UUID, code string, now time.Time) (bool, error) {
	// The recovery shape is longer than any TOTP code and carries hyphens;
	// one look at the shape picks the table, and the lookup is cheap enough
	// that trying both would also be fine.
	if isRecoveryShape(code) {
		consumed, err := s.consumeRecoveryCode(ctx, userID, code, now)
		if err != nil {
			return false, err
		}
		if !consumed {
			return false, ErrCodeInvalid
		}
		return true, nil
	}

	confirmed, err := s.repo.ListTotp(ctx, s.pool, userID)
	if err != nil {
		return false, err
	}
	// The shape check already refused the empty code; an account holding no
	// confirmed factor cannot be answering a challenge at all.
	for _, row := range confirmed {
		if row.ConfirmedAt == nil {
			continue
		}
		secret, err := s.unseal(row.Secret)
		if err != nil {
			return false, err
		}
		if step, ok := verifyCodeStep(secret, code, int(row.Period), now, 1); ok {
			won, err := s.repo.TouchTotpUsage(ctx, s.pool, row.ID, step, now)
			if err != nil {
				return false, err
			}
			if !won {
				// A concurrent challenge spent this step first: the code is
				// honest and already used, which is a refusal all the same.
				return false, ErrCodeInvalid
			}
			return true, nil
		}
	}
	return false, ErrCodeInvalid
}

// ---- The second-factor proof destructive procedures share ----

// verifyProof answers whether the code proves the caller holds a factor. It
// consumes what it accepts — a recovery code is single-use everywhere — but
// never a TOTP step: a proof that rotates under a double-clicked button is
// a support ticket, so proofs replay within their window.
func (s *Service) verifyProof(ctx context.Context, userID uuid.UUID, code string) error {
	now := s.now()
	if isRecoveryShape(code) {
		consumed, err := s.consumeRecoveryCode(ctx, userID, code, now)
		if err != nil {
			return err
		}
		if !consumed {
			return ErrCodeInvalid
		}
		return nil
	}

	rows, err := s.repo.ListTotp(ctx, s.pool, userID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ConfirmedAt == nil {
			continue
		}
		secret, err := s.unseal(row.Secret)
		if err != nil {
			return err
		}
		if verifyCode(secret, code, int(row.Period), now, 1) {
			return nil
		}
	}
	return ErrCodeInvalid
}

// ---- Recovery codes ----

// RecoveryStatus is the set's state, the numbers a settings page renders.
type RecoveryStatus struct {
	Total  int
	Unused int
}

// RecoveryCodesStatus answers the set's counts. The values themselves are
// gone from the server the moment their one answer left it.
func (s *Service) RecoveryCodesStatus(ctx context.Context, userID uuid.UUID) (RecoveryStatus, error) {
	total, used, err := s.repo.CountRecoveryCodes(ctx, s.pool, userID)
	if err != nil {
		return RecoveryStatus{}, err
	}
	return RecoveryStatus{Total: total, Unused: total - used}, nil
}

// RegenerateRecoveryCodes rewrites the account's set and answers the fresh
// codes in the clear exactly once. The proof is a factor the caller holds;
// a session alone cannot reset what stands guard over it.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	now := s.now()

	confirmed, countErr := s.repo.CountConfirmedTotp(ctx, s.pool, userID)
	if countErr != nil {
		return nil, countErr
	}
	if confirmed == 0 {
		return nil, ErrNotConfirmed
	}
	if err := s.verifyProof(ctx, userID, code); err != nil {
		return nil, err
	}

	codes, genErr := generateRecoveryCodes(recoveryCodeCount)
	if genErr != nil {
		return nil, genErr
	}
	txErr := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		return s.writeRecoverySet(ctx, tx, userID, codes, now)
	})
	if txErr != nil {
		return nil, txErr
	}
	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventMfaRecoveryRegenerated,
		Status: audit.StatusSuccess,
		UserID: userID.String(),
		Payload: map[string]string{
			"count": fmt.Sprint(len(codes)),
		},
	})
	return codes, nil
}

// writeRecoverySet replaces the account's codes with the hashed forms of the
// clear ones. The caller owns the transaction; the caller answers the clear
// codes.
func (s *Service) writeRecoverySet(ctx context.Context, tx datastore.Querier, userID uuid.UUID, codes []string, now time.Time) error {
	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = hashToken(code)
	}
	return s.repo.ReplaceRecoveryCodes(ctx, tx, userID, hashes, now)
}

// consumeRecoveryCode marks one code used. The hash lookup is the timing
// answer: the presented code is hashed and matched, never compared in clear
// against a stored row.
func (s *Service) consumeRecoveryCode(ctx context.Context, userID uuid.UUID, code string, now time.Time) (bool, error) {
	return s.repo.ConsumeRecoveryCode(ctx, s.pool, userID, hashToken(code), now)
}

// ---- The way out ----

// DisableMfa removes every authenticator and the recovery set. The proof is
// the caller's code — a stolen session alone cannot switch the protection
// off, which is the property the whole second factor exists for.
func (s *Service) DisableMfa(ctx context.Context, userID uuid.UUID, code string) error {
	confirmed, countErr := s.repo.CountConfirmedTotp(ctx, s.pool, userID)
	if countErr != nil {
		return countErr
	}
	if confirmed == 0 {
		return ErrNotConfirmed
	}
	if err := s.verifyProof(ctx, userID, code); err != nil {
		return err
	}

	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, deleteErr := s.repo.DeleteAllTotpForUser(ctx, tx, userID); deleteErr != nil {
			return deleteErr
		}
		return s.repo.DeleteAllRecoveryForUser(ctx, tx, userID)
	})
	if err != nil {
		return err
	}

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventMfaDisabled,
		Status: audit.StatusSuccess,
		UserID: userID.String(),
	})
	return nil
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
		var b strings.Builder
		for j := range recoveryCodeBytes * 2 {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if err != nil {
				return nil, fmt.Errorf("multifactor: recovery code: %w", err)
			}
			b.WriteByte(alphabet[n.Int64()])
			if j%4 == 3 && j < recoveryCodeBytes*2-1 {
				b.WriteByte('-')
			}
		}
		out[i] = b.String()
	}
	return out, nil
}

// randomToken answers nbytes of crypto-random hex.
func randomToken(nbytes int) (string, error) {
	raw := make([]byte, nbytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("multifactor: token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// hashToken is the one-way form every server-side token takes: a plain
// SHA-256, keyed by nothing, because the token's entropy is the secret and
// the hash's job is only to keep a database leak from replaying it.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
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
