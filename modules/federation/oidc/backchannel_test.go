package oidc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The back-channel logout delivery: the token's member set, the switch
// and the destination it rides, and the rule that a delivery never fails
// the logout that succeeded.

// testHMACKey is the 32-byte secret the token tests sign and verify with.
const testHMACKey = "expecto-patronum-32-bytes-of-secret!!"

// signingTestKey builds the symmetric key the claim tests share.
func signingTestKey(t *testing.T) jwk.Key {
	t.Helper()
	key, err := jwk.Import([]byte(testHMACKey))
	require.NoError(t, err)
	require.NoError(t, key.Set(jwk.KeyTypeKey, "oct"))
	require.NoError(t, key.Set(jwk.AlgorithmKey, "HS256"))
	return key
}

func TestTheLogoutTokenCarriesTheBackchannelMemberSet(t *testing.T) {
	token, err := signLogoutToken(signingTestKey(t), jwa.HS256(), "https://saka.example",
		"client-one", "hogwarts", "sess_elder-wand")
	require.NoError(t, err)

	// The JOSE type member is the discriminator the specification names.
	message, err := jws.Parse([]byte(token))
	require.NoError(t, err)
	typ, _ := message.Signatures()[0].ProtectedHeaders().Type()
	assert.Equal(t, "logout+jwt", typ)

	parsed, err := jwt.ParseString(token, jwt.WithKey(jwa.HS256(), []byte(testHMACKey)))
	require.NoError(t, err)

	var events map[string]any
	require.NoError(t, parsed.Get("events", &events), "the events member is present")
	assert.Contains(t, events, BackchannelLogoutEvent)

	var nonce any
	err = parsed.Get("nonce", &nonce)
	require.Error(t, err, "a logout token never carries a nonce — it would be a replayable ID token")

	var aud []string
	require.NoError(t, parsed.Get("aud", &aud))
	assert.Equal(t, []string{"client-one"}, aud)

	sub, present := parsed.Subject()
	require.True(t, present, "the subject rides the token")
	assert.Equal(t, "hogwarts", sub)

	var sid string
	require.NoError(t, parsed.Get("sid", &sid), "the hint's session identifier rides through")
	assert.Equal(t, "sess_elder-wand", sid)

	exp, present := parsed.Expiration()
	require.True(t, present, "the token carries its two-minute expiry")
	assert.WithinDuration(t, time.Now().Add(2*time.Minute), exp, 30*time.Second)

	// A token without a session identifier carries no sid member.
	bare, err := signLogoutToken(signingTestKey(t), jwa.HS256(), "https://saka.example",
		"client-one", "hogwarts", "")
	require.NoError(t, err)
	parsedBare, err := jwt.ParseString(bare, jwt.WithKey(jwa.HS256(), []byte(testHMACKey)))
	require.NoError(t, err)
	err = parsedBare.Get("sid", &sid)
	require.Error(t, err, "no session named, no sid member")
}

// recordingDispatcher is the dispatcher a dispatch test reads back.
type recordingDispatcher struct {
	dispatches []BackchannelLogoutDispatch
}

func (r *recordingDispatcher) DispatchBackchannelLogout(_ context.Context, dispatch BackchannelLogoutDispatch) error {
	r.dispatches = append(r.dispatches, dispatch)
	return nil
}

// staticBackchannel is the switch a dispatch test pins.
type staticBackchannel struct{ enabled bool }

func (s staticBackchannel) BackchannelLogoutEnabled(context.Context) (bool, error) {
	return s.enabled, nil
}

