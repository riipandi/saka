package identity

import (
	"context"

	"uuid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/modules/identity/multifactor"
	"github.com/riipandi/saka/modules/identity/password"
	"github.com/riipandi/saka/modules/identity/session"
	"github.com/riipandi/saka/modules/identity/verification"
)

// accountAdoption composes the credential features' transactional removals
// into the one operation the OAuth resolution asks for. The OAuth feature
// owns the decision and none of the tables; each feature below deletes what
// it owns.
//
// API keys, passkeys and live sessions are not credentials an unverified
// account can hold — sign-in refuses it until the address is confirmed, and
// both the key surface and the passkey ceremony need a session — so the
// sessions are revoked as a precaution and nothing else is listed.
type accountAdoption struct {
	verification *verification.Service
	passwords    *password.Service
	factors      *multifactor.Service
	sessions     *session.Service
}

func (a accountAdoption) AdoptUnverified(ctx context.Context, tx datastore.Querier, userID uuid.UUID) (bool, error) {
	moved, err := a.verification.StampVerifiedByProvider(ctx, tx, userID)
	if err != nil || !moved {
		return false, err
	}
	if err := a.passwords.DiscardCredentials(ctx, tx, userID); err != nil {
		return false, err
	}
	if err := a.factors.DiscardFactors(ctx, tx, userID); err != nil {
		return false, err
	}
	if _, err := a.sessions.RevokeAllForUser(ctx, tx, userID); err != nil {
		return false, err
	}
	return true, nil
}
