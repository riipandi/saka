package webauthn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	gowebauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/jwtutils"
)

// ErrProofRefused is the one refusal a step-up proof earns: a wrong
// password, a failed assertion, a foreign assertion, and a spent or unknown
// token answer the same failure, so the surface does not disclose which
// half was the lie.
var ErrProofRefused = errors.New("webauthn: the reauthentication proof failed")

// Reauthenticate proves the caller again and mints the step-up token. One
// proof per call: a password checked against the caller's own account, or a
// passkey assertion over a ceremony the sign-in begin answered — whose
// resolved account must be the caller's, so a stolen session cannot launder
// another holder's credential into a proof for this one.
//
// The token is single use and short-lived: its hash is all that rests in
// the database, several live tokens per account are legitimate, and the
// guarded call that reads the header spends it in one statement.
func (s *Service) Reauthenticate(ctx context.Context, userID uuid.UUID, password, passkeySession, passkeyCredential string) (string, time.Time, error) {
	var resolved uuid.UUID
	switch {
	case password != "":
		match, err := s.issuer.VerifyPassword(ctx, userID, password)
		if err != nil {
			return "", time.Time{}, err
		}
		if !match {
			return "", time.Time{}, ErrProofRefused
		}
		resolved = userID
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
		resolved = userID
	default:
		return "", time.Time{}, ErrProofRefused
	}

	token, err := crypto.RandomHexToken(32)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("webauthn: reauthentication token: %w", err)
	}
	expires := s.now().Add(reauthTTL)
	if err := s.repo.CreateReauthenticationToken(ctx, s.pool, resolved, crypto.HashHexToken(token), expires); err != nil {
		return "", time.Time{}, err
	}

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventWebauthnReauthenticationGranted,
		UserID: resolved.String(),
		Payload: map[string]string{
			"method": proofMethod(password != ""),
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
		found, lookupErr := s.issuer.FindAccountByID(ctx, uuid.UUID(userHandle))
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

// proofMethod names the proof kind for the audit record.
func proofMethod(password bool) string {
	if password {
		return "password"
	}
	return "passkey"
}
