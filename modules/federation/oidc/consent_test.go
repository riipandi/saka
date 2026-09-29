package oidc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/usergroup"
)

// The consent surface's contract: the ledger answers what the approval
// recorded, the withdrawal kills the ledger row together with the grants
// and tokens the consent issued, and the accessible-client list is the
// catalogue the account's groups admit it to.

func TestTheLedgerAnswersTheConsentsTheApprovalRecorded(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), userID, createParams("Gryffindor Portal"))
	require.NoError(t, err)

	// The record the authorization flow commits is the row the surface
	// answers.
	require.NoError(t, service.recordAuthorization(t.Context(), userID.String(), issued.Client.ID, []string{"openid", "profile"}))

	views, err := service.MyAuthorizedClients(t.Context(), userID.String())
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, issued.Client.ID, views[0].Client.ID)
	assert.ElementsMatch(t, []string{"openid", "profile"}, views[0].Scopes)
	require.NotNil(t, views[0].LastUsedAt)

	// The administrative read of the same ledger answers by account and
	// deployment-wide, the wide one naming the account in its wire form.
	perUser, err := service.UserAuthorizedClients(t.Context(), userID.String())
	require.NoError(t, err)
	assert.Len(t, perUser, 1)

	entries, err := service.AllAuthorizedClients(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, issued.Client.ID, entries[0].Client.ID)
	assert.NotEmpty(t, entries[0].UserWire)
}

func TestRevokingAConsentKillsTheGrantsAndTheirTokens(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "sophie")
	issued, err := service.Create(t.Context(), userID, createParams("Neutrino Gauge"))
	require.NoError(t, err)

	// A consent, a grant under it, and the two pointers the tokens ride —
	// the shape a live grant leaves behind.
	require.NoError(t, service.recordAuthorization(t.Context(), userID.String(), issued.Client.ID, []string{"openid"}))
	grant := seedGrant(t, pool, userID.String(), issued.Client.ID)

	require.NoError(t, service.RevokeMyAuthorizedClient(t.Context(), userID.String(), issued.Client.ID))

	views, err := service.MyAuthorizedClients(t.Context(), userID.String())
	require.NoError(t, err)
	assert.Empty(t, views)

	_, err = grantStore{protocolStore: protocolStore{pool: pool}}.Grant(t.Context(), grant)
	assert.ErrorIs(t, err, goidc.ErrNotFound)
}

// seedGrant plants one grant row with its code and refresh pointers, the
// state a live token issuance leaves behind. It answers the grant id.
// staticRevocation is the test's switch: a ConsentRevocationSource whose
// answer the test flips between calls, the way an operator's setting
// change reaches the next logout.
type staticRevocation struct {
	revoke bool
	err    error
}

func (s staticRevocation) EndSessionRevokesConsent(context.Context) (bool, error) {
	return s.revoke, s.err
}

// flipRevocation is the same switch with a setter the test drives.
type flipRevocation struct{ value bool }

func (f *flipRevocation) EndSessionRevokesConsent(context.Context) (bool, error) {
	return f.value, nil
}

func TestEndSessionReadsTheSwitchPerCall(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	flip := &flipRevocation{value: false}
	service = service.WithEndSessionRevokesConsentSource(flip)
	userID := seedAccount(t, pool, "langdon")
	issued, err := service.Create(t.Context(), userID, createParams("Florence Portal"))
	require.NoError(t, err)

	require.NoError(t, service.recordAuthorization(t.Context(), userID.String(), issued.Client.ID, []string{"openid"}))

	// Off: the ledger survives, the grants die.
	require.NoError(t, service.EndSession(t.Context(), userID.String(), issued.Client.ID))
	views, err := service.MyAuthorizedClients(t.Context(), userID.String())
	require.NoError(t, err)
	assert.Len(t, views, 1, "the ledger survives the off switch")

	// On, without a rebuild: the whole consent goes with the next logout.
	require.NoError(t, service.recordAuthorization(t.Context(), userID.String(), issued.Client.ID, []string{"openid"}))
	flip.value = true
	require.NoError(t, service.EndSession(t.Context(), userID.String(), issued.Client.ID))
	views, err = service.MyAuthorizedClients(t.Context(), userID.String())
	require.NoError(t, err)
	assert.Empty(t, views, "the flipped switch lands on the next logout")
}

func TestEndSessionWithAnUnreadableSwitchRevokesTheGrantsOnly(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool).WithEndSessionRevokesConsentSource(
		staticRevocation{err: errors.New("settings: unreadable")})
	userID := seedAccount(t, pool, "neveu")
	issued, err := service.Create(t.Context(), userID, createParams("Turin Portal"))
	require.NoError(t, err)

	require.NoError(t, service.recordAuthorization(t.Context(), userID.String(), issued.Client.ID, []string{"openid"}))
	grant := seedGrant(t, pool, userID.String(), issued.Client.ID)

	require.NoError(t, service.EndSession(t.Context(), userID.String(), issued.Client.ID),
		"an unreadable switch fails closed, it does not fail the logout")

	_, err = grantStore{protocolStore: protocolStore{pool: pool}}.Grant(t.Context(), grant)
	assert.ErrorIs(t, err, goidc.ErrNotFound, "the grants die as always")
	views, err := service.MyAuthorizedClients(t.Context(), userID.String())
	require.NoError(t, err)
	assert.Len(t, views, 1, "the ledger survives the unreadable switch")
}

func seedGrant(t *testing.T, pool *datastore.Postgres, subject, clientID string) string {
	t.Helper()

	store := grantStore{protocolStore: protocolStore{pool: pool}}
	grant := &goidc.Grant{
		ID:                    "grant-" + subject[:8] + "-revocation",
		CreatedAt:             int(time.Now().Unix()),
		Subject:               subject,
		ClientID:              clientID,
		Username:              "revocation-probe",
		Scopes:                "openid",
		RefreshToken:          "refresh-plain-" + subject[:8],
		RefreshTokenExpiresAt: int(time.Now().Add(time.Hour).Unix()),
		AuthCode:              "code-plain-" + subject[:8],
		AuthCodeExpiresAt:     int(time.Now().Add(time.Minute).Unix()),
	}
	require.NoError(t, store.SaveGrant(t.Context(), grant))
	return grant.ID
}

func TestTheAccessibleClientListFollowsTheGroupRestriction(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "langdon")
	open, err := service.Create(t.Context(), userID, createParams("Open Portal"))
	require.NoError(t, err)

	// A restricted client, its gate named after a group the account does
	// not belong to yet.
	restrictedParams := createParams("Illuminati Dash")
	groupWire := seedGroup(t, pool, "priori-incantatem")
	restrictedParams.AllowedGroupWires = []string{groupWire}
	restricted, err := service.Create(t.Context(), userID, restrictedParams)
	require.NoError(t, err)

	// Outside the restriction's groups, the account sees the open client
	// and not the restricted one.
	views, err := service.MyClients(t.Context(), userID.String())
	require.NoError(t, err)
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	assert.Contains(t, ids, open.Client.ID)
	assert.NotContains(t, ids, restricted.Client.ID)

	// Admitted through a group the account belongs to, the restricted
	// client joins the list. The creation already wrote the pairing, so
	// only the membership is missing.
	groupUUID, err := usergroup.ParseID(groupWire)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO public.user_groups_users (user_id, user_group_id) VALUES ($1, $2)`,
		userID, groupUUID.UUID())
	require.NoError(t, err)

	views, err = service.MyClients(t.Context(), userID.String())
	require.NoError(t, err)
	ids = ids[:0]
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	assert.Contains(t, ids, restricted.Client.ID)
}
