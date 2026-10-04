package oauthsso

import (
	"context"
	"errors"
	"time"

	"uuid"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
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

// LinkedAccountTokens is the holder's read of one binding's provider
// tokens, opened from their seal for this one answer. A token the
// provider never minted answers empty — the empty is the fact, not an
// error — and an expiry the provider did not name answers absent.
type LinkedAccountTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
	ConnectionID uuid.UUID
	Provider     string
}

// GetLinkedAccountTokens reads the calling account's binding and opens
// the tokens it carries. A foreign binding is a missing one — the same
// `not_found` the unlink answers keeps the procedure from naming whose
// bindings exist — and the read touches no write.
func (s *Service) GetLinkedAccountTokens(ctx context.Context, userID, linkedID uuid.UUID) (LinkedAccountTokens, error) {
	binding, err := s.repo.LinkedAccountByID(ctx, s.pool, linkedID)
	if errors.Is(err, datastore.ErrNoRows) {
		return LinkedAccountTokens{}, ErrLinkedAccountNotFound
	}
	if err != nil {
		return LinkedAccountTokens{}, err
	}
	if binding.LinkedAccount.UserID != userID {
		return LinkedAccountTokens{}, ErrLinkedAccountNotFound
	}

	access, err := s.openToken(binding.LinkedAccount.AccessToken)
	if err != nil {
		return LinkedAccountTokens{}, err
	}
	refresh, err := s.openToken(binding.LinkedAccount.RefreshToken)
	if err != nil {
		return LinkedAccountTokens{}, err
	}
	return LinkedAccountTokens{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    binding.LinkedAccount.AccessExpiresAt,
		ConnectionID: binding.LinkedAccount.ConnectionID,
		Provider:     binding.Provider,
	}, nil
}

// openToken opens one sealed token column. The empty column is the
// provider that minted nothing — it answers empty, the fact the row
// stores, rather than a seal error.
func (s *Service) openToken(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	return s.unseal(sealed)
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
