package oidc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"

	"github.com/riipandi/saka/modules/identity/user"
)

// accountQualifies answers whether the grant's account may still hold tokens
// for the grant's client: the account exists, is not disabled, is outside a
// ban window, and — for a group-restricted client — still belongs to an
// allowed group. The authorization flow judges the same facts once, at
// consent; a refresh or a userinfo call is where a change since then lands.
//
// An error is a read that failed, not a verdict: the caller must not treat it
// as a refusal that ends the grant.
func (s *Service) accountQualifies(ctx context.Context, grant *goidc.Grant) (bool, error) {
	wire, err := user.IDFromUUIDString(grant.Subject)
	if err != nil {
		return false, nil
	}
	view, err := s.users.GetUser(ctx, wire.String())
	if errors.Is(err, user.ErrUserNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("oidc: read the grant's account: %w", err)
	}
	if view.Disabled || (view.BannedAt != nil && (view.BanExpires == nil || view.BanExpires.After(time.Now()))) {
		return false, nil
	}

	client, err := s.Get(ctx, grant.ClientID)
	if errors.Is(err, ErrClientNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("oidc: read the grant's client: %w", err)
	}
	return s.accountAdmitted(ctx, grant.Subject, grant.ClientID, client.IsGroupRestricted || len(client.AllowedGroups) > 0)
}

// refuseDisqualifiedAccount is the refresh half of that check: a grant whose
// account no longer qualifies is deleted and the refresh is refused with
// invalid_grant, so the token the account's earlier standing minted stops
// being renewable. Access tokens already issued live to their own expiry.
func refuseDisqualifiedAccount(ctx context.Context, service *Service, grantType goidc.GrantType, grant *goidc.Grant) error {
	if grantType != goidc.GrantRefreshToken {
		return nil
	}
	qualifies, err := service.accountQualifies(ctx, grant)
	if err != nil {
		return err
	}
	if qualifies {
		return nil
	}
	if err := (grantStore{protocolStore: protocolStore{pool: service.pool}}).revokeChain(ctx, grant.ID); err != nil {
		return err
	}
	return goidc.WrapError(goidc.ErrorCodeInvalidGrant, "invalid grant",
		errors.New("the account no longer qualifies for this client"))
}
