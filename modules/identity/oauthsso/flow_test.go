package oauthsso

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProvider is the adapter the flow tests drive: the authorize URL
// echoes the state the service drew, and the resolution answers a fixed
// identity — or the error the test named.
type fakeProvider struct {
	identity ExternalIdentity
	err      error
	last     FlowSecrets
	endpoint string
}

func (f *fakeProvider) AuthorizeURL(_ Connection, flow FlowSecrets) (string, error) {
	f.last = flow
	return "https://sso.hogwarts.example/authorize?state=" + url.QueryEscape(flow.State), nil
}

func (f *fakeProvider) Resolve(_ context.Context, _ Connection, flow FlowSecrets, _ string) (ExternalIdentity, error) {
	f.last = flow
	return f.identity, f.err
}

func (f *fakeProvider) TokenEndpoint(_ Connection) string {
	return f.endpoint
}

func flowService(t *testing.T, provider Provider) *Service {
	t.Helper()
	pool := migratedPool(t)
	service := testService(t, pool, nil).WithBaseURL("https://app.hogwarts.example")
	service.providers.Custom = provider
	return service
}

func enabledCustom(t *testing.T, service *Service) {
	t.Helper()
	params := customParams()
	params.Enabled = true
	_, err := service.Create(t.Context(), params)
	require.NoError(t, err)
}

func stateOf(t *testing.T, authorizeURL string) string {
	t.Helper()
	parsed, err := url.Parse(authorizeURL)
	require.NoError(t, err)
	state := parsed.Query().Get("state")
	require.NotEmpty(t, state)
	return state
}

func TestBeginWritesAPendingFlowAndNeverStoresTheRawState(t *testing.T) {
	provider := &fakeProvider{}
	service := flowService(t, provider)
	enabledCustom(t, service)

	authorizeURL, err := service.Begin(t.Context(), "hogwarts-sso")
	require.NoError(t, err)
	assert.Contains(t, authorizeURL, "state=")
	assert.Equal(t, "https://app.hogwarts.example/oauth/hogwarts-sso/callback", provider.last.RedirectURI)
	assert.NotEmpty(t, provider.last.Nonce)
	assert.NotEmpty(t, provider.last.Verifier)

	var (
		stage     string
		verifier  string
		tokenHash *string
	)
	require.NoError(t, service.pool.QueryRow(t.Context(),
		`SELECT stage, code_verifier, flow_token_hash FROM public.oauth_flows
		 WHERE state_hash = $1`, hashSecret(stateOf(t, authorizeURL))).
		Scan(&stage, &verifier, &tokenHash))
	assert.Equal(t, string(StagePending), stage)
	assert.True(t, strings.HasPrefix(verifier, "enc:"), "the PKCE verifier rests sealed")
	assert.NotContains(t, verifier, provider.last.Verifier)
	assert.Nil(t, tokenHash, "the flow token is minted at the callback, not at the begin")
}

func TestBeginRefusesADisabledOrUnknownConnection(t *testing.T) {
	service := flowService(t, &fakeProvider{})

	_, err := service.Begin(t.Context(), "hogwarts-sso")
	require.ErrorIs(t, err, ErrConnectionUnavailable)

	params := customParams()
	_, err = service.Create(t.Context(), params)
	require.NoError(t, err)
	_, err = service.Begin(t.Context(), "hogwarts-sso")
	require.ErrorIs(t, err, ErrConnectionUnavailable, "a disabled connection answers the same refusal")
}

func TestTheMasterSwitchRefusesTheBegin(t *testing.T) {
	provider := &fakeProvider{}
	service := flowService(t, provider)
	enabledCustom(t, service)

	// The configuration's switch is the surface's word: off, the begin
	// refuses whatever the rows hold.
	_, err := service.WithOAuthEnabled(false).Begin(t.Context(), "hogwarts-sso")
	require.ErrorIs(t, err, ErrConnectionUnavailable)
}

