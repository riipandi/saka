package verification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/jobs"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/pkg/crypto"
)

// The failures the flow reports. The handler maps them to connect codes, so
// the wire form of a refusal lives with the transport, not here.
var (
	// ErrUserNotFound is a request whose claims name no account — an account
	// deleted after the token was signed.
	ErrUserNotFound = errors.New("verification: account not found")

	// ErrAlreadyVerified is a request for an account whose address the
	// database already records as verified.
	ErrAlreadyVerified = errors.New("verification: email already verified")

	// ErrMailUnavailable is a request the running process cannot serve: no
	// SMTP host is configured, so enqueuing would only dead-letter a task.
	ErrMailUnavailable = errors.New("verification: mailer is not configured")

	// ErrInvalidToken covers an unknown, expired, and spent verification
	// token: answering differently would tell a caller which half was wrong.
	ErrInvalidToken = errors.New("verification: invalid verification token")

	// ErrResendTooSoon is a request inside the cooldown the last send
	// opened: another message now would only invite a mailbomb.
	ErrResendTooSoon = errors.New("verification: a message was sent recently")

	// ErrSameEmail is a change request whose new address is the one already
	// on record: there is nothing to change.
	ErrSameEmail = errors.New("verification: the new address is the current one")

	// ErrEmailTaken is a request or a confirmation whose address another
	// account is already on record with. The caller proved an account to
	// reach here, so the answer names the collision instead of pretending
	// the request will one day succeed.
	ErrEmailTaken = errors.New("verification: the address is already in use")

	// ErrSubaddressBlocked is a request whose new address's base another
	// account already holds, while access.block_email_subaddresses is on.
	// The answer is the failed-precondition the change flow's own
	// constraints carry, and it names nothing about the account whose
	// base collided.
	ErrSubaddressBlocked = errors.New("verification: the address cannot be used")

	// ErrEmailChangeDisabled is a change flow the toggle turned off. It is
	// the not-found-shaped answer: the surface says nothing about the
	// setting that closed it.
	ErrEmailChangeDisabled = errors.New("verification: email change is not available")
)

// tokenTTL is how long a verification code works. The template copy states
// it, so changing one means changing the other.
const tokenTTL = time.Hour

// verificationCodeLength is the code's length: the unambiguous alphabet at
// twelve characters — long enough that a typed guess is hopeless, short
// enough that a human types it. The short/long window rule the one-time
// access code keeps does not apply: these codes are the long form wherever
// they are issued.
const verificationCodeLength = 12

// newVerificationCode draws the single-use code the message carries and the
// hash answers.
func newVerificationCode() (string, error) {
	return crypto.RandomString(verificationCodeLength, crypto.AlphabetUnambiguous)
}

// resendCooldown is how long the last send keeps a new one out. The window
// is what stops a caller from turning the procedure into a mailbomb; the
// token row's send time is the clock it reads.
const resendCooldown = time.Minute

// Service issues the verification tokens and consumes them.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of a verification that completed, in the
	// transaction that consumes the token.
	audit   *audit.Recorder
	mail    *mailer.Service
	queue   *queue.Client
	baseURL string
	log     *slog.Logger
	now     func() time.Time

	// changes is the notice channel the email-change flow feeds. Nil until
	// wired; a service without one skips the two notices, never the flow.
	changes changeNoticeEnqueuer
	// settings is the change toggle's runtime source. Nil keeps the gate
	// closed.
	settings emailChangeSettings
	// subaddresses is the block-email-subaddresses seam the area wires
	// after construction. Nil leaves the guard out — the state a test or a
	// bare wiring is in — and the toggle decides whether the wired guard
	// reads.
	subaddresses SubaddressGuard
}

// NewService builds the service. The mailer and the queue are the
// infrastructure the composition root resolves: the procedure writes the
// token row and enqueues, the queue owns the SMTP attempt and its retries.
func NewService(pool *datastore.Postgres, mail *mailer.Service, client *queue.Client, recorder *audit.Recorder, baseURL string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:    pool,
		repo:    NewRepository(),
		audit:   recorder,
		mail:    mail,
		queue:   client,
		baseURL: baseURL,
		log:     log,
		now:     time.Now,
	}
}