func TestTheDeliveryRidesTheSwitchAndTheDestination(t *testing.T) {
	pool := migratedPool(t)
	userID := seedAccount(t, pool, "granger")
	dispatcher := &recordingDispatcher{}
	service := testService(t, pool).
		WithBackchannelLogoutSource(staticBackchannel{enabled: true}).
		WithBackchannelLogoutSigner(stubSigner{}).
		WithBackchannelLogoutDispatcher(dispatcher)

	// A client that named a destination is the one the delivery reaches.
	issued, err := service.Create(t.Context(), userID, CreateParams{
		Name:                 "Ministry Portal",
		CallbackURLs:         []string{"https://ministry.example/callback"},
		BackchannelLogoutURI: "https://ministry.example/backchannel",
	})
	require.NoError(t, err)
	require.NoError(t, service.recordAuthorization(t.Context(), userID.String(), issued.Client.ID, []string{"openid"}))

	// A client with no destination never reaches the dispatcher.
	opted, err := service.Create(t.Context(), userID, createParams("Diagon Portal"))
	require.NoError(t, err)

	require.NoError(t, service.EndSession(t.Context(), userID.String(), issued.Client.ID, "sess_one"))
	require.Len(t, dispatcher.dispatches, 1)
	assert.Equal(t, issued.Client.ID, dispatcher.dispatches[0].ClientID)
	assert.Equal(t, "https://ministry.example/backchannel", dispatcher.dispatches[0].URI)
	assert.Equal(t, "signed-token", dispatcher.dispatches[0].Token)

	require.NoError(t, service.EndSession(t.Context(), userID.String(), opted.Client.ID, ""))
	assert.Len(t, dispatcher.dispatches, 1, "a client that opted out receives nothing")
}

func TestTheDeliveryOffSwitchStopsEverything(t *testing.T) {
	pool := migratedPool(t)
	userID := seedAccount(t, pool, "vetroff")
	dispatcher := &recordingDispatcher{}
	service := testService(t, pool).
		WithBackchannelLogoutSource(staticBackchannel{enabled: false}).
		WithBackchannelLogoutSigner(stubSigner{}).
		WithBackchannelLogoutDispatcher(dispatcher)
	issued, err := service.Create(t.Context(), userID, CreateParams{
		Name:                 "Horcrux Portal",
		CallbackURLs:         []string{"https://horcrux.example/callback"},
		BackchannelLogoutURI: "https://horcrux.example/backchannel",
	})
	require.NoError(t, err)

	require.NoError(t, service.EndSession(t.Context(), userID.String(), issued.Client.ID, ""))
	assert.Empty(t, dispatcher.dispatches, "the switch off mints nothing")
}

func TestAnUnwiredDeliveryNeverFailsTheLogout(t *testing.T) {
	pool := migratedPool(t)
	userID := seedAccount(t, pool, "neveu")
	// No seam wired at all — the pre-backchannel shape every existing
	// deployment carries.
	service := testService(t, pool)
	issued, err := service.Create(t.Context(), userID, CreateParams{
		Name:                 "Archives Portal",
		CallbackURLs:         []string{"https://archives.example/callback"},
		BackchannelLogoutURI: "https://archives.example/backchannel",
	})
	require.NoError(t, err)

	require.NoError(t, service.EndSession(t.Context(), userID.String(), issued.Client.ID, ""),
		"an unwired delivery is the feature off, not a broken logout")
}

// stubSigner answers a fixed token, the signing test above pins the real
// claim set.
type stubSigner struct{}

func (stubSigner) SignLogoutToken(context.Context, string, string, string) (string, error) {
	return "signed-token", nil
}

// failingSigner is the signing gone wrong.
type failingSigner struct{}

func (failingSigner) SignLogoutToken(context.Context, string, string, string) (string, error) {
	return "", errors.New("keyring: sealed")
}

func TestAFailingSignerNeverFailsTheLogout(t *testing.T) {
	pool := migratedPool(t)
	userID := seedAccount(t, pool, "vitra")
	dispatcher := &recordingDispatcher{}
	service := testService(t, pool).
		WithBackchannelLogoutSource(staticBackchannel{enabled: true}).
		WithBackchannelLogoutSigner(failingSigner{}).
		WithBackchannelLogoutDispatcher(dispatcher)
	issued, err := service.Create(t.Context(), userID, CreateParams{
		Name:                 "Beauxbatons Portal",
		CallbackURLs:         []string{"https://beauxbatons.example/callback"},
		BackchannelLogoutURI: "https://beauxbatons.example/backchannel",
	})
	require.NoError(t, err)

	require.NoError(t, service.EndSession(t.Context(), userID.String(), issued.Client.ID, ""),
		"the logout succeeded; a signing failure is a lost delivery, not a failed logout")
	assert.Empty(t, dispatcher.dispatches)
}
