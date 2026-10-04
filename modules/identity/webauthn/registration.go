package webauthn

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"uuid"

	"github.com/go-webauthn/webauthn/protocol"
	gowebauthn "github.com/go-webauthn/webauthn/webauthn"
	"go.jetify.com/typeid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
)

// typeidParseSession reads a ceremony handle back into its UUID. A handle
// that does not parse is a spent, foreign, or forged handle — the same
// invalid-ceremony answer any of them earns.
func typeidParseSession(wire string) (uuid.UUID, error) {
	parsed, err := typeid.Parse[SessionID](wire)
	if err != nil {
		return uuid.UUID{}, err
	}
	id, err := uuid.Parse(parsed.UUID())
	if err != nil {
		return uuid.UUID{}, err
	}
	return id, nil
}

// BeginRegistration opens the enrollment ceremony. The answer is the JSON
// the browser hands to navigator.credentials.create and the handle the
// verify call echoes. The ceremony state rests in its own row: the caller
// holds nothing but the handle, and the row dies at its expiry or its
// consumption.
//
// Credentials are enrolled discoverable on purpose — resident keys with the
// configured user-verification level — because the sign-in is usernameless:
// a credential the browser cannot name by itself cannot sign anybody in.
func (s *Service) BeginRegistration(ctx context.Context, userID uuid.UUID) (string, string, error) {
	verification, err := s.userVerification(ctx)
	if err != nil {
		return "", "", err
	}

	account, err := s.issuer.FindAccountByIDAny(ctx, userID)
	if err != nil {
		return "", "", err
	}
	user := &credentialUser{id: userID, name: account.Username, displayName: account.DisplayName}
	creation, session, err := s.engine.BeginRegistration(user, gowebauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
		ResidentKey:      protocol.ResidentKeyRequirementRequired,
		UserVerification: protocol.UserVerificationRequirement(verification),
	}))
	if err != nil {
		return "", "", fmt.Errorf("webauthn: begin registration: %w", err)
	}

	row, err := s.storeCeremony(ctx, &userID, ChallengeTypeRegistration, verification, session)
	if err != nil {
		return "", "", err
	}

	options, err := json.Marshal(creation)
	if err != nil {
		return "", "", fmt.Errorf("webauthn: creation options: %w", err)
	}
	handle, err := SessionIDFromUUID(row.ID)
	if err != nil {
		return "", "", fmt.Errorf("webauthn: session id: %w", err)
	}
	return string(options), handle.String(), nil
}

