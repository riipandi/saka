package oidc

import (
	"testing"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The expiry filter on protocol lookups: a row whose expiry the database
// judges dead answers not-found even before the sweep reaps it, a live
// row answers whole, and a row whose expiry is NULL — the shape a
// logout session without a client and a grant without a refresh window
// carry — is never refused by the column.

func TestExpiredProtocolRowsFailClosedAtTheLookup(t *testing.T) {
	pool := migratedPool(t)
	owner := seedAccount(t, pool, "langdon")
	service := testService(t, pool)
	issued, err := service.Create(t.Context(), owner, createParams("Expiry Portal"))
	require.NoError(t, err)
	clientID := issued.Client.ID

	expired := int(time.Now().Add(-time.Hour).Unix())
	live := int(time.Now().Add(time.Hour).Unix())

	grants := grantStore{protocolStore: protocolStore{pool: pool}}
	authn := authnStore{protocolStore: protocolStore{pool: pool}}
	devices := deviceStore{protocolStore: protocolStore{pool: pool}}
	logouts := logoutStore{protocolStore: protocolStore{pool: pool}}

	// The grant kind carries the refresh window as its column expiry; a
	// grant without one expires NULL and stays loadable.
	require.NoError(t, grants.SaveGrant(t.Context(), &goidc.Grant{
		ID: "g-null", ClientID: clientID, Subject: "s", Username: "langdon",
		Scopes: "openid", CreatedAt: expired,
	}))
	_, err = grants.Grant(t.Context(), "g-null")
	require.NoError(t, err, "a grant with no refresh window is never judged by the column")

	// Each pointer kind: an expired pointer is a not-found, a live one
	// resolves. The grant row rides the same column, so an expired
	// pointer's grant is refused with it.
	for _, tc := range []struct {
		name   string
		seed   func(suffix string, expiry int)
		lookup func(t *testing.T, suffix string)
	}{
		{
			name: "authcode",
			seed: func(suffix string, expiry int) {
				require.NoError(t, grants.SaveGrant(t.Context(), &goidc.Grant{
					ID: "g-ac-" + suffix, ClientID: clientID, Subject: "s", Username: "langdon",
					Scopes: "openid", CreatedAt: expiry,
					AuthCode: "code-" + suffix, AuthCodeExpiresAt: expiry,
				}))
			},
			lookup: func(t *testing.T, suffix string) {
				_, err := grants.GrantByAuthCode(t.Context(), "code-"+suffix)
				assert.Equal(t, goidc.ErrNotFound, err)
			},
		},
		{
			name: "refresh",
			seed: func(suffix string, expiry int) {
				require.NoError(t, grants.SaveGrant(t.Context(), &goidc.Grant{
					ID: "g-rt-" + suffix, ClientID: clientID, Subject: "s", Username: "langdon",
					Scopes: "openid", CreatedAt: expiry,
					RefreshToken: "rt-" + suffix, RefreshTokenExpiresAt: expiry,
				}))
			},
			lookup: func(t *testing.T, suffix string) {
				_, err := grants.GrantByRefreshToken(t.Context(), "rt-"+suffix)
				assert.Equal(t, goidc.ErrNotFound, err)
			},
		},
		{
			name: "devicecode",
			seed: func(suffix string, expiry int) {
				require.NoError(t, grants.SaveGrant(t.Context(), &goidc.Grant{
					ID: "g-dc-" + suffix, ClientID: clientID, Subject: "s", Username: "langdon",
					Scopes: "openid", CreatedAt: expiry,
					DeviceCode: "dc-" + suffix, DeviceCodeExpiresAt: expiry,
				}))
			},
			lookup: func(t *testing.T, suffix string) {
				_, err := grants.GrantByDeviceCode(t.Context(), "dc-"+suffix)
				assert.Equal(t, goidc.ErrNotFound, err)
			},
		},
		{
			name: "par",
			seed: func(suffix string, expiry int) {
				require.NoError(t, authn.SaveSession(t.Context(), &goidc.AuthnSession{
					ID: "as-par-" + suffix, ClientID: clientID, ExpiresAt: expiry,
					PushedAuthReqID: "par-" + suffix,
				}))
			},
			lookup: func(t *testing.T, suffix string) {
				_, err := authn.SessionByPushedAuthReqID(t.Context(), "par-"+suffix)
				assert.Equal(t, goidc.ErrNotFound, err)
			},
		},
		{
			name: "authn",
			seed: func(suffix string, expiry int) {
				require.NoError(t, authn.SaveSession(t.Context(), &goidc.AuthnSession{
					ID: "as-" + suffix, ClientID: clientID, ExpiresAt: expiry,
				}))
			},
			lookup: func(t *testing.T, suffix string) {
				_, err := authn.Session(t.Context(), "as-"+suffix)
				assert.Equal(t, goidc.ErrNotFound, err)
			},
		},
		{
			name: "device",
			seed: func(suffix string, expiry int) {
				require.NoError(t, devices.SaveSession(t.Context(), &goidc.AuthnSession{
					ID: "ds-" + suffix, ClientID: clientID, ExpiresAt: expiry,
					DeviceCode: "dsd-" + suffix, UserCode: "uc-" + suffix,
				}))
			},
			lookup: func(t *testing.T, suffix string) {
				_, err := devices.SessionByUserCode(t.Context(), "uc-"+suffix)
				assert.Equal(t, goidc.ErrNotFound, err)
				_, err = devices.SessionByDeviceCode(t.Context(), "dsd-"+suffix)
				assert.Equal(t, goidc.ErrNotFound, err)
				_, err = devices.Session(t.Context(), "ds-"+suffix)
				assert.Equal(t, goidc.ErrNotFound, err)
			},
		},
		{
			name: "logout",
			seed: func(suffix string, expiry int) {
				require.NoError(t, logouts.SaveLogoutSession(t.Context(), &goidc.LogoutSession{
					ID: "ls-" + suffix, ExpiresAt: expiry,
				}))
			},
			lookup: func(t *testing.T, suffix string) {
				_, err := logouts.LogoutSession(t.Context(), "ls-"+suffix)
				assert.Equal(t, goidc.ErrNotFound, err)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.seed("expired", expired)
			tc.seed("live", live)

			t.Run("expired", func(t *testing.T) { tc.lookup(t, "expired") })
			t.Run("live", func(t *testing.T) {
				// The live twin must still resolve — the filter refuses
				// only what the column judges dead.
				switch tc.name {
				case "authcode":
					_, err := grants.GrantByAuthCode(t.Context(), "code-live")
					require.NoError(t, err)
				case "refresh":
					_, err := grants.GrantByRefreshToken(t.Context(), "rt-live")
					require.NoError(t, err)
				case "devicecode":
					_, err := grants.GrantByDeviceCode(t.Context(), "dc-live")
					require.NoError(t, err)
				case "par":
					_, err := authn.SessionByPushedAuthReqID(t.Context(), "par-live")
					require.NoError(t, err)
				case "authn":
					_, err := authn.Session(t.Context(), "as-live")
					require.NoError(t, err)
				case "device":
					_, err := devices.SessionByUserCode(t.Context(), "uc-live")
					require.NoError(t, err)
				case "logout":
					_, err := logouts.LogoutSession(t.Context(), "ls-live")
					require.NoError(t, err)
				}
			})
		})
	}
}

func TestAnExpiryWithinTheSkewAllowanceStillAnswers(t *testing.T) {
	pool := migratedPool(t)
	owner := seedAccount(t, pool, "langdon")
	service := testService(t, pool)
	issued, err := service.Create(t.Context(), owner, createParams("Skew Portal"))
	require.NoError(t, err)

	// A row whose expiry sits inside the clock-skew allowance — just past
	// now, not past now plus the allowance — still answers: a lookup
	// must not refuse what a skewed clock still considers live.
	justPast := int(time.Now().Add(-30 * time.Second).Unix())
	grants := grantStore{protocolStore: protocolStore{pool: pool}}
	require.NoError(t, grants.SaveGrant(t.Context(), &goidc.Grant{
		ID: "g-skew", ClientID: issued.Client.ID, Subject: "s", Username: "langdon",
		Scopes: "openid", CreatedAt: justPast,
		RefreshToken: "rt-skew", RefreshTokenExpiresAt: justPast,
	}))
	_, err = grants.GrantByRefreshToken(t.Context(), "rt-skew")
	require.NoError(t, err, "a row within the skew allowance answers")
}
