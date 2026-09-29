package oidc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The refresh windows: the standard one and the offline_access one ride
// the settings, read at every issuance and rotation, and the offline
// scope picks the long window. Zero hours is the never-expiring token.

// staticWindows is the source a window test pins.
type staticWindows struct {
	standard int
	offline  int
	err      error
}

func (s staticWindows) RefreshTokenHours(context.Context) (int, error) {
	return s.standard, s.err
}

func (s staticWindows) OfflineRefreshTokenHours(context.Context) (int, error) {
	return s.offline, s.err
}

func TestTheRefreshWindowFollowsTheScope(t *testing.T) {
	service := testService(t, migratedPool(t)).WithRefreshWindowSource(staticWindows{standard: 336, offline: 720})
	now := int(time.Now().Unix())

	// The offline scope picks the long window.
	offline := goidc.Grant{ID: "g-off", Scopes: "openid offline_access", ClientID: "c", Subject: "s", CreatedAt: now}
	require.NoError(t, stampRefreshWindow(t.Context(), service, goidc.GrantAuthorizationCode, &offline))
	assert.Equal(t, now+720*3600, offline.RefreshTokenExpiresAt,
		"the offline_access grant rides the long window")

	// The plain grant rides the standard one.
	plain := goidc.Grant{ID: "g-plain", Scopes: "openid profile", ClientID: "c", Subject: "s", CreatedAt: now}
	require.NoError(t, stampRefreshWindow(t.Context(), service, goidc.GrantAuthorizationCode, &plain))
	assert.Equal(t, now+336*3600, plain.RefreshTokenExpiresAt)

	// The rotation re-stamps the same way: a grant whose scopes carry
	// offline_access keeps the long window through its rotations.
	require.NoError(t, stampRefreshWindow(t.Context(), service, goidc.GrantRefreshToken, &offline))
	assert.Equal(t, now+720*3600, offline.RefreshTokenExpiresAt)
}

func TestAZeroWindowIsTheNeverExpiringToken(t *testing.T) {
	service := testService(t, migratedPool(t)).WithRefreshWindowSource(staticWindows{standard: 0, offline: 0})
	grant := goidc.Grant{ID: "g", Scopes: "openid", ClientID: "c", Subject: "s", CreatedAt: 1000, RefreshTokenExpiresAt: 5000}

	require.NoError(t, stampRefreshWindow(t.Context(), service, goidc.GrantAuthorizationCode, &grant))
	assert.Zero(t, grant.RefreshTokenExpiresAt,
		"zero hours is the historical never-expiring token, not an instant expiry")
}

func TestAnUnreadableWindowFailsOpenToNeverExpiring(t *testing.T) {
	service := testService(t, migratedPool(t)).WithRefreshWindowSource(
		staticWindows{err: errors.New("settings: unreadable")})
	grant := goidc.Grant{ID: "g", Scopes: "openid", ClientID: "c", Subject: "s", CreatedAt: 1000}

	require.NoError(t, stampRefreshWindow(t.Context(), service, goidc.GrantAuthorizationCode, &grant))
	assert.Zero(t, grant.RefreshTokenExpiresAt,
		"an unreadable setting is the never-expiring token, not a broken issuance")
}

func TestAnUnwiredWindowSourceKeepsTheHistoricalBehavior(t *testing.T) {
	service := testService(t, migratedPool(t))
	grant := goidc.Grant{ID: "g", Scopes: "openid", ClientID: "c", Subject: "s", CreatedAt: 1000}

	require.NoError(t, stampRefreshWindow(t.Context(), service, goidc.GrantAuthorizationCode, &grant))
	assert.Zero(t, grant.RefreshTokenExpiresAt)
}

func TestOtherGrantTypesAreUntouched(t *testing.T) {
	service := testService(t, migratedPool(t)).WithRefreshWindowSource(staticWindows{standard: 336, offline: 720})
	grant := goidc.Grant{ID: "g", Scopes: "", ClientID: "c", Subject: "c", CreatedAt: 1000, RefreshTokenExpiresAt: 9999}

	require.NoError(t, stampRefreshWindow(t.Context(), service, goidc.GrantClientCredentials, &grant))
	assert.Equal(t, 9999, grant.RefreshTokenExpiresAt,
		"a client-credentials grant carries no refresh token; the stamp never touches it")
}
