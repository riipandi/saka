package oidc

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/modules/identity/usergroup"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// The group restriction gate on the protocol path: a client is restricted
// when its flag is set or its allowed-groups roll carries rows, restricted
// clients admit only accounts in an allowed group, a flag with an empty
// roll admits nobody, and an existing consent does not bypass the gate.

func TestTheRestrictionAdmitsOnlyMembersOfTheAllowedGroups(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	ownerID := seedAccount(t, pool, "langdon")
	memberID := seedAccount(t, pool, "vetra")

	restrictedParams := createParams("Hogwarts Portal")
	groupWire := seedGroup(t, pool, "gryffindor")
	restrictedParams.AllowedGroupWires = []string{groupWire}
	issued, err := service.Create(t.Context(), ownerID, restrictedParams)
	require.NoError(t, err)

	// A member of an allowed group is admitted.
	admitted, err := service.accountAdmitted(t.Context(), memberID.String(), issued.Client.ID, true)
	require.NoError(t, err)
	assert.False(t, admitted, "an account outside every allowed group must be refused")

	// Membership flips the answer.
	groupID, err := usergroup.ParseID(groupWire)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO public.user_groups_users (user_id, user_group_id) VALUES ($1, $2)`,
		memberID, groupID.UUID())
	require.NoError(t, err)
	admitted, err = service.accountAdmitted(t.Context(), memberID.String(), issued.Client.ID, true)
	require.NoError(t, err)
	assert.True(t, admitted, "a group member must be admitted")
}

func TestARestrictionFlagWithNoGroupsAdmitsNobody(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	ownerID := seedAccount(t, pool, "langdon")

	issued, err := service.Create(t.Context(), ownerID, createParams("Locked Vault"))
	require.NoError(t, err)

	// The flag is not writable through the API yet, so the seeded-row case
	// is created directly: restricted, but the roll carries no groups.
	_, err = pool.Exec(t.Context(),
		`UPDATE public.oidc_clients SET is_group_restricted = TRUE WHERE id = $1`,
		issued.Client.ID)
	require.NoError(t, err)

	// Even the owner belongs to no allowed group, so nobody gets in.
	admitted, err := service.accountAdmitted(t.Context(), ownerID.String(), issued.Client.ID, true)
	require.NoError(t, err)
	assert.False(t, admitted, "a restriction without groups must admit nobody")
}

func TestTheProtocolCompletionRefusesAnIneligibleAccount(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	ownerID := seedAccount(t, pool, "langdon")
	outsiderID := seedAccount(t, pool, "neveu")

	restrictedParams := createParams("Illuminati Gate")
	restrictedParams.SkipConsent = true
	groupWire := seedGroup(t, pool, "priori-incantatem")
	restrictedParams.AllowedGroupWires = []string{groupWire}
	restricted, err := service.Create(t.Context(), ownerID, restrictedParams)
	require.NoError(t, err)

	outsiderWire, err := user.IDFromUUIDString(outsiderID.String())
	require.NoError(t, err)
	caller := &jwtutils.Caller{UserID: outsiderWire.String()}
	session := &goidc.AuthnSession{Scopes: "openid profile"}

	// Skip-consent branch: the refusal comes before any grant or consent.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/oidc/authorize", nil)
	status, err := completeAuthentication(t.Context(), service, recorder, request,
		session, &goidc.Client{ID: restricted.Client.ID}, caller)
	assert.Equal(t, goidc.StatusFailure, status)
	require.Error(t, err)
	var oidcErr goidc.Error
	require.True(t, errors.As(err, &oidcErr), "the refusal must be a protocol error")
	assert.Equal(t, goidc.ErrorCodeAccessDenied, oidcErr.Code)

	// The ledger must stay empty — no consent was recorded for the refusal.
	views, err := service.MyAuthorizedClients(t.Context(), outsiderID.String())
	require.NoError(t, err)
	assert.Empty(t, views)
}

func TestAnExistingConsentDoesNotBypassTheRestriction(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	ownerID := seedAccount(t, pool, "langdon")
	userID := seedAccount(t, pool, "vetra")

	restrictedParams := createParams("Vetra Console")
	groupWire := seedGroup(t, pool, "camerlengo")
	restrictedParams.AllowedGroupWires = []string{groupWire}
	restricted, err := service.Create(t.Context(), ownerID, restrictedParams)
	require.NoError(t, err)

	// The account consented while it was still a member; the membership is
	// gone now, so the old consent must not open the door again.
	require.NoError(t, service.recordAuthorization(t.Context(), userID.String(), restricted.Client.ID, []string{"openid", "profile"}))

	userWire, err := user.IDFromUUIDString(userID.String())
	require.NoError(t, err)
	caller := &jwtutils.Caller{UserID: userWire.String()}
	session := &goidc.AuthnSession{Scopes: "openid profile"}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/oidc/authorize", nil)
	status, err := completeAuthentication(t.Context(), service, recorder, request,
		session, &goidc.Client{ID: restricted.Client.ID}, caller)
	assert.Equal(t, goidc.StatusFailure, status)
	require.Error(t, err)
	var oidcErr goidc.Error
	require.True(t, errors.As(err, &oidcErr), "the refusal must be a protocol error")
	assert.Equal(t, goidc.ErrorCodeAccessDenied, oidcErr.Code)
}

func TestAnEligibleAccountStillCompletesTheFlow(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	ownerID := seedAccount(t, pool, "langdon")
	memberID := seedAccount(t, pool, "vetra")

	restrictedParams := createParams("Vetra Studio")
	restrictedParams.SkipConsent = true
	groupWire := seedGroup(t, pool, "camerlengo")
	restrictedParams.AllowedGroupWires = []string{groupWire}
	restricted, err := service.Create(t.Context(), ownerID, restrictedParams)
	require.NoError(t, err)

	// Make the account a member of the allowed group.
	groupID, err := usergroup.ParseID(groupWire)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO public.user_groups_users (user_id, user_group_id) VALUES ($1, $2)`,
		memberID, groupID.UUID())
	require.NoError(t, err)

	memberWire, err := user.IDFromUUIDString(memberID.String())
	require.NoError(t, err)
	caller := &jwtutils.Caller{UserID: memberWire.String()}
	session := &goidc.AuthnSession{Scopes: "openid profile"}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/oidc/authorize", nil)
	status, err := completeAuthentication(t.Context(), service, recorder, request,
		session, &goidc.Client{ID: restricted.Client.ID}, caller)
	require.NoError(t, err)
	assert.Equal(t, goidc.StatusSuccess, status)
	assert.Equal(t, memberID.String(), session.Subject)
}

func TestTheCatalogueHidesAFlaggedClientWithNoGroups(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "langdon")

	issued, err := service.Create(t.Context(), userID, createParams("Sealed Archive"))
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`UPDATE public.oidc_clients SET is_group_restricted = TRUE WHERE id = $1`,
		issued.Client.ID)
	require.NoError(t, err)

	// Flag set, roll empty: the client is hidden from every account.
	views, err := service.MyClients(t.Context(), userID.String())
	require.NoError(t, err)
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	assert.NotContains(t, ids, issued.Client.ID)
}
