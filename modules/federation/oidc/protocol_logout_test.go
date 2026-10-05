package oidc

import (
	"context"
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

// recordingSigner answers the session identifier the dispatch was handed —
// the correlation the signed token would carry.
type recordingSigner struct {
	sessions []string
}

func (r *recordingSigner) SignLogoutToken(_ context.Context, _, _, sessionID string) (string, error) {
	r.sessions = append(r.sessions, sessionID)
	return "signed-token", nil
}

// TestTheLogoutPolicyCarriesTheHintsSessionToTheDelivery pins the Back-Channel
// Logout correlation: the hint's sid — the grant id the ID token minted at
// login — is the sid the delivered logout token carries, so a client that
// registered `backchannel_logout_session_required` can match the delivery to
// the session it ended.
// TestTheIDTokenCarriesTheGrantAsItsSession pins the session correlation the
// Back-Channel Logout profile reads: the ID token's sid is the grant's own
// identifier, so a relying party can match the delivered logout token to the
// session it names, and the end-session hint carries it back to the provider.
func TestTheIDTokenCarriesTheGrantAsItsSession(t *testing.T) {
	pool := migratedPool(t)
	account := seedAccount(t, pool, "luna")
	accountWire, err := user.IDFromUUIDString(account.String())
	require.NoError(t, err)
	service := testService(t, pool).WithUserDirectory(&stubDirectory{
		accounts: map[string]user.UserView{
			accountWire.String(): {ID: accountWire.String(), Username: "luna"},
		},
	}).WithClaimSource(&stubClaims{})

	grant := &goidc.Grant{ID: "grant-elder-wand", Subject: account.String(), Scopes: "openid"}
	claims := idTokenClaims(service)(t.Context(), grant)
	assert.Equal(t, "grant-elder-wand", claims["sid"],
		"the grant id is the session correlation")

	// The userinfo map is not a session surface — no sid rides there.
	info := userInfoClaims(service)(t.Context(), grant)
	_, present := info["sid"]
	assert.False(t, present, "userinfo carries claims, not the session correlation")
}

func TestTheLogoutPolicyCarriesTheHintsSessionToTheDelivery(t *testing.T) {
	pool := migratedPool(t)
	userID := seedAccount(t, pool, "sinistra")
	dispatcher := &recordingDispatcher{}
	signer := &recordingSigner{}
	service := testService(t, pool).
		WithBackchannelLogoutSource(staticBackchannel{enabled: true}).
		WithBackchannelLogoutSigner(signer).
		WithBackchannelLogoutDispatcher(dispatcher)
	issued, err := service.Create(t.Context(), userID, CreateParams{
		Name:                             "Astronomy Tower",
		CallbackURLs:                     []string{"https://tower.example/callback"},
		BackchannelLogoutURI:             "https://tower.example/backchannel",
		BackchannelLogoutSessionRequired: true,
	})
	require.NoError(t, err)

	// The hint carries the sid the ID token minted at login: the grant's
	// own identifier.
	session := hintSession(hintToken(t, ""), userID.String(), issued.Client.ID)
	session.IDTokenHintClaims = &goidc.IDToken{
		Subject:          userID.String(),
		AdditionalClaims: map[string]any{"sid": "grant-elder-wand"},
	}

	policy := logoutPolicy(service)
	status, logoutErr := policy.Logout(httptest.NewRecorder(),
		httptest.NewRequest("POST", "/oidc/end-session", nil), session)

	assert.Equal(t, goidc.StatusSuccess, status)
	require.NoError(t, logoutErr)
	assert.Equal(t, []string{"grant-elder-wand"}, signer.sessions,
		"the hint's session identifier rides to the signing")
	require.Len(t, dispatcher.dispatches, 1)
	assert.Equal(t, issued.Client.ID, dispatcher.dispatches[0].ClientID)

	// The session-required flag answers false-positively no more: the
	// round-trip reads the flag back the create wrote.
	view, err := service.Get(t.Context(), issued.Client.ID)
	require.NoError(t, err)
	assert.True(t, view.BackchannelLogoutSessionRequired)
	assert.Equal(t, "https://tower.example/backchannel", view.BackchannelLogoutURI)
}
