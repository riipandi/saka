package oidc

import (
	"testing"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/require"
)

// The protocol stores' contract: the objects round trip through the
// oauth2_sessions rows, the pointers resolve the presented credentials,
// and an unknown handle is the library's not-found.

func TestTheGrantStoreRoundTripsAndResolvesTheTokens(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	issued, err := service.Create(t.Context(), seedAccount(t, pool, "hermione"), createParams("Gryffindor"))
	require.NoError(t, err)

	store := grantStore{protocolStore: protocolStore{pool: pool}}
	grant := &goidc.Grant{
		ID:                    "grant-1",
		CreatedAt:             int(time.Now().Unix()),
		Subject:               "user_hermione",
		ClientID:              issued.Client.ID,
		Username:              "hermione",
		Scopes:                "openid profile",
		RefreshToken:          "refresh-plain",
		RefreshTokenExpiresAt: int(time.Now().Add(time.Hour).Unix()),
		AuthCode:              "code-plain",
		AuthCodeExpiresAt:     int(time.Now().Add(time.Minute).Unix()),
	}
	require.NoError(t, store.SaveGrant(t.Context(), grant))

	loaded, err := store.Grant(t.Context(), grant.ID)
	require.NoError(t, err)
	require.Equal(t, "user_hermione", loaded.Subject)
	require.Equal(t, "openid profile", loaded.Scopes)

	byCode, err := store.GrantByAuthCode(t.Context(), "code-plain")
	require.NoError(t, err)
	require.Equal(t, grant.ID, byCode.ID)

	byRefresh, err := store.GrantByRefreshToken(t.Context(), "refresh-plain")
	require.NoError(t, err)
	require.Equal(t, grant.ID, byRefresh.ID)

	// The plain tokens never sit in the row: the pointers are their only
	// database presence.
	_, err = store.Grant(t.Context(), "unknown")
	require.ErrorIs(t, err, goidc.ErrNotFound)

	// A consumed code loses its pointer, so the replay is a not-found.
	grant.AuthCodeConsumedAt = int(time.Now().Unix())
	require.NoError(t, store.SaveGrant(t.Context(), grant))
	_, err = store.GrantByAuthCode(t.Context(), "code-plain")
	require.ErrorIs(t, err, goidc.ErrNotFound)
}

func TestTheAuthnStoreResolvesTheSessionAndThePARHandle(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	issued, err := service.Create(t.Context(), seedAccount(t, pool, "sophie"), createParams("Sophie"))
	require.NoError(t, err)

	store := authnStore{protocolStore: protocolStore{pool: pool}}
	session := &goidc.AuthnSession{
		ID:       "session-1",
		Status:   goidc.StatusPending,
		Subject:  "user_sophie",
		ClientID: issued.Client.ID,
		AuthorizationParameters: goidc.AuthorizationParameters{
			Scopes: "openid",
		},
		ExpiresAt:       int(time.Now().Add(time.Minute).Unix()),
		PushedAuthReqID: "par-1",
	}
	require.NoError(t, store.SaveSession(t.Context(), session))

	loaded, err := store.Session(t.Context(), "session-1")
	require.NoError(t, err)
	require.Equal(t, goidc.StatusPending, loaded.Status)

	byPAR, err := store.SessionByPushedAuthReqID(t.Context(), "par-1")
	require.NoError(t, err)
	require.Equal(t, "session-1", byPAR.ID)

	_, err = store.Session(t.Context(), "unknown")
	require.ErrorIs(t, err, goidc.ErrNotFound)
}

func TestTheLogoutStoreRoundTripsAClientlessSession(t *testing.T) {
	pool := migratedPool(t)

	store := logoutStore{protocolStore: protocolStore{pool: pool}}
	session := &goidc.LogoutSession{
		ID:        "logout-1",
		Status:    goidc.StatusPending,
		ExpiresAt: int(time.Now().Add(time.Minute).Unix()),
	}
	require.NoError(t, store.SaveLogoutSession(t.Context(), session))

	loaded, err := store.LogoutSession(t.Context(), "logout-1")
	require.NoError(t, err)
	require.Equal(t, "logout-1", loaded.ID)
}
