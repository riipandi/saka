package oauthsso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// ErrTokenRejected is the provider's own word that the grant it was
// handed is dead — an `invalid_grant` answer, the RFC 6749 error code
// for a refresh token that was revoked, expired, or rotated away. It is
// the one token-client failure that means something about the identity:
// every other failure is this run's transport, not the holder's standing.
var ErrTokenRejected = errors.New("oauthsso: the provider rejected the token grant")

// tokenGrant is the token endpoint's JSON answer, the fields the refresh
// grant carries. A provider that rotates its refresh tokens answers a new
// one; one that keeps them answers none, and the stored refresh token
// stays the binding's.
type tokenGrant struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

// rotatedTokens is what a successful refresh hands back: the sealed pair
// and the expiry, exactly the shape the binding's token columns store.
type rotatedTokens struct {
	SealedAccessToken  string
	SealedRefreshToken string
	AccessExpiresAt    *time.Time
}

// errNoRefresh is the answer for a binding that carries no refresh token:
// there is nothing to present, and the caller answers the stored tokens.
var errNoRefresh = errors.New("oauthsso: the binding carries no refresh token")

// refreshTokens presents the binding's refresh token to the connection's
// token endpoint with the client credentials, and hands back the rotated
// pair sealed for the write. The poster and the endpoint come from the
// connection's own adapter; a connection whose adapter names no token
// endpoint — or a binding without a refresh token — is a no the caller
// answers without an attempt.
func (s *Service) refreshTokens(ctx context.Context, conn Connection, sealedRefresh string) (rotatedTokens, error) {
	if sealedRefresh == "" {
		return rotatedTokens{}, errNoRefresh
	}
	adapter, err := s.providerFor(conn)
	if err != nil {
		return rotatedTokens{}, err
	}
	endpoint := adapter.TokenEndpoint(conn)
	if endpoint == "" || s.tokens == nil {
		return rotatedTokens{}, errNoRefresh
	}

	refresh, err := s.unseal(sealedRefresh)
	if err != nil {
		return rotatedTokens{}, err
	}
	secret, err := s.unseal(conn.ClientSecret)
	if err != nil {
		return rotatedTokens{}, err
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {conn.ClientID},
		"client_secret": {secret},
	}
	status, body, err := s.tokens.PostForm(ctx, endpoint, form)
	if err != nil {
		return rotatedTokens{}, fmt.Errorf("oauthsso: the token endpoint did not answer: %w", err)
	}

	var grant tokenGrant
	if unmarshalErr := json.Unmarshal(body, &grant); unmarshalErr != nil {
		return rotatedTokens{}, fmt.Errorf("oauthsso: the token endpoint answered no grant document (status %d): %w", status, unmarshalErr)
	}
	if grant.Error == "invalid_grant" {
		return rotatedTokens{}, ErrTokenRejected
	}
	if status != 200 || grant.AccessToken == "" {
		return rotatedTokens{}, fmt.Errorf("oauthsso: the token endpoint refused the grant (status %d, error %q)", status, grant.Error)
	}

	sealedAccess, err := s.seal(grant.AccessToken)
	if err != nil {
		return rotatedTokens{}, err
	}
	rotated := rotatedTokens{
		SealedAccessToken:  sealedAccess,
		SealedRefreshToken: sealedRefresh,
	}
	if grant.RefreshToken != "" {
		sealedRotated, sealErr := s.seal(grant.RefreshToken)
		if sealErr != nil {
			return rotatedTokens{}, sealErr
		}
		rotated.SealedRefreshToken = sealedRotated
	}
	if grant.ExpiresIn > 0 {
		expires := s.now().Add(time.Duration(grant.ExpiresIn) * time.Second)
		rotated.AccessExpiresAt = &expires
	}
	return rotated, nil
}
