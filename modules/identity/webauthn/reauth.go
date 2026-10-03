package webauthn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	gowebauthn "github.com/go-webauthn/webauthn/webauthn"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/modules/identity/signin"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// ErrProofRefused is the one refusal a step-up proof earns: a wrong
// password, a failed assertion, a foreign assertion, and a spent or unknown
// token answer the same failure, so the surface does not disclose which
// half was the lie.
var ErrProofRefused = errors.New("webauthn: the reauthentication proof failed")

// ErrCodeSendUnavailable is a code request the running process cannot
// serve: the mailer or the queue the delivery rides is not configured.
var ErrCodeSendUnavailable = errors.New("webauthn: the code delivery is not configured")

// ErrResendTooSoon is a code request inside the resend window — the
// feature's cooldown beside the rate bucket's budget.
var ErrResendTooSoon = errors.New("webauthn: a code was sent recently")

// codeEnqueuer accepts the message a code request produced. internal/jobs
// satisfies it with the durable queue; the disclosure boundary for the raw
// code is the queue's optional payload encryption, unchanged.
type codeEnqueuer interface {
	EnqueueReauthenticationCodeEmail(ctx context.Context, mail CodeEmail) error
}

// CodeEmail is one reverification code's delivery: the raw code rides to the
// queue as the only clear-text copy in the system.
type CodeEmail struct {
	// UserID is the account the proof is for.
	UserID string
	// Email is the address on record.
	Email string
	// DisplayName is the name the template greets.
	DisplayName string
	// Token is the raw code the message carries.
	Token string
	// TTLSeconds is the window the copy states.
	TTLSeconds int64
}

const (
	// reauthCodeTTL is the email code's window: long enough to reach an
	// inbox and type a twelve-character code, short enough to be a
	// credential that dies.
	reauthCodeTTL = 15 * time.Minute

	// resendCooldown is how long the live code keeps a fresh email send,
	// the same window the one-time access send observes.
	resendCooldown = time.Minute

	// reauthCodeLength is the code's length: twelve characters of the
	// unambiguous alphabet — a credential typed from an email, not a
	// six-digit guess a drive can carry off.
	reauthCodeLength = 12
)

// SendReauthenticationCode delivers the email-code reverification factor to
// the caller's own address — the proof an account with no password and no
// passkey holds. One live code per account: a resend inside the cooldown is
// refused, past it the newest code replaces the old, so the email on the
// screen is the only code that works. The code rests hashed; the queue's
// payload is the only clear-text copy.
func (s *Service) SendReauthenticationCode(ctx context.Context, userID uuid.UUID) error {
	if s.mail == nil || !s.mail.Configured() || s.codeMail == nil {
		return ErrCodeSendUnavailable
	}
	if sent, err := s.repo.ReauthenticationCodeSentAt(ctx, s.pool, userID); err != nil {
		return err
	} else if sent != nil && s.now().Before(sent.Add(resendCooldown)) {
		return ErrResendTooSoon
	}

	account, err := s.issuer.FindAccountByIDAny(ctx, userID)
	if err != nil {
		return err
	}

	code, err := crypto.RandomString(reauthCodeLength, crypto.AlphabetUnambiguous)
	if err != nil {
		return fmt.Errorf("webauthn: reauthentication code: %w", err)
	}
	now := s.now()
	expires := now.Add(reauthCodeTTL)
	if err := s.repo.UpsertReauthenticationCode(ctx, s.pool, userID, crypto.HashHexToken(code), expires, now); err != nil {
		return err
	}
	if err := s.codeMail.EnqueueReauthenticationCodeEmail(ctx, CodeEmail{
		UserID:      userID.String(),
		Email:       account.Email,
		DisplayName: account.DisplayName,
		Token:       code,
		TTLSeconds:  int64(reauthCodeTTL.Seconds()),
	}); err != nil {
		return fmt.Errorf("webauthn: enqueue reauthentication code: %w", err)
	}

	// The record follows the queue's acceptance, on the pool rather than a
	// transaction: the enqueue is already committed, and a record inside a
	// transaction that rolled back after it would understate what happened.
	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventWebauthnReauthenticationCodeSent,
		UserID: userID.String(),
		Payload: map[string]string{
			"email": account.Email,
		},
	})
	return nil
}

