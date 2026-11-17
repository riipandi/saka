package oidc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/modules/identity/usergroup"
)

// A refresh renews only for an account that still qualifies for the client:
// the account must exist, be enabled, sit outside a ban window and, for a
// group-restricted client, still belong to an allowed group. A refused grant
// is deleted with its tokens, and the userinfo endpoint answers the subject
// alone for such an account.

// failingDirectory answers every read with an infrastructure error.
type failingDirectory struct{}

func (failingDirectory) GetUser(context.Context, string) (user.UserView, error) {
	return user.UserView{}, errors.New("directory: connection refused")
}

type refreshHarness struct {
	service  *Service
	store    grantStore
	grant    *goidc.Grant
	clientID string
	view     *user.UserView
	wireID   string
	subject  string
	groupID  string
}

// newRefreshHarness seeds an account, a client and a live grant with one
// refresh token, and answers the service wired to a directory the test edits
// through view.
func newRefreshHarness(t *testing.T, restrictedGroup bool) *refreshHarness {
	t.Helper()

	pool := migratedPool(t)
	account := seedAccount(t, pool, "hermione")
	wire, err := user.IDFromUUIDString(account.String())
	require.NoError(t, err)

	view := &user.UserView{ID: wire.String(), Username: "hermione", Email: "hermione@example.com"}
	directory := &stubDirectory{accounts: map[string]user.UserView{wire.String(): *view}}
	service := testService(t, pool).WithUserDirectory(directory)

	params := createParams("Hogwarts Portal")
	groupWire := ""
	if restrictedGroup {
		groupWire = seedGroup(t, pool, "gryffindor")
		params.AllowedGroupWires = []string{groupWire}
	}
	issued, err := service.Create(t.Context(), seedAccount(t, pool, "langdon"), params)
	require.NoError(t, err)

	if restrictedGroup {
		groupID, parseErr := usergroup.ParseID(groupWire)
		require.NoError(t, parseErr)
		_, err = pool.Exec(t.Context(),
			`INSERT INTO public.user_groups_users (user_id, user_group_id) VALUES ($1, $2)`, account, groupID.UUID())
		require.NoError(t, err)
	}

	store := grantStore{protocolStore: protocolStore{pool: pool}}
	grant := &goidc.Grant{
		ID:                    "grant-1",
		CreatedAt:             int(time.Now().Unix()),
		Subject:               account.String(),
		ClientID:              issued.Client.ID,
		Scopes:                "openid profile",
		RefreshToken:          "refresh-1",
		RefreshTokenExpiresAt: int(time.Now().Add(time.Hour).Unix()),
	}
	require.NoError(t, store.SaveGrant(t.Context(), grant))

	h := &refreshHarness{service: service, store: store, grant: grant, clientID: issued.Client.ID,
		view: view, wireID: wire.String(), subject: account.String(), groupID: groupWire}
	h.publish(directory)
	return h
}

// publish copies the edited view into the directory the service reads.
func (h *refreshHarness) publish(directory *stubDirectory) {
	directory.accounts[h.wireID] = *h.view
	h.service.WithUserDirectory(directory)
}

func (h *refreshHarness) grantSurvives(t *testing.T) bool {
	t.Helper()
	_, err := h.store.Grant(t.Context(), h.grant.ID)
	if errors.Is(err, goidc.ErrNotFound) {
		_, refreshErr := h.store.GrantByRefreshToken(t.Context(), "refresh-1")
		assert.ErrorIs(t, refreshErr, goidc.ErrNotFound, "a deleted grant leaves no refresh pointer")
		return false
	}
	require.NoError(t, err)
	return true
}

func (h *refreshHarness) refresh(t *testing.T) error {
	t.Helper()
	return refuseDisqualifiedAccount(t.Context(), h.service, goidc.GrantRefreshToken, h.grant)
}