// SendEmail issues a verification token for the signed-in account and
// enqueues the message. The account is looked up from the identifier the
// claims carry — the address on record is the one the message goes to,
// never one the request could name — so a changed address is honored at
// the next request without touching the signed token.
func (s *Service) SendEmail(ctx context.Context, userID uuid.UUID) error {
	account, err := s.repo.FindUserByID(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if account.EmailVerifiedAt != nil {
		return ErrAlreadyVerified
	}
	if !s.mail.Configured() {
		return ErrMailUnavailable
	}

	// The resend cooldown reads the send time the last token row stamps: a
	// request inside the window refuses, so the resend is the deliberate
	// re-issue the caller waits for, not the loop a script runs.
	if existing, findErr := s.repo.FindTokenByUser(ctx, s.pool, account.ID); findErr == nil && existing.LastSentAt != nil {
		if s.now().Before(existing.LastSentAt.Add(resendCooldown)) {
			return ErrResendTooSoon
		}
	}

	rawToken, err := newVerificationCode()
	if err != nil {
		return fmt.Errorf("verification: token: %w", err)
	}
	now := s.now()

	if err := s.repo.UpsertToken(ctx, s.pool, account.ID, crypto.HashHexToken(rawToken), now.Add(tokenTTL), now); err != nil {
		return err
	}
	if _, err := s.queue.Add(jobs.EmailVerificationTask{
		UserID:      account.ID.String(),
		Email:       account.Email,
		DisplayName: account.DisplayName,
		Token:       rawToken,
	}).Save(); err != nil {
		return fmt.Errorf("verification: enqueue: %w", err)
	}

	// The message is enqueued and the record is written after the queue
	// accepted it, so the record describes a message that will be attempted
	// rather than one that was requested. It runs on the pool rather than a
	// transaction because the enqueue is already committed: a task queue is
	// durable on its own, and a record inside a transaction that rolled back
	// after the enqueue would understate what happened.
	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventEmailVerificationSent,
		Status: audit.StatusSuccess,
		UserID: account.ID.String(),
		Payload: map[string]string{
			"email": account.Email,
		},
	})
	return nil
}

// IssueForSignup draws the single-use verification code for an account a
// sign-up has just created and writes its row inside the sign-up's own
// transaction, so the account and its outstanding code commit together. The
// raw value is answered once; only the caller's hash is stored.
func (s *Service) IssueForSignup(ctx context.Context, tx datastore.Querier, userID uuid.UUID, email, displayName string) (string, error) {
	rawToken, err := newVerificationCode()
	if err != nil {
		return "", fmt.Errorf("verification: token: %w", err)
	}
	now := s.now()
	if err := s.repo.UpsertToken(ctx, tx, userID, crypto.HashHexToken(rawToken), now.Add(tokenTTL), now); err != nil {
		return "", fmt.Errorf("verification: issue for signup: %w", err)
	}
	return rawToken, nil
}

// DeliverForSignup enqueues the verification message for a code whose row
// committed, then records the send. The message is not the sign-up's
// business: the caller delivered what it could and logs what it could not.
func (s *Service) DeliverForSignup(ctx context.Context, userID uuid.UUID, email, displayName, rawToken string) error {
	if !s.mail.Configured() {
		return ErrMailUnavailable
	}
	if _, err := s.queue.Add(jobs.EmailVerificationTask{
		UserID:      userID.String(),
		Email:       email,
		DisplayName: displayName,
		Token:       rawToken,
	}).Save(); err != nil {
		return fmt.Errorf("verification: enqueue: %w", err)
	}
	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventEmailVerificationSent,
		Status: audit.StatusSuccess,
		UserID: userID.String(),
		Payload: map[string]string{
			"email":  email,
			"source": "signup",
		},
	})
	return nil
}

