package webauthn

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"

	"uuid"

	"github.com/go-webauthn/webauthn/protocol"
	gowebauthn "github.com/go-webauthn/webauthn/webauthn"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/signin"
)

// BeginLogin opens the sign-in ceremony without naming an account. The
// options carry an empty allowCredentials, so the browser offers every
// discoverable credential it holds for this origin, and the account resolves
// from the credential the holder picks.
func (s *Service) BeginLogin(ctx context.Context) (string, string, error) {
	verification, err := s.userVerification(ctx)
	if err != nil {
		return "", "", err
	}

	assertion, session, err := s.engine.BeginDiscoverableLogin(
		gowebauthn.WithUserVerification(protocol.UserVerificationRequirement(verification)),
	)
	if err != nil {
		return "", "", fmt.Errorf("webauthn: begin login: %w", err)
	}

	row, err := s.storeCeremony(ctx, nil, ChallengeTypeAuthentication, verification, session)
	if err != nil {
		return "", "", err
	}

	options, err := json.Marshal(assertion)
	if err != nil {
		return "", "", fmt.Errorf("webauthn: assertion options: %w", err)
	}
	handle, err := SessionIDFromUUID(row.ID)
	if err != nil {
		return "", "", fmt.Errorf("webauthn: session id: %w", err)
	}
	return string(options), handle.String(), nil
}

// VerifyLogin finishes the sign-in. The ceremony row is consumed first —
// single use is the DELETE's WHERE — then the assertion is verified with the
// account resolving from the response's user handle, which is the account's
// UUID the enrollment wrote into the credential.
//
// The session opens in one step: a passkey assertion with user verification
// is full authentication, so an MFA-enabled account answers tokens here, not
// a second challenge. The assertion bookkeeping — the advanced counter, the
// backup state, the last-use stamp — commits with the session, so a rollback
// leaves the credential's state exactly as the assertion found it.
func (s *Service) VerifyLogin(ctx context.Context, sessionWire, credentialJSON string, params signin.SessionParams) (IssuedSession, error) {
	sessionID, err := typeidParseSession(sessionWire)
	if err != nil {
		return IssuedSession{}, ErrCeremonyInvalid
	}
	row, live, err := s.consumeCeremony(ctx, sessionID)
	if err != nil {
		return IssuedSession{}, err
	}
	if !live {
		return IssuedSession{}, ErrCeremonyInvalid
	}

	parsed, err := protocolParseAssertion(credentialJSON)
	if err != nil {
		return IssuedSession{}, err
	}

	sessionData, err := s.storedSessionData(row, nil)
	if err != nil {
		return IssuedSession{}, err
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
		return IssuedSession{}, classifyCeremonyError(err)
	}
	if account == nil || credential == nil {
		return IssuedSession{}, fmt.Errorf("%w: the assertion resolved to no account", ErrAssertionInvalid)
	}
	if credential.Authenticator.CloneWarning {
		// The counter went backwards: this credential exists twice, and
		// one of the two holders is not the holder. The assertion records
		// nothing — the stored counter stays where it was, so the clone
		// stays detectable on the next attempt too.
		return IssuedSession{}, ErrClonedCredential
	}

	stored, storedErr := s.repo.GetCredentialByCredentialID(ctx, s.pool, credential.ID)
	if storedErr != nil {
		return IssuedSession{}, storedErr
	}

	var result IssuedSession
	txErr := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if recordErr := s.repo.RecordAssertion(ctx, tx, stored.ID, int64(credential.Authenticator.SignCount), credential.Flags.BackupState, s.now()); recordErr != nil {
			return recordErr
		}
		var issueErr error
		result, issueErr = s.issuer.IssueSession(ctx, tx, account, signin.ProviderWebauthn, audit.EventWebauthnSignIn, params)
		return issueErr
	})
	if txErr != nil {
		return IssuedSession{}, txErr
	}
	return result, nil
}

// protocolParseAssertion reads the browser's assertion JSON into the parsed
// form the engine verifies.
func protocolParseAssertion(credentialJSON string) (*protocol.ParsedCredentialAssertionData, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBody(strings.NewReader(credentialJSON))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAssertionInvalid, err)
	}
	return parsed, nil
}

// engineCredentials maps the account's stored rows into the engine's
// credential shapes — the form ValidateDiscoverableLogin compares an
// assertion against, signature counter included.
func (s *Service) engineCredentials(ctx context.Context, userID uuid.UUID) ([]gowebauthn.Credential, error) {
	rows, err := s.repo.ListCredentials(ctx, s.pool, userID)
	if err != nil {
		return nil, err
	}
	creds := make([]gowebauthn.Credential, 0, len(rows))
	for _, row := range rows {
		mapped, mapErr := toEngineCredential(row)
		if mapErr != nil {
			return nil, mapErr
		}
		creds = append(creds, mapped)
	}
	return creds, nil
}
