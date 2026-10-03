package multifactor

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"
	"uuid"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
)

// ---- Enrollment ----

// CountConfirmedTotp answers how many confirmed authenticators the account
// holds. It is the seam the passkey feature's shared enrollment ceiling
// reads: mfa.max_enrollments counts TOTP devices and passkeys together, and
// this is the TOTP half's door — exported so the wiring after construction
// can hand the webauthn service the counter without an import the other way.
func (s *Service) CountConfirmedTotp(ctx context.Context, db datastore.Querier, userID uuid.UUID) (int, error) {
	return s.repo.CountConfirmedTotp(ctx, db, userID)
}

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
	limit, limitErr := s.enrollmentLimit(ctx)
	if limitErr != nil {
		return BeginTotpEnrollmentResult{}, limitErr
	}
	if len(count) >= limit {
		return BeginTotpEnrollmentResult{}, ErrEnrollmentLimit
	}

	// The label the authenticator renders — "Saka: user@example.com" — is
	// the account the ceremony is for. An account the issuer cannot read is
	// a caller the guard should have refused, so the failure is internal.
	account, err := s.issuer.FindAccountByIDAny(ctx, userID)
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
	// The enrollment and its record commit together, so a row that reads
	// back has the record that explains it.
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if createErr := s.repo.CreateTotp(ctx, tx, row); createErr != nil {
			return createErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventMfaEnrollmentStarted,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"totp_id": totpIDString(row.ID),
			},
		})
		return nil
	})
	if err != nil {
		return BeginTotpEnrollmentResult{}, err
	}

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

	first, err := s.CountConfirmedTotp(ctx, s.pool, userID)
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
		// The activation and its record commit together.
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventMfaEnrollmentConfirmed,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"totp_id":     totpIDString(row.ID),
				"device_name": row.Name,
			},
		})
		return nil
	})
	if err != nil {
		return ConfirmTotpEnrollmentResult{}, err
	}

	return ConfirmTotpEnrollmentResult{
		TotpID:        totpIDString(row.ID),
		RecoveryCodes: clearCodes,
	}, nil
}

// EnrollmentView is one enrollment's metadata — the shape a settings page
// renders. It never carries a secret unless the service was built with
// WithExposedSecrets, the development aid.
type EnrollmentView struct {
	TotpID      string
	Name        string
	ConfirmedAt *time.Time
	LastUsedAt  *time.Time
	CreatedAt   time.Time
	Secret      string
}

// WithExposedSecrets turns the listing's decrypted-secret aid on. The wiring
// passes the configuration's expose flag only, and the configuration's
// validation refuses the flag outside the development mode — so a
// production run cannot carry the aid even by accident.
func (s *Service) WithExposedSecrets(expose bool) *Service {
	s.exposeSecrets = expose
	return s
}

// ListTotpEnrollments answers the account's authenticators.
func (s *Service) ListTotpEnrollments(ctx context.Context, userID uuid.UUID) ([]EnrollmentView, error) {
	rows, err := s.repo.ListTotp(ctx, s.pool, userID)
	if err != nil {
		return nil, err
	}
	out := make([]EnrollmentView, 0, len(rows))
	for _, row := range rows {
		view := EnrollmentView{
			TotpID:      totpIDString(row.ID),
			Name:        row.Name,
			ConfirmedAt: row.ConfirmedAt,
			LastUsedAt:  row.LastUsedAt,
			CreatedAt:   row.CreatedAt,
		}
		if s.exposeSecrets {
			secret, unsealErr := s.unseal(row.Secret)
			if unsealErr != nil {
				s.log.Warn("multifactor: enrollment secret could not be decrypted",
					"error", unsealErr, "totp_id", view.TotpID)
			} else {
				view.Secret = secret
			}
		}
		out = append(out, view)
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
	// holds it or refuse. The proof, the deletes, and the record commit
	// together — a failure between them must not leave recovery codes that
	// answer a factor the account no longer holds.
	if code == "" {
		return ErrProofRequired
	}
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if verifyErr := s.verifyProof(ctx, tx, userID, code); verifyErr != nil {
			return verifyErr
		}
		if delErr := s.repo.DeleteTotp(ctx, tx, row.ID, userID); delErr != nil {
			return delErr
		}
		// The set's device is gone: the recovery codes answer a factor that
		// no longer exists, so they go with it.
		if recErr := s.repo.DeleteAllRecoveryForUser(ctx, tx, userID); recErr != nil {
			return recErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventMfaDisabled,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"totp_id": totpIDString(row.ID),
				"reason":  "last_device_removed",
			},
		})
		return nil
	})
	return err
}