func TestCallbackConsumesTheFlowAndSealsTheTokens(t *testing.T) {
	provider := &fakeProvider{identity: ExternalIdentity{
		ProviderAccountID: "gryffindor-1",
		Email:             "hermione@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Hermione",
		FamilyName:        "Granger",
		AccessToken:       "access-plain",
		RefreshToken:      "refresh-plain",
	}}
	service := flowService(t, provider)
	enabledCustom(t, service)

	authorizeURL, err := service.Begin(t.Context(), "hogwarts-sso")
	require.NoError(t, err)

	flowToken, err := service.Callback(t.Context(), "hogwarts-sso", "code-from-provider", stateOf(t, authorizeURL))
	require.NoError(t, err)
	require.NotEmpty(t, flowToken)

	flow, err := service.FlowByToken(t.Context(), flowToken, StageResolved)
	require.NoError(t, err)
	assert.Equal(t, StageResolved, flow.Stage)
	assert.Equal(t, "gryffindor-1", flow.ProviderAccountID)
	assert.Equal(t, "hermione@hogwarts.example", flow.Email)
	assert.True(t, flow.EmailVerified)
	assert.Equal(t, "Hermione", flow.GivenName)
	assert.True(t, strings.HasPrefix(flow.AccessToken, "enc:"))
	assert.True(t, strings.HasPrefix(flow.RefreshToken, "enc:"))
	assert.NotContains(t, flow.AccessToken, "access-plain")

	// The verifier the adapter received is the one the begin sealed.
	assert.NotEmpty(t, provider.last.Verifier)
	assert.Equal(t, "https://app.hogwarts.example/oauth/hogwarts-sso/callback", provider.last.RedirectURI)
}

func TestCallbackReplayIsRefused(t *testing.T) {
	provider := &fakeProvider{identity: ExternalIdentity{
		ProviderAccountID: "gryffindor-1",
		Email:             "hermione@hogwarts.example",
		AccessToken:       "access-plain",
	}}
	service := flowService(t, provider)
	enabledCustom(t, service)

	authorizeURL, err := service.Begin(t.Context(), "hogwarts-sso")
	require.NoError(t, err)
	state := stateOf(t, authorizeURL)

	_, err = service.Callback(t.Context(), "hogwarts-sso", "code", state)
	require.NoError(t, err)
	_, err = service.Callback(t.Context(), "hogwarts-sso", "code", state)
	require.ErrorIs(t, err, ErrFlowUnknown)
}

func TestCallbackWithAnUnknownStateIsRefused(t *testing.T) {
	service := flowService(t, &fakeProvider{})
	enabledCustom(t, service)

	_, err := service.Callback(t.Context(), "hogwarts-sso", "code", "not-a-state")
	require.ErrorIs(t, err, ErrFlowUnknown)
}

func TestCallbackLeavesTheFlowPendingWhenTheProviderRefuses(t *testing.T) {
	provider := &fakeProvider{err: ErrResolutionFailed}
	service := flowService(t, provider)
	enabledCustom(t, service)

	authorizeURL, err := service.Begin(t.Context(), "hogwarts-sso")
	require.NoError(t, err)
	state := stateOf(t, authorizeURL)

	_, err = service.Callback(t.Context(), "hogwarts-sso", "code", state)
	require.ErrorIs(t, err, ErrResolutionFailed)

	var stage string
	require.NoError(t, service.pool.QueryRow(t.Context(),
		`SELECT stage FROM public.oauth_flows WHERE state_hash = $1`, hashSecret(state)).Scan(&stage))
	assert.Equal(t, string(StagePending), stage, "a refused resolution must not spend the state")
}

func TestTheSweepDeletesOnlyExpiredFlows(t *testing.T) {
	service := flowService(t, &fakeProvider{})
	enabledCustom(t, service)

	_, err := service.Begin(t.Context(), "hogwarts-sso")
	require.NoError(t, err)

	deleted, err := service.repo.DeleteExpiredFlows(t.Context(), time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)

	deleted, err = service.repo.DeleteExpiredFlows(t.Context(), time.Now().Add(flowLifetime+time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
}