// VerifyRegistration finishes the enrollment. The session row is consumed
// first — single use is the DELETE's WHERE — so a failed verification costs
// the ceremony, the way a wrong TOTP confirm costs an enrollment: start
// again.
//
// The policy runs after the cryptography: the synced-passkey toggle is
// judged against the attestation's backup eligibility, and both limits are
// judged against what the account would hold — enforcement is at enrollment
// only, so lowering a limit never unregisters an existing device.
func (s *Service) VerifyRegistration(ctx context.Context, userID uuid.UUID, sessionWire, credentialJSON, name string) (View, error) {
	sessionID, err := typeidParseSession(sessionWire)
	if err != nil {
		return View{}, ErrCeremonyInvalid
	}
	row, consumed, err := s.consumeCeremony(ctx, sessionID)
	if err != nil {
		return View{}, err
	}
	if !consumed {
		return View{}, ErrCeremonyInvalid
	}

	parsed, err := protocol.ParseCredentialCreationResponseBody(strings.NewReader(credentialJSON))
	if err != nil {
		return View{}, fmt.Errorf("%w: %v", ErrAssertionInvalid, err)
	}

	sessionData, err := s.storedSessionData(row, userID[:])
	if err != nil {
		return View{}, err
	}
	account, err := s.issuer.FindAccountByIDAny(ctx, userID)
	if err != nil {
		return View{}, err
	}
	user := &credentialUser{id: userID, name: account.Username, displayName: account.DisplayName}
	credential, err := s.engine.CreateCredential(user, sessionData, parsed)
	if err != nil {
		return View{}, classifyCeremonyError(err)
	}

	allowSynced, err := s.allowSyncedPasskeys(ctx)
	if err != nil {
		return View{}, err
	}
	if credential.Flags.BackupEligible && !allowSynced {
		return View{}, ErrSyncedPasskeyOff
	}

	// The limit judgement and the credential write share one transaction
	// under the account's lock: two parallel enrollments read each other's
	// committed state, so a limit of one lands one credential — the
	// count-then-insert window two loose statements would leave open.
	var stored CredentialSchema
	txErr := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if lockErr := lockAccount(ctx, tx, userID); lockErr != nil {
			return lockErr
		}
		if limitErr := s.enforceLimits(ctx, tx, userID); limitErr != nil {
			return limitErr
		}
		row, storeErr := s.storeCredential(ctx, tx, userID, credential, name)
		if storeErr != nil {
			return storeErr
		}
		stored = row
		return nil
	})
	if txErr != nil {
		return View{}, txErr
	}

	wireID, err := IDFromUUID(stored.ID)
	if err == nil {
		s.audit.Record(ctx, s.pool, audit.Entry{
			Event:        audit.EventWebauthnCredentialRegistered,
			UserID:       userID.String(),
			ResourceType: "webauthn_credential",
			ResourceID:   stored.ID.String(),
			Payload: map[string]string{
				"credential": wireID.String(),
				"aaguid":     derefOrEmpty(stored.AAGUID),
				"name":       stored.Name,
			},
		})
		// The receipt rides the same best-effort enqueue the other
		// security notices keep: the enrollment committed, and a lost
		// notice must not fail the ceremony that succeeded.
		if s.passkeyNotices != nil {
			if noticeErr := s.passkeyNotices.EnqueuePasskeyAddedNotice(ctx, s.passkeyNoticeFrom(account, userID, stored.Name)); noticeErr != nil {
				s.log.WarnContext(ctx, "webauthn: passkey added notice was not queued", "error", noticeErr, "user_id", userID.String())
			}
		}
	}

	return view(stored)
}

// enforceLimits judges both enrollment ceilings: passkey.max_credentials
// against the passkeys the account holds, mfa.max_enrollments against the
// passkeys plus the TOTP devices together. An unreadable limit is the
// fail-closed refusal the reader answered. It runs inside the account's
// lock — the count is only as strong as the serialization around it.
func (s *Service) enforceLimits(ctx context.Context, db datastore.Querier, userID uuid.UUID) error {
	maxCredentials, err := s.limitInt(ctx, SettingMaxCredentials, defaultMaxCredentials)
	if err != nil {
		return err
	}
	held, err := s.repo.CountCredentials(ctx, db, userID)
	if err != nil {
		return err
	}
	if held >= maxCredentials {
		return ErrTooManyPasskeys
	}

	maxEnrollments, err := s.limitInt(ctx, SettingMaxEnrollments, defaultMaxEnrollments)
	if err != nil {
		return err
	}
	if maxEnrollments > held {
		totp := 0
		if s.totpEnrollments != nil {
			totp, err = s.totpEnrollments.CountConfirmedTotp(ctx, db, userID)
			if err != nil {
				return err
			}
		}
		if held+totp >= maxEnrollments {
			return ErrTooManyEnrollments
		}
	}
	return nil
}

// storeCeremony persists one ceremony row from the engine's session state.
// The credential parameters and the extensions travel as the JSON the
// verify step reconstructs the state from; the expiry is the engine's own.
func (s *Service) storeCeremony(ctx context.Context, userID *uuid.UUID, kind, verification string, session *gowebauthn.SessionData) (SessionSchema, error) {
	params, err := json.Marshal(session.CredParams)
	if err != nil {
		return SessionSchema{}, fmt.Errorf("webauthn: credential params: %w", err)
	}
	extensions, err := json.Marshal(session.Extensions)
	if err != nil {
		return SessionSchema{}, fmt.Errorf("webauthn: extensions: %w", err)
	}
	expires := session.Expires
	if expires.IsZero() {
		expires = s.now().Add(ceremonyTTL)
	}
	row := SessionSchema{
		ID:               uuid.NewV7(),
		UserID:           userID,
		Challenge:        session.Challenge,
		ChallengeType:    kind,
		UserVerification: verification,
		CredentialParams: params,
		Extensions:       extensions,
		CreatedAt:        s.now(),
		ExpiresAt:        expires,
	}
	if err := s.repo.CreateSession(ctx, s.pool, row); err != nil {
		return SessionSchema{}, err
	}
	return row, nil
}

