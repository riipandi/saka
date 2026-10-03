package password

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/pkg/crypto"
)

// defaultHasher is the credential hashing the account's password verifies
// against — the same algorithm and parameters, so a reset writes what
// SignIn can read.
func defaultHasher(clearText string) (string, error) {
	return crypto.NewPasswordHasher().Hash(clearText)
}

// The failures the flow reports. The handler maps them to connect codes, so
// the wire form of a refusal lives with the transport, not here.
var (
	// ErrUserNotFound is a trigger for an address or identifier no account
	// carries. ForgotPassword answers the same as success — the caller
	// cannot see it; the admin trigger maps it to not_found.
	ErrUserNotFound = errors.New("password: account not found")

	// ErrNoPassword is a trigger for an account holding no password
	// credential at all (passkey-only), which a reset cannot serve.
	ErrNoPassword = errors.New("password: account holds no password")

	// ErrAccountForbidden is a reset whose token is valid but whose account
	// is disabled or inside a live ban window. The refusal names the reason,
	// the same convention the sign-in keeps for account state.
	ErrAccountForbidden = errors.New("password: account is disabled or banned")

	// ErrMailUnavailable is a request the running process cannot serve: no
	// SMTP host is configured, so enqueuing would only dead-letter a task.
	ErrMailUnavailable = errors.New("password: mailer is not configured")

	// ErrInvalidToken covers an unknown, expired, and spent reset token:
	// answering differently would tell a caller which half was wrong.
	ErrInvalidToken = errors.New("password: invalid reset token")

	// ErrResendTooSoon is a request inside the cooldown the last send
	// opened: another message now would only invite a mailbomb.
	ErrResendTooSoon = errors.New("password: a reset email was sent recently")

	// ErrSamePassword is a reset whose new credential equals the current
	// one: a reset that changes nothing is either a typo or a probe, and
	// both deserve the refusal rather than a success that rotated nothing.
	ErrSamePassword = errors.New("password: the new password matches the current one")
)

// tokenTTL is how long a reset code works. The template copy states it, so
// changing one means changing the other.
const tokenTTL = time.Hour

// resetCodeLength is the reset code's length: the unambiguous alphabet at
// twelve characters — the form the verification code keeps, long enough
// that a typed guess is hopeless, short enough that a human types it.
const resetCodeLength = 12

// resendCooldown is how long the last send keeps a new one out. The window
// is what stops a caller from turning the trigger into a mailbomb; the token
// row's send time is the clock it reads.
const resendCooldown = time.Minute

// ResetEmail is the message a trigger queues. The queue's task wraps it, so
// this package names the fields and never the queue — importing internal/jobs
// from here would close a cycle through apikey back into user.
type ResetEmail struct {
	// UserID is the account the reset belongs to, for tracing.
	UserID string
	// Email is the address on record when the trigger ran.
	Email string
	// Token is the raw reset token the link carries. Its hash is all the
	// database keeps.
	Token string
}

// resetEnqueuer accepts the message a trigger produced. internal/jobs
// satisfies it with the durable queue; the disclosure boundary for the raw
// token is the queue's optional payload encryption, unchanged.
type resetEnqueuer interface {
	EnqueuePasswordResetEmail(ctx context.Context, email ResetEmail) error
	EnqueuePasswordChangedNotice(ctx context.Context, notice ChangedNotice) error
}

// ChangedNotice is the receipt a completed reset sends. It carries no
// secret: the credential has already been replaced when this is queued.
type ChangedNotice struct {
	// UserID is the account whose credential was replaced.
	UserID string
	// Email is the address the receipt goes to.
	Email string
	// DisplayName is the name the receipt greets.
	DisplayName string
}

// sessionEnder is the half of the session lifecycle a reset needs: the rows
// it invalidates. It is nil in tests that exercise the token flow without
// the session coupling.
type sessionEnder interface {
	RevokeAllForUser(ctx context.Context, tx datastore.Querier, userID uuid.UUID) (int, error)
}

// UUIDDecoder turns the wire-form TypeID into the row's UUID. It is
// injected because the format's owner imports this package for the password
// policy — a direct import back would close a cycle.
type UUIDDecoder func(wire string) (uuid.UUID, error)