// Reauthenticate proves the caller again and mints the step-up token. One
// proof per call: a password checked against the caller's own account, a
// passkey assertion over a ceremony the sign-in begin answered — whose
// resolved account must be the caller's, so a stolen session cannot launder
// another holder's credential into a proof for this one — or the email code
// SendReauthenticationCode delivered to the caller's own address, the
// factor an account with no proof material holds.
//
// The token is single use and short-lived: its hash is all that rests in
// the database, several live tokens per account are legitimate, and the
// guarded call that reads the header spends it in one statement.
func (s *Service) Reauthenticate(ctx context.Context, userID uuid.UUID, password, passkeySession, passkeyCredential, emailCode string) (string, time.Time, error) {
	var resolved uuid.UUID
	proof := ""
	switch {
	case password != "":
		match, err := s.issuer.VerifyPassword(ctx, userID, password)
		if err != nil {
			return "", time.Time{}, err
		}
		if !match {
			return "", time.Time{}, ErrProofRefused
		}
		resolved, proof = userID, "password"
	case passkeySession != "" && passkeyCredential != "":
		account, err := s.verifyStepUpAssertion(ctx, passkeySession, passkeyCredential)
		if err != nil {
			return "", time.Time{}, err
		}
		if account.ID != userID {
			// The assertion proved another holder: for this caller it is
			// not a proof, and the refusal is the same one a wrong
			// password earns.
			return "", time.Time{}, ErrProofRefused
		}
		resolved, proof = userID, "passkey"
	case emailCode != "":
		spent, err := s.repo.ConsumeReauthenticationCode(ctx, s.pool, userID, crypto.HashHexToken(emailCode), s.now())
		if err != nil {
			return "", time.Time{}, err
		}
		if !spent {
			return "", time.Time{}, ErrProofRefused
		}
		resolved, proof = userID, "email_code"
	default:
		return "", time.Time{}, ErrProofRefused
	}

	token, err := crypto.RandomHexToken(32)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("webauthn: reauthentication token: %w", err)
	}
	expires := s.now().Add(s.reverificationWindow(ctx))
	if err := s.repo.CreateReauthenticationToken(ctx, s.pool, resolved, crypto.HashHexToken(token), s.now(), expires); err != nil {
		return "", time.Time{}, err
	}

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventWebauthnReauthenticationGranted,
		UserID: resolved.String(),
		Payload: map[string]string{
			"method": proof,
		},
	})
	return token, expires, nil
}

// verifyStepUpAssertion runs the usernameless verification a step-up proof
// shares with the sign-in: the ceremony is consumed, the assertion verified,
// and the account resolved from the credential. The assertion's bookkeeping
// commits here too — a step-up proof is a real use of the credential.
func (s *Service) verifyStepUpAssertion(ctx context.Context, sessionWire, credentialJSON string) (*signin.Account, error) {
	sessionID, err := typeidParseSession(sessionWire)
	if err != nil {
		return nil, ErrCeremonyInvalid
	}
	row, live, err := s.consumeCeremony(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !live {
		return nil, ErrCeremonyInvalid
	}

	parsed, parseErr := protocolParseAssertion(credentialJSON)
	if parseErr != nil {
		return nil, parseErr
	}

	sessionData, err := s.storedSessionData(row, nil)
	if err != nil {
		return nil, err
	}

	var account *signin.Account
	handler := func(_, userHandle []byte) (gowebauthn.User, error) {
		if len(userHandle) != 16 {
			return nil, fmt.Errorf("%w: the user handle is not an account identifier", ErrAssertionInvalid)
		}
		found, lookupErr := s.issuer.FindAccountByIDAny(ctx, uuid.UUID(userHandle))
		if lookupErr != nil {
			return nil, lookupErr
		}
		account = found
		creds, listErr := s.engineCredentials(ctx, found.ID)
		if listErr != nil {
			return nil, listErr
		}
		return &credentialUser{id: found.ID, name: found.Username, displayName: found.DisplayName, credentials: creds}, nil
	}
	credential, err := s.engine.ValidateDiscoverableLogin(handler, sessionData, parsed)
	if err != nil {
		return nil, classifyCeremonyError(err)
	}
	if account == nil || credential == nil {
		return nil, fmt.Errorf("%w: the assertion resolved to no account", ErrAssertionInvalid)
	}
	if credential.Authenticator.CloneWarning {
		return nil, ErrClonedCredential
	}

	stored, err := s.repo.GetCredentialByCredentialID(ctx, s.pool, credential.ID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.RecordAssertion(ctx, s.pool, stored.ID, int64(credential.Authenticator.SignCount), credential.Flags.BackupState, s.now()); err != nil {
		return nil, err
	}
	return account, nil
}

// VerifyBridgeAssertion verifies the assertion half of an MFA challenge: the
// ceremony is consumed, the assertion checked, and the resolved account must
// be the bridge's own — an assertion that proves another holder is not a
// proof for this one. It is the multifactor feature's seam, defined there
// and satisfied here; the assertion's bookkeeping commits as the real use
// of the credential it was.
func (s *Service) VerifyBridgeAssertion(ctx context.Context, userID uuid.UUID, sessionID, credentialJSON string) error {
	account, err := s.verifyStepUpAssertion(ctx, sessionID, credentialJSON)
	if err != nil {
		return err
	}
	if account.ID != userID {
		return ErrProofRefused
	}
	return nil
}

// ConsumeReauthentication spends the proof a guarded call carries. It is// the guard's seam: the interceptor reads the header, this method deletes
// the hashed row in one statement, and a replay answers the same refusal an
// unknown token does. The consumption is recorded, so a double-spend
// attempt shows up in the log as the second refusal it was.
func (s *Service) ConsumeReauthentication(ctx context.Context, caller *jwtutils.Caller, token string) error {
	userID, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return ErrProofRefused
	}
	spent, err := s.repo.ConsumeReauthenticationToken(ctx, s.pool, userID, crypto.HashHexToken(token), s.now())
	if err != nil {
		return err
	}
	if !spent {
		return ErrProofRefused
	}
	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventWebauthnReauthenticationConsumed,
		UserID: userID.String(),
	})
	return nil
}

// proofMethod is gone: the proof's name is chosen at the switch, so the
// audit payload and the code cannot drift.
