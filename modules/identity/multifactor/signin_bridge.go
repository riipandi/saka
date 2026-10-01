package multifactor

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/pkg/crypto"
)

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
	return s.beginBridge(ctx, userID, remember, PurposeVerify)
}

// BeginEnrollment writes the bridge the `mfa.required` gate mints: the
// password is proven, the account keeps no confirmed factor, and the
// enrollment endpoints are what the bridge admits. The caller is the sign-in
// service, at the same point its per-account fork would have minted a
// challenge — no token is issued until a factor is confirmed.
func (s *Service) BeginEnrollment(ctx context.Context, userID uuid.UUID, remember bool) (SignInOutcome, error) {
	return s.beginBridge(ctx, userID, remember, PurposeEnroll)
}

// beginBridge writes one bridge row of the named purpose. The write runs
// outside a transaction on purpose — the bridge is worthless without the
// password success that minted it, and that success carries no row.
func (s *Service) beginBridge(ctx context.Context, userID uuid.UUID, remember bool, purpose string) (SignInOutcome, error) {
	now := s.now()

	token, err := crypto.RandomHexToken(32)
	if err != nil {
		return SignInOutcome{}, err
	}
	hash := crypto.HashHexToken(token)

	expires := now.Add(pendingTTL)
	row := PendingSchema{
		ID:        uuid.NewV7(),
		UserID:    userID,
		TokenHash: hash,
		Remember:  remember,
		Purpose:   purpose,
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

// PasskeyAssertion is the passkey half of the challenge: the ceremony handle
// and the browser's assertion JSON. The passkey feature verifies it; the
// interface below keeps the multifactor package from importing it back.
type PasskeyAssertion struct {
	SessionID  string
	Credential string
}

// PasskeyVerifier verifies the assertion half of the challenge. The webauthn
// service satisfies it; the wiring rides the area's post-construction seam
// the same way the issuer's gate does.
type PasskeyVerifier interface {
	// VerifyBridgeAssertion verifies the assertion against the bridge's
	// account: the ceremony is consumed, the assertion checked, and an
	// assertion that resolves to another account is a refusal.
	VerifyBridgeAssertion(ctx context.Context, userID uuid.UUID, sessionID, credentialJSON string) error
}

// WithPasskeyVerifier wires the passkey half of the challenge after
// construction. Nil keeps the code-only challenge — the state a wiring that
// skipped the passkey feature is in, and a refusal at the call site rather
// than a failed run.
func (s *Service) WithPasskeyVerifier(verifier PasskeyVerifier) *Service {
	s.passkeys = verifier
	return s
}

// CompleteSignIn spends the bridge plus the second factor on the session.
// The bridge dies whatever way the call ends: success consumes it, a wrong
// code eats the failure budget, an expired bridge is swept on read.
//
// The proof runs inside the transaction the session opens in, so a failure
// consumes nothing — a TOTP step a rollback returned, a recovery code a
// rollback un-spent — and the bridge's delete carries the token hash, so two
// completions racing on one bridge cannot both open a session.
func (s *Service) CompleteSignIn(ctx context.Context, pendingToken, code string, passkey *PasskeyAssertion, session signin.SessionParams) (CompleteSignInResult, error) {
	now := s.now()

	hash := crypto.HashHexToken(pendingToken)
	pending, err := s.repo.FindLivePending(ctx, s.pool, hash, now)
	if errors.Is(err, datastore.ErrNoRows) {
		return CompleteSignInResult{}, ErrPendingInvalid
	}
	if err != nil {
		return CompleteSignInResult{}, err
	}

	// A bridge that has spent its budget is dead on arrival: swept here so
	// the account learns the refusal is final, not one guess from final.
	if pending.WrongAttempts >= maxAttempts {
		_, _ = s.repo.DeletePending(ctx, s.pool, pending.ID, hash)
		return CompleteSignInResult{}, ErrPendingExhausted
	}

	account, err := s.issuer.FindAccountByID(ctx, pending.UserID)
	if err != nil {
		return CompleteSignInResult{}, err
	}

	var result CompleteSignInResult
	var challengeErr error
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if passkey != nil {
			// The passkey half is verified beside the transaction, not
			// inside it: the assertion's ceremony consumption and the
			// credential's bookkeeping are their own writes, and a failed
			// proof costs the bridge's budget below, exactly as a wrong
			// code does.
			if verifyErr := s.verifyPasskeyChallenge(ctx, pending.UserID, passkey); verifyErr != nil {
				challengeErr = verifyErr
				return verifyErr
			}
		} else if _, verifyErr := s.verifyChallenge(ctx, tx, pending.UserID, code, now); verifyErr != nil {
			challengeErr = verifyErr
			return verifyErr
		}
		// The bridge dies in the transaction the session opens in: a
		// rollback returns the bridge, and the hash in the WHERE refuses the
		// second racer before anything is spent twice.
		deleted, deleteErr := s.repo.DeletePending(ctx, tx, pending.ID, hash)
		if deleteErr != nil {
			return deleteErr
		}
		if !deleted {
			return ErrPendingInvalid
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
		if errors.Is(challengeErr, ErrCodeInvalid) {
			// The wrong answer is the bridge's problem, not the session's:
			// the budget rides the row, committed apart from the rolled-back
			// proof, and the strike that fills it ends the bridge.
			last, budgetErr := s.repo.RegisterWrongAttempt(ctx, s.pool, pending.ID, maxAttempts)
			if budgetErr != nil {
				return CompleteSignInResult{}, budgetErr
			}
			if last {
				_, _ = s.repo.DeletePending(ctx, s.pool, pending.ID, hash)
				return CompleteSignInResult{}, ErrPendingExhausted
			}
		}
		if errors.Is(err, ErrPendingInvalid) {
			return CompleteSignInResult{}, ErrPendingInvalid
		}
		return CompleteSignInResult{}, err
	}
	return result, nil
}

// verifyPasskeyChallenge runs the assertion half of the challenge. A nil
// verifier is the wiring that skipped the passkey feature — the challenge
// simply cannot be answered this way, which is the refusal the call site
// answers rather than a failed run.
func (s *Service) verifyPasskeyChallenge(ctx context.Context, userID uuid.UUID, passkey *PasskeyAssertion) error {
	if s.passkeys == nil {
		return ErrCodeInvalid
	}
	return s.passkeys.VerifyBridgeAssertion(ctx, userID, passkey.SessionID, passkey.Credential)
}

// verifyChallenge answers whether the code is the account's second factor: a
// TOTP code from any confirmed authenticator, or one unused recovery code.
// It marks what it consumes — on the query surface it is handed, so a
// caller's transaction can return every consumption it caused.
func (s *Service) verifyChallenge(ctx context.Context, db datastore.Querier, userID uuid.UUID, code string, now time.Time) (bool, error) {
	// The recovery shape is longer than any TOTP code and carries hyphens;
	// one look at the shape picks the table, and the lookup is cheap enough
	// that trying both would also be fine.
	if isRecoveryShape(code) {
		consumed, err := s.consumeRecoveryCode(ctx, db, userID, code, now)
		if err != nil {
			return false, err
		}
		if !consumed {
			return false, ErrCodeInvalid
		}
		return true, nil
	}

	confirmed, err := s.repo.ListTotp(ctx, db, userID)
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
			won, err := s.repo.TouchTotpUsage(ctx, db, row.ID, step, now)
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
// a support ticket, so proofs replay within their window. The consumption
// runs on the query surface it is handed, so a caller's transaction can
// return it.
func (s *Service) verifyProof(ctx context.Context, db datastore.Querier, userID uuid.UUID, code string) error {
	now := s.now()
	if isRecoveryShape(code) {
		consumed, err := s.consumeRecoveryCode(ctx, db, userID, code, now)
		if err != nil {
			return err
		}
		if !consumed {
			return ErrCodeInvalid
		}
		return nil
	}

	rows, err := s.repo.ListTotp(ctx, db, userID)
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