// Service issues the reset tokens and consumes them.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// hasher rewrites the credential the token opens.
	hasher hasherFunc
	// decoder turns the admin trigger's wire-form identifier into the row's
	// UUID. It is wired post-construction by the area, which owns the
	// format.
	decoder UUIDDecoder
	// enqueuer accepts the email a trigger produced. It is wired
	// post-construction by the area, because internal/jobs cannot sit
	// below this package.
	enqueuer resetEnqueuer
	// sessions ends the live rows a successful reset withdraws. It is nil
	// in the tests that exercise the token flow without the coupling.
	sessions sessionEnder
	// audit writes the records of a send and of a completed reset, in the
	// transactions their writes run in.
	audit   *audit.Recorder
	mail    *mailer.Service
	baseURL string
	log     *slog.Logger
	now     func() time.Time
	// policy is the credential check's runtime source. Nil keeps the
	// static policy.
	policy *Validator
}

// WithPasswordPolicy wires the settings-driven validator. Nil keeps the
// static policy — the state a test or a bare wiring is in.
func (s *Service) WithPasswordPolicy(policy *Validator) *Service {
	s.policy = policy
	return s
}

// validatePassword runs the credential through the wired policy.
func (s *Service) validatePassword(ctx context.Context, clearText string) error {
	if s.policy != nil {
		return s.policy.Validate(ctx, clearText)
	}
	return Validate(clearText)
}

// hasherFunc is the credential hashing the service owes. It is the same
// function the account's password verification answers, kept as a function
// so the recovery flow needs none of the signing-in machinery.
type hasherFunc func(clearText string) (string, error)

// NewService builds the service. The mailer is the infrastructure the
// composition root resolves: the procedure writes the token row and hands
// the message to the enqueuer, which owns the SMTP attempt and its retries.
func NewService(pool *datastore.Postgres, mail *mailer.Service, recorder *audit.Recorder, baseURL string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:     pool,
		repo:     NewRepository(),
		hasher:   defaultHasher,
		sessions: nil,
		audit:    recorder,
		mail:     mail,
		baseURL:  baseURL,
		log:      log,
		now:      time.Now,
	}
}

// WithSessionEnder wires the session lifecycle the successful reset couples
// to. Called after construction because the session service is built beside
// this one, not before it — the same seam the ban's side effects keep.
func (s *Service) WithSessionEnder(ender sessionEnder) *Service {
	s.sessions = ender
	return s
}

// WithUUIDDecoder wires the wire-form identifier decoder. Called after
// construction because the format's owner imports this package.
func (s *Service) WithUUIDDecoder(decoder UUIDDecoder) *Service {
	s.decoder = decoder
	return s
}

// WithEnqueuer wires the durable queue that owns the SMTP attempt. Called
// after construction for the same reason the other seams are: internal/jobs
// sits above this package.
func (s *Service) WithEnqueuer(enqueuer resetEnqueuer) *Service {
	s.enqueuer = enqueuer
	return s
}

