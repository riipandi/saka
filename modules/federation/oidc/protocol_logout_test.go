package oidc

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/modules/identity/user"
)

// hintToken mints a compact JWS the way the two token kinds differ: the
// access token carries the RFC 9068 type member, the ID token carries none.
func hintToken(t *testing.T, typ string) string {
	t.Helper()

	headers := jws.NewHeaders()
	if typ != "" {
		require.NoError(t, headers.Set(jws.TypeKey, typ))
	}
	token, err := jws.Sign([]byte(`{"iss":"https://saka.example"}`),
		jws.WithKey(jwa.HS256(), []byte("test-hint-key"), jws.WithProtectedHeaders(headers)))
	require.NoError(t, err)
	return string(token)
}

// hintSession assembles the logout session the library hands a policy
// once its own validation passed: the raw hint and its parsed claims.
func hintSession(hint string, subjectWire, clientID string) *goidc.LogoutSession {
	session := &goidc.LogoutSession{
		ID:          "logout-probe",
		Status:      goidc.StatusPending,
		ClientID:    clientID,
		ExpiresAt:   int(time.Now().Add(time.Minute).Unix()),
		IDTokenHint: hint,
	}
	if subjectWire != "" {
		session.IDTokenHintClaims = &goidc.IDToken{Subject: subjectWire}
	}
	return session
}

func TestEndSessionKillsTheGrantsAndKeepsTheConsent(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "neveu")
	issued, err := service.Create(t.Context(), userID, createParams("Illuminati Portal"))
	require.NoError(t, err)

	require.NoError(t, service.recordAuthorization(t.Context(), userID.String(), issued.Client.ID, []string{"openid"}))
	grant := seedGrant(t, pool, userID.String(), issued.Client.ID)

	require.NoError(t, service.EndSession(t.Context(), userID.String(), issued.Client.ID, ""))

	_, err = grantStore{protocolStore: protocolStore{pool: pool}}.Grant(t.Context(), grant)
	assert.ErrorIs(t, err, goidc.ErrNotFound)

	views, err := service.MyAuthorizedClients(t.Context(), userID.String())
	require.NoError(t, err)
	assert.Len(t, views, 1, "the ledger survives the logout")
	assertEvents(t, pool, auditEventSessionEnded, 1)
}

func TestEndSessionWithConsentRevocationDropsTheLedger(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool).WithEndSessionRevokesConsentSource(staticRevocation{revoke: true})
	userID := seedAccount(t, pool, "vetra")
	issued, err := service.Create(t.Context(), userID, createParams("CERN Gateway"))
	require.NoError(t, err)

	require.NoError(t, service.recordAuthorization(t.Context(), userID.String(), issued.Client.ID, []string{"openid"}))
	grant := seedGrant(t, pool, userID.String(), issued.Client.ID)

	require.NoError(t, service.EndSession(t.Context(), userID.String(), issued.Client.ID, ""))

	views, err := service.MyAuthorizedClients(t.Context(), userID.String())
	require.NoError(t, err)
	assert.Empty(t, views, "the consent dies with the grants")

	_, err = grantStore{protocolStore: protocolStore{pool: pool}}.Grant(t.Context(), grant)
	assert.ErrorIs(t, err, goidc.ErrNotFound)
	assertEvents(t, pool, auditEventSessionEnded, 1)
}

func TestTheLogoutPolicySetupRequiresAnIDTokenHint(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	policy := logoutPolicy(service)

	assert.False(t, policy.Setup(nil, &goidc.LogoutSession{}),
		"no hint names no subject, so the request fails closed")
	assert.True(t, policy.Setup(nil, &goidc.LogoutSession{IDTokenHint: "hint"}))
}

func TestTheLogoutPolicyRefusesAnAccessTokenHint(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "langdon")
	issued, err := service.Create(t.Context(), userID, createParams("Vatican Archive"))
	require.NoError(t, err)
	grant := seedGrant(t, pool, userID.String(), issued.Client.ID)

	policy := logoutPolicy(service)
	status, err := policy.Logout(httptest.NewRecorder(), httptest.NewRequest("POST", "/oidc/end-session", nil),
		hintSession(hintToken(t, accessTokenTypeMember), "", issued.Client.ID))

	assert.Equal(t, goidc.StatusFailure, status)
	require.Error(t, err)

	// The refusal happens before anything is revoked.
	_, loadErr := grantStore{protocolStore: protocolStore{pool: pool}}.Grant(t.Context(), grant)
	require.NoError(t, loadErr, "a refused hint revokes nothing")
}

func TestTheLogoutPolicyRevokesTheHintedSubject(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "gryffindor")
	issued, err := service.Create(t.Context(), userID, createParams("Time Turner"))
	require.NoError(t, err)
	grant := seedGrant(t, pool, userID.String(), issued.Client.ID)

	wire, err := user.IDFromUUIDString(userID.String())
	require.NoError(t, err)

	policy := logoutPolicy(service)
	// The provider mints the subject claim in the raw UUID form the grant
	// rows store; the wire form stays accepted for a pairwise-proofed
	// future or a hint minted by the directory.
	for _, subject := range []string{userID.String(), wire.String()} {
		status, logoutErr := policy.Logout(httptest.NewRecorder(), httptest.NewRequest("POST", "/oidc/end-session", nil),
			hintSession(hintToken(t, ""), subject, issued.Client.ID))

		assert.Equal(t, goidc.StatusSuccess, status)
		require.NoError(t, logoutErr)
	}

	_, err = grantStore{protocolStore: protocolStore{pool: pool}}.Grant(t.Context(), grant)
	assert.ErrorIs(t, err, goidc.ErrNotFound)
}

func TestTheLogoutPolicyRefusesAnUnknownSubject(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	policy := logoutPolicy(service)

	status, err := policy.Logout(httptest.NewRecorder(), httptest.NewRequest("POST", "/oidc/end-session", nil),
		hintSession(hintToken(t, ""), "user_notarealwireform0", "some-client"))

	assert.Equal(t, goidc.StatusFailure, status)
	require.Error(t, err)
}