// VerifyEmail consumes the token and verifies the account. The read, the
// stamp, and the delete run in one transaction, so a token cannot verify
// twice under a race: the delete is what a second caller loses. The delete
// carries the hash the read resolved, so a re-request that replaced the row
// between the two makes the consume fail rather than stamping a token that is
// no longer the one presented.
func (s *Service) VerifyEmail(ctx context.Context, rawToken string) error {
	hash := crypto.HashHexToken(rawToken)
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		token, findErr := s.repo.FindTokenByHash(ctx, tx, hash)
		if errors.Is(findErr, datastore.ErrNoRows) {
			return ErrInvalidToken
		}
		if findErr != nil {
			return findErr
		}
		if !token.ExpiresAt.After(s.now()) {
			return ErrInvalidToken
		}

		if markErr := s.repo.MarkVerified(ctx, tx, token.UserID, s.now()); markErr != nil {
			return markErr
		}
		consumed, deleteErr := s.repo.DeleteToken(ctx, tx, token.ID, hash, PurposeEmailVerification)
		if deleteErr != nil {
			return deleteErr
		}
		if !consumed {
			// A re-request replaced the row between the read and the delete:
			// the token this caller presented is no longer the live one, so
			// the stamp rolls back with the delete and the flow refuses.
			return ErrInvalidToken
		}
		// The stamp and the record commit together: an address that reads as
		// verified must have a record saying when it became so.
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventEmailVerified,
			Status: audit.StatusSuccess,
			UserID: token.UserID.String(),
		})
		return nil
	})
	return err
}

// EmailChangeNotice is what a pending request and a completed confirmation
// send to the address they concern; the struct lives in the jobs package
// beside the task it becomes. To is the address the notice goes to — the old
// one while the change is pending, the new one once it completed.
type EmailChangeNotice = jobs.EmailChangeNotice

// changeNoticeEnqueuer is the notification channel the email-change flow
// feeds. The interface lives here so the flow names no queue; the jobs
// package adapts it, the way every other notice travels.
type changeNoticeEnqueuer interface {
	EnqueuePendingNotice(ctx context.Context, notice jobs.EmailChangeNotice)
	EnqueueSuccessNotice(ctx context.Context, notice jobs.EmailChangeNotice)
}

// WithEmailChangeNotifier arms the two email-change notices. Nil-safe: a
// service without a notifier runs the flow and only skips the mail.
func (s *Service) WithEmailChangeNotifier(notices changeNoticeEnqueuer) *Service {
	s.changes = notices
	return s
}

// emailChangeSettings is the change toggle's runtime source: the gate reads
// fresh at every request and confirmation, so an operator's change lands
// without a restart. *appconfig.Settings satisfies it; the interface keeps
// the appconfig feature out of this one's import graph.
type emailChangeSettings interface {
	GetBool(ctx context.Context, key string) (bool, error)
	GetString(ctx context.Context, key string) (string, error)
}

// SettingChangeEmailEnabled is the catalog key the change gate reads. The
// catalog owns the name; this constant is how this package spells it.
const SettingChangeEmailEnabled = "users.change_email_enabled"

// SettingUserEnumerationProtection is the strict mode's key: strict, a
// change toward a taken address answers as if the verification started —
// no code, no refusal that names the collision.
const SettingUserEnumerationProtection = "auth.user_enumeration_protection"

// SettingAccessBlockSubaddresses is the catalog key the subaddress guard
// reads. The catalog owns the name; this constant is how this package
// spells it.
const SettingAccessBlockSubaddresses = "access.block_email_subaddresses"

// SubaddressGuard is the block-email-subaddresses seam: the one question
// the change flow asks before it spends a token on a base another account
// holds. The interface is this package's — the consuming side defines it —
// and the identity area satisfies it with the blocklist service after
// construction.
type SubaddressGuard interface {
	CollisionTaken(ctx context.Context, address string) (bool, error)
}

// WithSubaddressGuard wires the subaddress collision guard. Nil leaves the
// guard out.
func (s *Service) WithSubaddressGuard(guard SubaddressGuard) *Service {
	s.subaddresses = guard
	return s
}

// subaddressesBlocked answers the guard's toggle. An unreadable setting
// leaves the guard out — the lists fail open, and this rule rides with
// them; a broken read must never hold an account's own change hostage.
func (s *Service) subaddressesBlocked(ctx context.Context) bool {
	if s.settings == nil || s.subaddresses == nil {
		return false
	}
	on, err := s.settings.GetBool(ctx, SettingAccessBlockSubaddresses)
	if err != nil {
		return false
	}
	return on
}