// consumeCeremony spends one ceremony row: the read brings the state back,
// the DELETE proves it live and single-use. Two verifies racing on one
// handle both read the row; the DELETE's WHERE lets exactly one through, and
// the loser answers the same invalid-ceremony refusal a replay does.
func (s *Service) consumeCeremony(ctx context.Context, id uuid.UUID) (SessionSchema, bool, error) {
	row, err := s.repo.GetSession(ctx, s.pool, id)
	if errors.Is(err, ErrNoRows) {
		return SessionSchema{}, false, nil
	}
	if err != nil {
		return SessionSchema{}, false, err
	}
	live, err := s.repo.ConsumeSession(ctx, s.pool, id, s.now())
	if err != nil {
		return SessionSchema{}, false, err
	}
	if !live {
		return SessionSchema{}, false, nil
	}
	return row, true, nil
}

// storedSessionData reconstructs the engine's session state from the row and
// the identity the verify path knows — the challenge match does the rest.
func (s *Service) storedSessionData(row SessionSchema, userID []byte) (gowebauthn.SessionData, error) {
	var params []protocol.CredentialParameter
	if err := json.Unmarshal(row.CredentialParams, &params); err != nil {
		return gowebauthn.SessionData{}, fmt.Errorf("webauthn: stored params: %w", err)
	}
	var extensions protocol.SessionExtensions
	if err := json.Unmarshal(row.Extensions, &extensions); err != nil {
		return gowebauthn.SessionData{}, fmt.Errorf("webauthn: stored extensions: %w", err)
	}
	return gowebauthn.SessionData{
		Challenge:        row.Challenge,
		UserID:           userID,
		Expires:          row.ExpiresAt,
		UserVerification: protocol.UserVerificationRequirement(row.UserVerification),
		Extensions:       extensions,
		CredParams:       params,
	}, nil
}

// missingUserVerificationInfo is the DevInfo go-webauthn stamps on the one
// refusal a client can fix by asking the browser to try again: the
// authenticator answered presence without the verification the ceremony
// demanded. Everything else is one invalid-ceremony answer that does not
// explain itself to a potential attacker.
const missingUserVerificationInfo = "User verification required but flag not set by authenticator"

// classifyCeremonyError maps the engine's refusals onto the service's.
func classifyCeremonyError(err error) error {
	var protocolErr *protocol.Error
	if errors.As(err, &protocolErr) && protocolErr.DevInfo == missingUserVerificationInfo {
		return ErrVerificationDue
	}
	return fmt.Errorf("%w: %v", ErrAssertionInvalid, err)
}

// storeCredential writes the verified attestation. The name falls back to
// the catalog's authenticator name when the holder named none — the embedded
// AAGUID manifest answers it — and to a plain word when the AAGUID is
// unknown. It runs on the caller's query surface: under the account's lock
// the limit judgement and the insert are one transaction.
func (s *Service) storeCredential(ctx context.Context, db datastore.Querier, userID uuid.UUID, credential *gowebauthn.Credential, name string) (CredentialSchema, error) {
	if name == "" {
		name = authenticatorName(credential.Authenticator.AAGUID)
	}
	aaguid := formatAAGUID(credential.Authenticator.AAGUID)
	transports := make([]string, 0, len(credential.Transport))
	for _, transport := range credential.Transport {
		transports = append(transports, string(transport))
	}
	transportJSON, err := json.Marshal(transports)
	if err != nil {
		return CredentialSchema{}, fmt.Errorf("webauthn: transports: %w", err)
	}

	row := CredentialSchema{
		ID:              uuid.NewV7(),
		UserID:          userID,
		Name:            name,
		CredentialID:    credential.ID,
		PublicKey:       credential.PublicKey,
		SignCount:       int64(credential.Authenticator.SignCount),
		AttestationType: credential.AttestationType,
		Transport:       transportJSON,
		BackupEligible:  credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
		AAGUID:          &aaguid,
		CreatedAt:       s.now(),
	}
	if err := s.repo.CreateCredential(ctx, db, row); err != nil {
		return CredentialSchema{}, err
	}
	return row, nil
}