// ForgotPassword issues a reset token for the named address and enqueues the
// email. The answer is success whether the account exists or not — the
// endpoint is not an account enumerator — so every early return carries the
// same answer and the work differs only in what it writes. The raw token is
// answered only when the deployment exposes it: the email is the ordinary
// channel, and a response body is a disclosure the configuration owns.
func (s *Service) ForgotPassword(ctx context.Context, email string) (string, error) {
	account, err := s.repo.FindUserByEmail(ctx, s.pool, email)
	if errors.Is(err, datastore.ErrNoRows) {
		// No account, no row, no record: the silence is the anti-enumeration.
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !account.PasswordSet {
		return "", nil
	}

	// The resend cooldown reads the send time the last token row stamps: a
	// request inside the window holds the send rather than re-issuing, and
	// answers the same generic success — a distinct refusal would tell the
	// caller the address is real and freshly targeted, which is the one bit
	// every other path here refuses to give.
	if existing, findErr := s.repo.FindTokenByUser(ctx, s.pool, account.ID); findErr == nil && existing.LastSent != nil {
		if s.now().Before(existing.LastSent.Add(resendCooldown)) {
			return "", nil
		}
	}

	return s.issue(ctx, account)
}

// AdminResetUserPassword lets an administrator trigger the same email on a
// named account. The administrator never sees or sets the credential — the
// token goes to the account's address, and its holder completes the reset
// through ResetPassword. An impersonating administrator is refused by the
// guard before this runs.
func (s *Service) AdminResetUserPassword(ctx context.Context, wireUserID string) error {
	if s.decoder == nil {
		return ErrUserNotFound
	}
	userID, err := s.decoder(wireUserID)
	if err != nil {
		return ErrUserNotFound
	}
	account, err := s.repo.FindUserByID(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if !account.PasswordSet {
		return ErrNoPassword
	}

	// The same cooldown applies: an administrator's impatience must not
	// turn into a mailbomb either.
	if existing, findErr := s.repo.FindTokenByUser(ctx, s.pool, account.ID); findErr == nil && existing.LastSent != nil {
		if s.now().Before(existing.LastSent.Add(resendCooldown)) {
			return ErrResendTooSoon
		}
	}

	if _, err := s.issue(ctx, account); err != nil {
		return err
	}
	return nil
}

// issue mints the token, writes its row, and enqueues the message. The
// cooldown check belongs to the caller: the self-service path answers
// success where it refuses, the admin path reports the refusal. The raw
// token is the return value — the caller decides who may see it.
func (s *Service) issue(ctx context.Context, account Account) (string, error) {
	if !s.mail.Configured() {
		return "", ErrMailUnavailable
	}
	if s.enqueuer == nil {
		return "", ErrMailUnavailable
	}

	// The reset credential is a single-use code — typed on the reset
	// screen, never linked. The unambiguous alphabet at twelve characters
	// is the same form the verification code keeps.
	raw, err := crypto.RandomString(resetCodeLength, crypto.AlphabetUnambiguous)
	if err != nil {
		return "", fmt.Errorf("password: mint token: %w", err)
	}
	now := s.now()
	if err := s.repo.UpsertToken(ctx, s.pool, account.ID, crypto.HashHexToken(raw), now.Add(tokenTTL), now); err != nil {
		return "", err
	}

	// The message is enqueued and the record is written after the queue
	// accepts: a task queue is durable on its own, and a record before the
	// enqueue would overstate what happened.
	if err := s.enqueuer.EnqueuePasswordResetEmail(ctx, ResetEmail{
		UserID: account.ID.String(),
		Email:  account.Email,
		Token:  raw,
	}); err != nil {
		return "", fmt.Errorf("password: enqueue: %w", err)
	}
	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventPasswordResetEmailSent,
		Status: audit.StatusSuccess,
		UserID: account.ID.String(),
	})
	return raw, nil
}

