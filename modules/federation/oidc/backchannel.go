package oidc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"

	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/pkg/jwtutils"
)

// BackchannelLogoutEvent is the events member a logout token carries — the
// one the OIDC Back-Channel Logout specification names.
const BackchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

// backchannelLogoutSwitch is the settings key the feature's switch rides.
// Off, no delivery is ever minted, whatever the clients declare.
const backchannelLogoutSwitch = "oidc.backchannel_logout_enabled"

// SettingOIDCBackchannelLogoutEnabled is the switch's name as the catalog
// declares it.
const SettingOIDCBackchannelLogoutEnabled = backchannelLogoutSwitch

// BackchannelLogoutSource is the runtime switch the delivery reads per
// logout, so an operator's change lands without a restart. The settings
// feature backs it.
type BackchannelLogoutSource interface {
	// BackchannelLogoutEnabled answers the deployment's decision.
	BackchannelLogoutEnabled(ctx context.Context) (bool, error)
}

// BackchannelLogoutDispatch is one delivery the dispatcher carries: the
// signed logout token, and the client's registered destination.
type BackchannelLogoutDispatch struct {
	ClientID string
	URI      string
	Token    string
}

// BackchannelLogoutDispatcher hands one delivery to the durable queue —
// the enqueue is the dispatch's whole job, the retry belongs to the queue.
type BackchannelLogoutDispatcher interface {
	// DispatchBackchannelLogout enqueues one delivery. A failure to
	// enqueue is returned, never swallowed.
	DispatchBackchannelLogout(ctx context.Context, dispatch BackchannelLogoutDispatch) error
}

// LogoutTokenSigner signs the logout token's claim set with the same key
// set the provider publishes and its tokens carry — the relying party
// verifies the delivery against the JWKS it already knows.
type LogoutTokenSigner interface {
	// SignLogoutToken signs one delivery's claims. The session identifier
	// is absent when the end-session request named no session.
	SignLogoutToken(ctx context.Context, audience, subject, sessionID string) (string, error)
}

// WithBackchannelLogoutSource wires the runtime switch the delivery reads.
// A nil source answers off.
func (s *Service) WithBackchannelLogoutSource(source BackchannelLogoutSource) *Service {
	s.backchannelSource = source
	return s
}

// WithBackchannelLogoutSigner wires the signing the logout token rides. A
// nil signer is a deployment with no signing material resolved — the
// delivery is off.
func (s *Service) WithBackchannelLogoutSigner(signer LogoutTokenSigner) *Service {
	s.backchannelSigner = signer
	return s
}

// WithBackchannelLogoutDispatcher wires the durable queue the deliveries
// ride. A nil dispatcher is the delivery off.
func (s *Service) WithBackchannelLogoutDispatcher(dispatcher BackchannelLogoutDispatcher) *Service {
	s.backchannelDispatcher = dispatcher
	return s
}

// dispatchBackchannelLogout delivers the logout token for one ended
// session: the switch is read per call, the client's own destination is
// consulted, and every failure on the way is logged and dropped — a
// delivery that cannot be prepared must never fail the logout that
// succeeded, and the queue owns the retry of one that was prepared.
func (s *Service) dispatchBackchannelLogout(ctx context.Context, userID, clientID, sessionID string) {
	if s.backchannelSource == nil || s.backchannelSigner == nil || s.backchannelDispatcher == nil {
		return
	}
	enabled, err := s.backchannelSource.BackchannelLogoutEnabled(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "oidc: the back-channel logout switch is unread; delivering nothing",
			"error", err)
		return
	}
	if !enabled {
		return
	}

	row, err := s.repo.GetClient(ctx, s.pool, clientID)
	if err != nil {
		s.log.WarnContext(ctx, "oidc: the back-channel logout client is unread; delivering nothing",
			"error", err, "client_id", clientID)
		return
	}
	if row.BackchannelLogoutURI == "" {
		return
	}

	token, err := s.backchannelSigner.SignLogoutToken(ctx, clientID, userID, sessionID)
	if err != nil {
		s.log.WarnContext(ctx, "oidc: the logout token did not sign; delivering nothing",
			"error", err, "client_id", clientID)
		return
	}
	if err := s.backchannelDispatcher.DispatchBackchannelLogout(ctx, BackchannelLogoutDispatch{
		ClientID: clientID,
		URI:      row.BackchannelLogoutURI,
		Token:    token,
	}); err != nil {
		s.log.WarnContext(ctx, "oidc: the back-channel logout did not enqueue; the delivery is lost",
			"error", err, "client_id", clientID)
	}
}