// WithEmailChangeGate wires the change toggle. Nil keeps the gate closed —
// the state a test or a bare wiring is in, answering not-found.
func (s *Service) WithEmailChangeGate(settings emailChangeSettings) *Service {
	s.settings = settings
	return s
}

// emailChangeOpen answers the gate. An unreadable setting refuses: a gate
// that cannot answer is a gate closed.
func (s *Service) emailChangeOpen(ctx context.Context) bool {
	if s.settings == nil {
		return false
	}
	on, err := s.settings.GetBool(ctx, SettingChangeEmailEnabled)
	if err != nil {
		return false
	}
	return on
}

// changeIsStrict answers the enumeration mode. An unreadable setting keeps
// the default: the bulk behaviour, the honest refusals.
func (s *Service) changeIsStrict(ctx context.Context) bool {
	if s.settings == nil {
		return false
	}
	mode, err := s.settings.GetString(ctx, SettingUserEnumerationProtection)
	if err != nil {
		return false
	}
	return mode == "strict"
}

// RequestEmailChange writes the pending change for the signed-in account and
// enqueues its token to the address the change moves to. The code is the
// flow's whole credential: it is generated here, shown once in the message,
// and stored only as a hash, so a database leak cannot move an address. The
// token row binds the payload — the pending address — so the confirmation
// moves the account to the address the requester named, never one a replayed
// code could pick.
//
// The request is silent about the mail: acceptance says the token was issued
// and the task enqueued, not that a message was delivered. The notice to the
// old address rides the deployment's cost decision; the token message is the
// flow itself and always goes.
func (s *Service) RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) error {
	// The toggle is the cheapest check, so it runs before anything touches
	// the database: a flow turned off answers not-found — the same shape an
	// unknown account earns, and the surface says nothing about the setting.
	if !s.emailChangeOpen(ctx) {
		return ErrEmailChangeDisabled
	}
	account, err := s.repo.FindUserByID(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if !s.mail.Configured() {
		return ErrMailUnavailable
	}
	if newEmail == account.Email {
		return ErrSameEmail
	}
	taken := false
	if _, findErr := s.repo.FindUserByEmail(ctx, s.pool, newEmail); findErr == nil {
		taken = true
	} else if !errors.Is(findErr, datastore.ErrNoRows) {
		return findErr
	}
	// The strict mode answers the taken address as if the verification had
	// started: no code, no mail, no refusal that names the collision — the
	// response is the shape every requestable address answers with, and
	// the existence question has no left answer. The caller is
	// authenticated here, but the account behind the address is still
	// none of its business. A read that failed answers honestly: the
	// strict shim must not swallow a real failure into a fake success.
	if taken && s.changeIsStrict(ctx) {
		return nil
	}
	if taken {
		return ErrEmailTaken
	}
	// The subaddress blocker is the change flow's other refusal: the new
	// address's base is one an account already holds. It fails open with
	// the lists, and its answer says nothing about the other account —
	// the caller proved its own account to reach here, but the refusal
	// still does not confirm whose base it collided with.
	if s.subaddressesBlocked(ctx) {
		taken, guardErr := s.subaddresses.CollisionTaken(ctx, newEmail)
		if guardErr != nil {
			s.log.WarnContext(ctx, "verification: the subaddress collision scan failed; letting the change pass", "error", guardErr)
		} else if taken {
			return ErrSubaddressBlocked
		}
	}

	// The resend cooldown reads the send time the pending row stamps, the
	// same mailbomb stop the verification send keeps.
	if existing, findErr := s.repo.FindEmailChangeTokenByUser(ctx, s.pool, account.ID); findErr == nil && existing.LastSentAt != nil {
		if s.now().Before(existing.LastSentAt.Add(resendCooldown)) {
			return ErrResendTooSoon
		}
	}

	rawToken, err := newVerificationCode()
	if err != nil {
		return fmt.Errorf("verification: change token: %w", err)
	}
	now := s.now()

	if err := s.repo.UpsertEmailChangeToken(ctx, s.pool, account.ID, crypto.HashHexToken(rawToken), newEmail, now.Add(tokenTTL), now); err != nil {
		return err
	}
	if _, err := s.queue.Add(jobs.EmailChangeRequestEmailTask{
		Email:       newEmail,
		DisplayName: account.DisplayName,
		OldEmail:    account.Email,
		NewEmail:    newEmail,
		Token:       rawToken,
	}).Save(); err != nil {
		return fmt.Errorf("verification: change enqueue: %w", err)
	}

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventEmailChangeRequested,
		Status: audit.StatusSuccess,
		UserID: account.ID.String(),
		Payload: map[string]string{
			"old_email": account.Email,
			"new_email": newEmail,
		},
	})

	// The old address hears about the request the moment it is written —
	// the notice is the account holder's defense against a change someone
	// else asked for. Best-effort: the request committed, the audit record
	// already says so.
	if s.changes != nil {
		s.changes.EnqueuePendingNotice(ctx, EmailChangeNotice{
			UserID:      account.ID.String(),
			To:          account.Email,
			DisplayName: account.DisplayName,
			OldEmail:    account.Email,
			NewEmail:    newEmail,
		})
	}
	return nil
}