func requireInvalidGrant(t *testing.T, err error) {
	t.Helper()
	var oidcErr goidc.Error
	require.ErrorAs(t, err, &oidcErr)
	assert.Equal(t, goidc.ErrorCodeInvalidGrant, oidcErr.Code)
}

func TestAQualifyingAccountRefreshesAndKeepsItsGrant(t *testing.T) {
	h := newRefreshHarness(t, true)

	require.NoError(t, h.refresh(t))
	assert.True(t, h.grantSurvives(t))

	past := time.Now().Add(-time.Hour)
	h.view.BannedAt = &past
	h.view.BanExpires = &past
	h.publish(&stubDirectory{accounts: map[string]user.UserView{}})
	require.NoError(t, h.refresh(t), "a ban that has lapsed no longer refuses")
}

func TestAnAccountThatStopsQualifyingLosesItsRefreshToken(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)

	cases := map[string]func(h *refreshHarness, t *testing.T){
		"disabled": func(h *refreshHarness, _ *testing.T) { h.view.Disabled = true },
		"banned until further notice": func(h *refreshHarness, _ *testing.T) {
			h.view.BannedAt = &now
		},
		"banned for a window": func(h *refreshHarness, _ *testing.T) {
			h.view.BannedAt = &now
			h.view.BanExpires = &later
		},
		"deleted": func(h *refreshHarness, _ *testing.T) { delete(h.service.users.(*stubDirectory).accounts, h.wireID) },
		"removed from the allowed group": func(h *refreshHarness, t *testing.T) {
			_, err := h.store.pool.Exec(t.Context(), `DELETE FROM public.user_groups_users`)
			require.NoError(t, err)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			h := newRefreshHarness(t, true)
			change(h, t)
			if directory, ok := h.service.users.(*stubDirectory); ok && name != "deleted" {
				h.publish(directory)
			}

			requireInvalidGrant(t, h.refresh(t))
			assert.False(t, h.grantSurvives(t), "the refused grant is deleted with its tokens")
		})
	}
}

func TestAnUnreadableDirectoryRefusesTheRefreshWithoutEndingTheGrant(t *testing.T) {
	h := newRefreshHarness(t, false)
	h.service.WithUserDirectory(failingDirectory{})

	err := h.refresh(t)
	require.Error(t, err)
	var oidcErr goidc.Error
	assert.False(t, errors.As(err, &oidcErr), "a failed read is a server error, not a verdict on the grant")
	assert.True(t, h.grantSurvives(t), "an outage must not delete anyone's grant")
}

func TestOtherGrantTypesSkipTheAccountCheck(t *testing.T) {
	h := newRefreshHarness(t, false)
	h.view.Disabled = true
	h.publish(h.service.users.(*stubDirectory))

	require.NoError(t, refuseDisqualifiedAccount(t.Context(), h.service, goidc.GrantAuthorizationCode, h.grant))
	assert.True(t, h.grantSurvives(t))
}

func TestUserinfoAnswersTheSubjectAloneForAnAccountThatNoLongerQualifies(t *testing.T) {
	h := newRefreshHarness(t, false)
	claims := userInfoClaims(h.service)

	assert.Equal(t, "hermione", claims(t.Context(), h.grant)["preferred_username"])

	h.view.Disabled = true
	h.publish(h.service.users.(*stubDirectory))
	assert.Empty(t, claims(t.Context(), h.grant))
}

func TestAnUnreadableRefreshWindowFailsTheGrant(t *testing.T) {
	service := testService(t, migratedPool(t)).WithRefreshWindowSource(
		staticWindows{err: errors.New("settings: unreadable")})
	grant := goidc.Grant{ID: "g", Scopes: "openid", ClientID: "c", Subject: "s", CreatedAt: 1000}

	err := stampRefreshWindow(t.Context(), service, goidc.GrantRefreshToken, &grant)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "refresh window"), "the failure names the setting: %v", err)
	assert.Zero(t, grant.RefreshTokenExpiresAt, "no expiry is stamped, least of all the never-expiring one")
}
