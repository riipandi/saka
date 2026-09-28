package oidc

import (
	"context"
	"errors"
	"time"

	"github.com/riipandi/tango/modules/identity/user"
)

// UserDirectory is the account facts the preview builds its claims from.
// The user feature implements it — *user.Service satisfies the method set
// as it stands — and the area wires it post-construction, so the protocol
// feature never reaches the account feature's own surface.
type UserDirectory interface {
	GetUser(ctx context.Context, id string) (user.UserView, error)
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
	return profile, access, profile, nil
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
