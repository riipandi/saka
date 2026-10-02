package multifactor

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/crypto"
)

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

	codes, genErr := generateRecoveryCodes(recoveryCodeCount)
	if genErr != nil {
		return nil, genErr
	}
	// The proof, the rewrite, and the record commit together: a rolled-back
	// regeneration must not have burned the proof or left a half state.
	txErr := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if verifyErr := s.verifyProof(ctx, tx, userID, code); verifyErr != nil {
			return verifyErr
		}
		if writeErr := s.writeRecoverySet(ctx, tx, userID, codes, now); writeErr != nil {
			return writeErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventMfaRecoveryRegenerated,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"count": fmt.Sprint(len(codes)),
			},
		})
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return codes, nil
}

// writeRecoverySet replaces the account's codes with the hashed forms of the
// clear ones. The caller owns the transaction; the caller answers the clear
// codes.
func (s *Service) writeRecoverySet(ctx context.Context, tx datastore.Querier, userID uuid.UUID, codes []string, now time.Time) error {
	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = crypto.HashHexToken(code)
	}
	return s.repo.ReplaceRecoveryCodes(ctx, tx, userID, hashes, now)
}

// consumeRecoveryCode marks one code used. The hash lookup is the timing
// answer: the presented code is hashed and matched, never compared in clear
// against a stored row.
func (s *Service) consumeRecoveryCode(ctx context.Context, db datastore.Querier, userID uuid.UUID, code string, now time.Time) (bool, error) {
	return s.repo.ConsumeRecoveryCode(ctx, db, userID, crypto.HashHexToken(code), now)
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
	if err := s.verifyProof(ctx, s.pool, userID, code); err != nil {
		return err
	}

	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, deleteErr := s.repo.DeleteAllTotpForUser(ctx, tx, userID); deleteErr != nil {
			return deleteErr
		}
		if recErr := s.repo.DeleteAllRecoveryForUser(ctx, tx, userID); recErr != nil {
			return recErr
		}
		// The removal and its record commit together: a disabled state that
		// reads back has the record that explains it.
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventMfaDisabled,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
		})
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// MfaDisabledNotice is what the administrative disable hands the
// notification channel: the account it was about, and the reason the
// operator wrote. The channel renders and delivers; the service only
// states.
type MfaDisabledNotice struct {
	UserID      string
	Email       string
	DisplayName string
	Reason      string
}

// noticeEnqueuer is the notification channel the administrative disable
// hands the message to. The concrete type lives in internal/jobs — the
// adapter keeps the queue out of this package, the way the password
// recovery's enqueuer does.
type noticeEnqueuer interface {
	EnqueueMfaDisabledNotice(ctx context.Context, notice MfaDisabledNotice)
}

// WithNoticeEnqueuer wires the notification channel after construction —
// the same post-construction seam the sign-in gate rides, because the
// queue client is built beside this service, not beneath it.
func (s *Service) WithNoticeEnqueuer(enqueuer noticeEnqueuer) *Service {
	s.notices = enqueuer
	return s
}

// AdminDisableMfa removes every authenticator and the recovery set of the
// named account. The caller is the administrator: no proof code exists to
// give — the holder has lost every factor, which is the reason the
// procedure runs — so the administrative session is the authority and the
// record names it. An account without a confirmed factor is refused: there
// is nothing to disable, and the refusal tells the operator so.
func (s *Service) AdminDisableMfa(ctx context.Context, targetUserID uuid.UUID, reason string) error {
	// The target is resolved before the tables are read: an unknown
	// identifier is the operator's typo, and it answers the not-found the
	// other administrative refusals keep.
	if _, err := s.issuer.FindAccountByIDAny(ctx, targetUserID); err != nil {
		return ErrUserNotFound
	}

	confirmed, err := s.repo.CountConfirmedTotp(ctx, s.pool, targetUserID)
	if err != nil {
		return err
	}
	if confirmed == 0 {
		return ErrNotConfirmed
	}

	payload := map[string]string{}
	if reason != "" {
		payload["reason"] = reason
	}

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, deleteErr := s.repo.DeleteAllTotpForUser(ctx, tx, targetUserID); deleteErr != nil {
			return deleteErr
		}
		if recErr := s.repo.DeleteAllRecoveryForUser(ctx, tx, targetUserID); recErr != nil {
			return recErr
		}
		// The removal and its record commit together, the way the
		// self-service disable writes its own.
		s.audit.Record(ctx, tx, audit.Entry{
			Event:   audit.EventMfaDisabled,
			Status:  audit.StatusSuccess,
			UserID:  targetUserID.String(),
			Payload: payload,
		})
		return nil
	})
	if err != nil {
		return err
	}

	// The notice rides the account's own address; a missing channel or a
	// failed enqueue is best-effort — the removal has committed, and the
	// audit record already says so.
	if s.notices != nil {
		account, findErr := s.issuer.FindAccountByIDAny(ctx, targetUserID)
		if findErr != nil {
			return fmt.Errorf("multifactor: disable notice account: %w", findErr)
		}
		s.notices.EnqueueMfaDisabledNotice(ctx, MfaDisabledNotice{
			UserID:      targetUserID.String(),
			Email:       account.Email,
			DisplayName: account.DisplayName,
			Reason:      reason,
		})
	}
	return nil
}

// VerifyRecoveryCode spends one recovery code as a standalone proof of
// identity — the step-up a sensitive client flow asks for without a new
// sign-in. The code is consumed exactly once, the way every use of a
// recovery code is, and the consumption is the record.
func (s *Service) VerifyRecoveryCode(ctx context.Context, userID uuid.UUID, code string) error {
	consumed, err := s.consumeRecoveryCode(ctx, s.pool, userID, code, s.now())
	if err != nil {
		return err
	}
	if !consumed {
		return ErrCodeInvalid
	}

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:  audit.EventMfaRecoveryVerified,
		Status: audit.StatusSuccess,
		UserID: userID.String(),
	})
	return nil
}