// logoutTokenClaims is the private claim set a back-channel logout token
// carries — the event member and, when the end-session named one, the
// session. No nonce member ever rides it: a token with a nonce is a
// replayable ID token, and the specification forbids the pair. The
// registered claims (iss, aud, sub, iat, exp, jti) ride the Standard the
// signer stamps.
type logoutTokenClaims struct {
	Events    map[string]any `json:"events"`
	SessionID string         `json:"sid,omitempty"`
}

// jwksLogoutTokenSigner signs the deliveries with the deployment's active
// signing material — the key an ID token verifies against is the key the
// client verifies this token against.
type jwksLogoutTokenSigner struct {
	keys   *jwks.Service
	issuer string
}

// LogoutTokenSignerFor builds the feature's signer over the jwks service:
// a nil service is a run with no signing material resolved, and the
// delivery is off.
func LogoutTokenSignerFor(keys *jwks.Service, issuer string) LogoutTokenSigner {
	if keys == nil {
		return nil
	}
	return jwksLogoutTokenSigner{keys: keys, issuer: issuer}
}

func (s jwksLogoutTokenSigner) SignLogoutToken(ctx context.Context, audience, subject, sessionID string) (string, error) {
	algorithm, err := s.keys.SigningAlgorithm()
	if err != nil {
		return "", fmt.Errorf("oidc: logout token algorithm: %w", err)
	}
	key, err := s.signingKey(ctx, algorithm)
	if err != nil {
		return "", fmt.Errorf("oidc: logout token signing key: %w", err)
	}
	return signLogoutToken(key, algorithm, s.issuer, audience, subject, sessionID)
}

// signLogoutToken mints one delivery's token from resolved material. The
// token lives two minutes — long enough for the queue's attempt, short
// enough that a redelivery after every attempt failed signs nothing the
// client can still bank.
func signLogoutToken(key jwk.Key, algorithm jwa.SignatureAlgorithm, issuer, audience, subject, sessionID string) (string, error) {
	claims := logoutTokenClaims{
		Events:    map[string]any{BackchannelLogoutEvent: map[string]any{}},
		SessionID: sessionID,
	}
	signer, err := jwtutils.NewSigner[logoutTokenClaims](key, algorithm)
	if err != nil {
		return "", fmt.Errorf("oidc: logout token signer: %w", err)
	}
	signer = signer.WithIssuer(issuer).
		WithAudience(audience).
		WithTTL(2 * time.Minute).
		WithTyp("logout+jwt")
	token, err := signer.Sign(claims, jwtutils.Standard{
		Subject:  subject,
		JWTID:    randomLogoutTokenID(),
		IssuedAt: time.Now(),
	})
	if err != nil {
		return "", fmt.Errorf("oidc: sign logout token: %w", err)
	}
	return token, nil
}

// signingKey picks the half of the dual stack the resolved algorithm
// names: a symmetric algorithm signs with the HMAC secret, anything else
// with the configured key pair — the same decision the sign-in issuer
// makes, since the same clients verify both.
func (s jwksLogoutTokenSigner) signingKey(ctx context.Context, algorithm jwa.SignatureAlgorithm) (jwk.Key, error) {
	if algorithm == (jwa.SignatureAlgorithm{}) || strings.EqualFold(algorithm.String(), "none") {
		return nil, errors.New("oidc: the signing algorithm is unresolved")
	}
	if algorithm.IsSymmetric() {
		return s.keys.HMACKey(ctx)
	}
	return s.keys.SignKey(ctx)
}

// randomLogoutTokenID draws the jti the delivery carries — the one-time
// identifier a replay-conscious relying party checks, the shape the
// provider's own codes take.
func randomLogoutTokenID() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}
