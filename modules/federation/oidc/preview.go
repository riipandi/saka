package oidc

import (
	"context"
	"encoding/json/v2"
	"errors"
	"time"
	"uuid"

	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/modules/identity/usergroup"
)

// UserDirectory is the account facts the preview builds its claims from.
// The user feature implements it — *user.Service satisfies the method set
// as it stands — and the area wires it post-construction, so the protocol
// feature never reaches the account feature's own surface.
type UserDirectory interface {
	GetUser(ctx context.Context, id string) (user.UserView, error)
}

// ClaimSource is the extra claims the tokens carry: the operator-defined
// rows the customclaim feature owns, merged into the claims the account and
// its groups produce. The consuming feature owns the interface; the
// customclaim service reaches it through the area's adapter.
type ClaimSource interface {
	// UserClaims answers the claims an account carries.
	UserClaims(ctx context.Context, userID uuid.UUID) ([]Claim, error)
	// GroupClaims answers the claims the account's groups carry, merged.
	GroupClaims(ctx context.Context, groupIDs []uuid.UUID) ([]Claim, error)
}

// Claim is one operator-defined claim: the key the token names it by and
// the value it carries.
type Claim struct {
	Key   string
	Value string
}

// ErrPreviewUnknownUser is a preview whose account names no account. It is
// its own refusal, because the client is known and the account is not — the
// two facts answer different questions.
var ErrPreviewUnknownUser = errors.New("oidc: the account does not exist")

// accessTokenSeconds is the window the preview's access-token map shows. The
// preview describes a grant that has not been asked for, so it uses the
// default window rather than pretending to know the grant's.
const accessTokenSeconds = DefaultAccessTokenMinutes * 60

// Preview answers the claim maps a client would receive for one account —
// id token, access token, userinfo — built from the account as it stands.
// No token is minted, and none of the claims are secrets: they are the
// facts the account's own views already answer with, named the way the
// protocol names them.
//
// The custom claims an operator hangs on the account and its groups join
// when the customclaim feature does; today the maps carry the account's own
// fields.
func (s *Service) Preview(ctx context.Context, clientID, wireUserID string) (map[string]any, map[string]any, map[string]any, error) {
	if s.users == nil {
		return nil, nil, nil, ErrClientNotFound
	}
	if _, err := s.Get(ctx, clientID); err != nil {
		return nil, nil, nil, err
	}
	account, err := s.users.GetUser(ctx, wireUserID)
	if errors.Is(err, user.ErrUserNotFound) {
		return nil, nil, nil, ErrPreviewUnknownUser
	}
	if err != nil {
		return nil, nil, nil, err
	}

	profile := map[string]any{
		"sub":                account.ID,
		"preferred_username": account.Username,
		"name":               account.DisplayName,
		"email":              account.Email,
		"email_verified":     account.EmailVerified,
		"groups":             groupNames(account.Groups),
	}
	if account.FirstName != nil && *account.FirstName != "" {
		profile["given_name"] = *account.FirstName
	}
	if account.LastName != nil && *account.LastName != "" {
		profile["family_name"] = *account.LastName
	}

	now := s.now()
	access := map[string]any{
		"iss":       s.baseURL,
		"aud":       clientID,
		"client_id": clientID,
		"scp":       []string{"openid", "profile", "email", "groups"},
		"iat":       now.Unix(),
		"exp":       now.Add(time.Duration(accessTokenSeconds) * time.Second).Unix(),
	}
	if mergeErr := s.mergeCustomClaims(ctx, profile, account); mergeErr != nil {
		return nil, nil, nil, mergeErr
	}
	return profile, access, profile, nil
}

// mergeCustomClaims folds the operator-defined claims — the account's own
// and its groups' — into the profile map. A value that parses as JSON
// travels as the document it names, the way the token surface will carry
// it; a value that does not stays the string it is.
func (s *Service) mergeCustomClaims(ctx context.Context, profile map[string]any, account user.UserView) error {
	if s.claims == nil {
		return nil
	}

	extra := make([]Claim, 0, 8)
	accountID, err := user.UUIDFromWire(account.ID)
	if err != nil {
		return err
	}
	own, err := s.claims.UserClaims(ctx, accountID)
	if err != nil {
		return err
	}
	extra = append(extra, own...)

	if len(account.Groups) > 0 {
		groupIDs := make([]uuid.UUID, 0, len(account.Groups))
		for _, group := range account.Groups {
			id, err := usergroup.UUIDFromWire(group.ID)
			if err != nil {
				continue
			}
			groupIDs = append(groupIDs, id)
		}
		grouped, err := s.claims.GroupClaims(ctx, groupIDs)
		if err != nil {
			return err
		}
		extra = append(extra, grouped...)
	}

	for _, claim := range extra {
		if _, protected := protectedClaimKeys[claim.Key]; protected {
			continue
		}
		var jsonValue any
		if err := json.Unmarshal([]byte(claim.Value), &jsonValue); err == nil {
			profile[claim.Key] = jsonValue
		} else {
			profile[claim.Key] = claim.Value
		}
	}
	return nil
}

// groupNames flattens the account's memberships into the claim the protocol
// carries: the group names, ordered as the account view orders them.
func groupNames(groups []user.GroupSummary) []string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		names = append(names, group.Name)
	}
	return names
}
