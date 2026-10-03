package webauthn

import (
	"context"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/modules/identity/user"
)

// The administrative door over the credential roll. It holds the same rules
// the holder's own surface does — the not-found that hides another
// account's credential, the stranding refusal at the last way in — with the
// target account named by the request instead of the token's subject. The
// audit record still names the administrator: the recorder lifts the actor
// from the caller in the context.

// AdminListCredentials answers one account's roll, oldest first.
func (s *Service) AdminListCredentials(ctx context.Context, userWire string) ([]View, error) {
	userID, err := user.UUIDFromWire(userWire)
	if err != nil {
		return nil, ErrAccountUnknown
	}
	return s.ListCredentials(ctx, userID)
}

// AdminRenameCredential replaces one of an account's passkey display names.
func (s *Service) AdminRenameCredential(ctx context.Context, userWire, credentialWire, name string) (View, error) {
	userID, err := user.UUIDFromWire(userWire)
	if err != nil {
		return View{}, ErrAccountUnknown
	}
	return s.renameCredential(ctx, userID, credentialWire, name, audit.EventWebauthnCredentialAdminRenamed)
}

// AdminDeleteCredential removes one of an account's passkeys. An account
// left with no way in at all is refused here as it is on the holder's own
// surface — an administrator's key does not waive the recovery anchor.
func (s *Service) AdminDeleteCredential(ctx context.Context, userWire, credentialWire string) error {
	userID, err := user.UUIDFromWire(userWire)
	if err != nil {
		return ErrAccountUnknown
	}
	return s.deleteCredential(ctx, userID, credentialWire, audit.EventWebauthnCredentialAdminRemoved)
}