// ResetPassword spends a reset token on a new password. The token is the
// whole credential: the caller carries none. Success swaps the hash, ends
// the live sessions unless the request spares them, consumes the token, and
// records the reset — all in one transaction, so a rollback returns the
// token and the old password.
func (s *Service) ResetPassword(ctx context.Context, rawToken, newPassword string, terminateSessions bool) error {
	if err := s.validatePassword(ctx, newPassword); err != nil {
		return err
	}

	// The token is looked up before the password is hashed: a public
	// endpoint answers a junk token with a cheap refusal, not a KDF run.
	token, err := s.repo.FindTokenByHash(ctx, s.pool, crypto.HashHexToken(rawToken))
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	if !token.ExpiresAt.After(s.now()) {
		return ErrInvalidToken
	}

	hash, err := s.hasher(newPassword)
	if err != nil {
		return fmt.Errorf("password: hash: %w", err)
	}

	account, err := s.repo.FindUserByID(ctx, s.pool, token.UserID)
	if err != nil {
		return err
	}
	if account.Disabled || account.BannedNow() {
		// The state refusal is not the invalid-token one: the caller who
		// holds the token learns the real reason, the convention the
		// sign-in keeps for account state.
		return ErrAccountForbidden
	}

	// A new password equal to the current one is refused before anything
	// is written. The check needs the current hash, so the account row is
	// read again with it — an account holding no password credential has
	// nothing to compare against, and the reset stands.
	current, err := s.repo.FindPasswordHash(ctx, s.pool, token.UserID)
	if err != nil {
		return err
	}
	if current != "" {
		same, verifyErr := crypto.NewPasswordHasher().Verify(newPassword, current)
		if verifyErr != nil {
			// An unreadable stored hash is not the caller's fault; the
			// reset proceeds and overwrites it.
			s.log.Warn("password: current hash could not be verified", "error", verifyErr)
		} else if same {
			return ErrSamePassword
		}
	}

	var ended int
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if terminateSessions {
			var updateErr error
			ended, updateErr = s.revokeInTx(ctx, tx, token.UserID)
			if updateErr != nil {
				return updateErr
			}
		}
		if setErr := s.repo.SetPasswordHash(ctx, tx, token.UserID, hash); setErr != nil {
			return setErr
		}
		consumed, delErr := s.repo.DeleteToken(ctx, tx, token.ID, crypto.HashHexToken(rawToken))
		if delErr != nil {
			return delErr
		}
		if !consumed {
			// Another reset spent this token between the read and the
			// write: the write rolls back, so only one caller's password
			// lands.
			return ErrInvalidToken
		}
		// The swap, the revocations, and the record commit together: a
		// credential that reads as replaced has a record saying so.
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventPasswordReset,
			Status: audit.StatusSuccess,
			UserID: token.UserID.String(),
			Payload: map[string]string{
				"ended_sessions": fmt.Sprintf("%d", ended),
				"kept_sessions":  fmt.Sprintf("%t", !terminateSessions),
			},
		})
		return nil
	})
	if err != nil {
		return err
	}

	// The receipt goes out after the commit — a notice about a reset that
	// rolled back would be a lie. The enqueue is best-effort: the queue
	// retries the SMTP attempt, and a lost notice does not undo a reset
	// that happened.
	return s.enqueuer.EnqueuePasswordChangedNotice(ctx, ChangedNotice{
		UserID:      token.UserID.String(),
		Email:       account.Email,
		DisplayName: account.DisplayName,
	})
}

// ErrPasswordSet is the refusal for a first-credential set on an account
// that already holds one: the change flow is a different procedure, and the
// add must not become an overwrite that skips the current-password proof.
var ErrPasswordSet = errors.New("password: the account already holds a password")

// AddPassword sets an account's first credential — the self-service an
// account created without a password holds. The proof is the step-up token
// the guard consumed before this ran; the write carries the same policy the
// sign-up and the reset judge (min length, the char rules, the breach
// corpus), and the record commits in the hash's transaction. The notice
// rides the same best-effort enqueue the reset's receipt keeps.
func (s *Service) AddPassword(ctx context.Context, userID uuid.UUID, newPassword string) error {
	if err := s.validatePassword(ctx, newPassword); err != nil {
		return err
	}

	// The precondition reads before the KDF runs: an account that already
	// holds a credential learns the refusal cheaply.
	current, err := s.repo.FindPasswordHash(ctx, s.pool, userID)
	if err != nil {
		return err
	}
	if current != "" {
		return ErrPasswordSet
	}

	account, err := s.repo.FindUserByID(ctx, s.pool, userID)
	if err != nil {
		return err
	}
	if account.Disabled || account.BannedNow() {
		return ErrAccountForbidden
	}

	hash, err := s.hasher(newPassword)
	if err != nil {
		return fmt.Errorf("password: hash: %w", err)
	}

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if setErr := s.repo.SetPasswordHash(ctx, tx, userID, hash); setErr != nil {
			return setErr
		}
		// The hash and the record commit together: a credential that reads
		// as added has a record saying so.
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventPasswordAdded,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
		})
		return nil
	})
	if err != nil {
		return err
	}

	// The receipt goes out after the commit — a notice about an add that
	// rolled back would be a lie. The enqueue is best-effort, the way the
	// reset's receipt is.
	return s.enqueuer.EnqueuePasswordChangedNotice(ctx, ChangedNotice{
		UserID:      userID.String(),
		Email:       account.Email,
		DisplayName: account.DisplayName,
	})
}

// revokeInTx ends the live sessions inside the caller's transaction. A nil
// ender (tests) skips the coupling; the count stays zero.
func (s *Service) revokeInTx(ctx context.Context, tx datastore.Querier, userID uuid.UUID) (int, error) {
	if s.sessions == nil {
		return 0, nil
	}
	return s.sessions.RevokeAllForUser(ctx, tx, userID)
}
