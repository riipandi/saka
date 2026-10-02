package oauthsso

import (
	"context"
	"errors"

	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
)

// The failures the account surfaces report. The handler maps them onto
// the connect codes.
var (
	// ErrLinkedAccountNotFound is a linked account identifier that
	// names no binding of the calling account. A foreign binding
	// answers the same — the refusal says nothing about whose it is.
	ErrLinkedAccountNotFound = errors.New("oauthsso: no linked account answers this identifier")

	// ErrLastCredential is the unlink the stranding rule refuses: the
	// binding is the account's last way in — no other binding, no
	// password, no passkey — and removing it would strand the account.
	// The way back in is one procedure away, and the refusal names it.
	ErrLastCredential = errors.New("oauthsso: set a password before unlinking the last provider")
)

// ListLinkedAccounts reads the calling account's bindings, oldest
// first. The secrets the rows carry — the provider's tokens — are the
// feature's own sealing business: the listing is the holder's view, and
// no answer carries them.
func (s *Service) ListLinkedAccounts(ctx context.Context, userID uuid.UUID) ([]LinkedAccountView, error) {
	return s.repo.LinkedAccountsByUser(ctx, s.pool, userID)
}

// UnlinkLinkedAccount removes one of the calling account's bindings.
// The stranding rule runs before the removal: a binding that is the
// account's last credential — no other binding, no password, no passkey
// — is refused until the holder sets a password, and the refusal is the
// feature's own word, not a permission error. The removal and its audit
// record commit as one transaction, and the soft-delete trigger
// captures the row the unlink removed.
func (s *Service) UnlinkLinkedAccount(ctx context.Context, userID, linkedID uuid.UUID) error {
	binding, err := s.repo.LinkedAccountByID(ctx, s.pool, linkedID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrLinkedAccountNotFound
	}
	if err != nil {
		return err
	}
	// A foreign binding is a missing one: the same answer keeps the
	// procedure from naming whose bindings exist.
	if binding.LinkedAccount.UserID != userID {
		return ErrLinkedAccountNotFound
	}

	keeps, err := s.repo.KeepsAnotherCredential(ctx, s.pool, userID, linkedID)
	if err != nil {
		return err
	}
	if !keeps {
		return ErrLastCredential
	}

	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		removed, err := s.repo.DeleteLinkedAccount(ctx, tx, linkedID, userID)
		if err != nil {
			return err
		}
		if !removed {
			// A concurrent unlink won the row; the account's answer is
			// the same not-found a later call would earn.
			return ErrLinkedAccountNotFound
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventOauthSsoAccountUnlinked,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"provider":            binding.Provider,
				"provider_account_id": binding.LinkedAccount.ProviderAccountID,
				"oauth_linked_id":     wireLinkedID(linkedID),
			},
		})
		return nil
	})
}

// wireLinkedID renders the binding's identifier in its wire form for
// the audit payload. A render that fails carries the raw UUID — the
// record must not fail the write it rides.
func wireLinkedID(id uuid.UUID) string {
	wire, err := LinkedAccountIDFromUUID(id)
	if err != nil {
		return id.String()
	}
	return wire.String()
}