// ConfirmEmailChange consumes the pending token and moves the account to the
// address the token binds. The read, the move, and the delete run in one
// transaction, so a token cannot confirm twice under a race: the delete is
// what a second caller loses. The uniqueness judgement runs again inside the
// transaction — another account may have claimed the address between the
// request and the click — and a lost race leaves the token unconsumed, so
// the requester can ask for a different address with the same attempt
// budget.
//
// The token identifies the account, so the confirmation is answered without
// a caller: the frontend the message links to forwards the value, and the
// flow works in a browser that holds no session.
func (s *Service) ConfirmEmailChange(ctx context.Context, rawToken string) error {
	// The gate answers the confirmation too: a toggle turned off between
	// the request and the code ends the flow, not just the requests for it.
	if !s.emailChangeOpen(ctx) {
		return ErrEmailChangeDisabled
	}
	hash := crypto.HashHexToken(rawToken)
	var confirmed EmailChangeNotice
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		token, findErr := s.repo.FindEmailChangeTokenByHash(ctx, tx, hash)
		if errors.Is(findErr, datastore.ErrNoRows) {
			return ErrInvalidToken
		}
		if findErr != nil {
			return findErr
		}
		if !token.ExpiresAt.After(s.now()) {
			return ErrInvalidToken
		}

		if _, takenErr := s.repo.FindUserByEmail(ctx, tx, token.Payload); takenErr == nil {
			return ErrEmailTaken
		} else if !errors.Is(takenErr, datastore.ErrNoRows) {
			return takenErr
		}

		account, accountErr := s.repo.FindUserByID(ctx, tx, token.UserID)
		if accountErr != nil {
			return accountErr
		}

		if setErr := s.repo.SetEmail(ctx, tx, token.UserID, token.Payload, s.now()); setErr != nil {
			return setErr
		}
		consumed, deleteErr := s.repo.DeleteToken(ctx, tx, token.ID, hash, PurposeEmailChange)
		if deleteErr != nil {
			return deleteErr
		}
		if !consumed {
			// A re-request replaced the row between the read and the delete:
			// the payload this caller read belongs to a token that is no
			// longer live, so the move rolls back with the delete and the
			// stale confirmation applies nothing.
			return ErrInvalidToken
		}

		// The move and the record commit together: an account whose address
		// reads as changed must have a record saying when it did.
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventEmailChanged,
			Status: audit.StatusSuccess,
			UserID: token.UserID.String(),
			Payload: map[string]string{
				"old_email": account.Email,
				"new_email": token.Payload,
			},
		})

		confirmed = EmailChangeNotice{
			UserID:      token.UserID.String(),
			To:          token.Payload,
			DisplayName: account.DisplayName,
			OldEmail:    account.Email,
			NewEmail:    token.Payload,
		}
		return nil
	})
	if err != nil {
		return err
	}

	// The new address hears about the change once it happened. Best-effort,
	// the same way the pending notice is.
	if s.changes != nil {
		s.changes.EnqueueSuccessNotice(ctx, confirmed)
	}
	return nil
}
