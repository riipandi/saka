package jwtutils

import (
	"context"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keySource is a SigningKeySource over one HMAC secret, the one-stack
// deployment the tests sign with.
type keySource struct {
	secret    string
	algorithm jwa.SignatureAlgorithm
}

func (k keySource) hmacKey() (jwk.Key, error) {
	raw := []byte(k.secret)
	if len(raw) < 32 {
		padded := make([]byte, 32)
		for i := range padded {
			padded[i] = k.secret[i%len(k.secret)]
		}
		raw = padded
	}
	return jwk.Import(raw)
}

func (k keySource) SignKey(context.Context) (jwk.Key, error) {
	return k.hmacKey()
}

func (k keySource) VerifyKeySet(context.Context) (jwk.Set, error) {
	return jwk.NewSet(), nil
}

func (k keySource) HMACKey(context.Context) (jwk.Key, error) {
	return k.hmacKey()
}

func (k keySource) SigningAlgorithm() (jwa.SignatureAlgorithm, error) {
	return k.algorithm, nil
}

// TestAccessVerifierRoundTripsTheClaims pins the token that carries no
// delegation: the actor claims are absent, not empty, so a reader can tell a
// token that belongs to its account from a delegated one by their presence.
func TestAccessVerifierRoundTripsTheClaims(t *testing.T) {
	source := keySource{secret: "0123456789abcdeffedcba9876543210", algorithm: jwa.HS256()}
	signer, err := NewSigner[AccessClaims](mustHMACKey(t, source.secret), jwa.HS256())
	require.NoError(t, err)
	signer = signer.WithIssuer("https://saka.example").WithTTL(time.Hour)

	token, err := signer.Sign(AccessClaims{
		Email:     "hermione@example.com",
		Username:  "hermione",
		Roles:     []string{"administrator"},
		SessionID: "sess_01abc",
	}, Standard{Subject: "0197abc", IssuedAt: time.Now()})
	require.NoError(t, err)

	verified, err := NewAccessVerifier(source, "https://saka.example").Verify(t.Context(), token)
	require.NoError(t, err)

	caller, err := NewCaller(verified)
	require.NoError(t, err)

	assert.Equal(t, "0197abc", verified.Subject)
	assert.Equal(t, "hermione@example.com", verified.Private.Email)
	assert.Equal(t, "hermione", verified.Private.Username)
	assert.True(t, caller.HasRole("administrator"))
	assert.True(t, caller.IsAdministrator())
	assert.Equal(t, "sess_01abc", verified.Private.SessionID)
}

// TestAccessVerifierRoundTripsADelegatedToken is the impersonation plumbing:
// the token acts as its subject while naming the administrator behind it, and
// the caller it decodes to reports both. The two claims are the only signal a
// seam gets, so they must survive the round trip exactly.
func TestAccessVerifierRoundTripsADelegatedToken(t *testing.T) {
	source := keySource{secret: "0123456789abcdeffedcba9876543210", algorithm: jwa.HS256()}
	signer, err := NewSigner[AccessClaims](mustHMACKey(t, source.secret), jwa.HS256())
	require.NoError(t, err)
	signer = signer.WithIssuer("https://saka.example").WithTTL(time.Hour)

	token, err := signer.Sign(AccessClaims{
		Email:         "hermione@example.com",
		Username:      "hermione",
		SessionID:     "sess_01abc",
		ActorID:       "01a0da1c-cb41-779d-bd02-99b3eb5da32a",
		ActorUsername: "admin",
	}, Standard{Subject: "0197abc", IssuedAt: time.Now()})
	require.NoError(t, err)

	caller, err := NewAccessVerifier(source, "https://saka.example").VerifyCaller(t.Context(), token)
	require.NoError(t, err)

	// The subject is the account the request runs as; the actor is who is
	// behind it. Both are needed, and neither replaces the other.
	assert.Equal(t, "0197abc", caller.UserID)
	assert.Equal(t, "hermione", caller.Username)
	assert.True(t, caller.IsImpersonating())
	assert.Equal(t, "01a0da1c-cb41-779d-bd02-99b3eb5da32a", caller.ActorID)
	assert.Equal(t, "admin", caller.ActorUsername)

	// The delegation is not a way to pass a self-service rule, even for the
	// very account the token names.
	assert.True(t, caller.ActsFor("0197abc"))
}

// TestAnUndelegatedTokenCarriesNoActorClaim keeps the other half of the
// signal: a token that belongs to its account must not answer
// IsImpersonating, and must not carry an empty actor claim a reader could
// mistake for one.
func TestAnUndelegatedTokenCarriesNoActorClaim(t *testing.T) {
	source := keySource{secret: "0123456789abcdeffedcba9876543210", algorithm: jwa.HS256()}
	signer, err := NewSigner[AccessClaims](mustHMACKey(t, source.secret), jwa.HS256())
	require.NoError(t, err)
	signer = signer.WithIssuer("https://saka.example").WithTTL(time.Hour)

	token, err := signer.Sign(AccessClaims{Username: "hermione"},
		Standard{Subject: "0197abc", IssuedAt: time.Now()})
	require.NoError(t, err)

	caller, err := NewAccessVerifier(source, "https://saka.example").VerifyCaller(t.Context(), token)
	require.NoError(t, err)

	assert.False(t, caller.IsImpersonating())
	assert.Empty(t, caller.ActorID)
}

// TestNewCallerRefusesATokenWithoutASubject covers the door: a verified token
// that names no account cannot be compared against an identifier, so it is
// refused rather than handed on as an empty identity.
func TestNewCallerRefusesATokenWithoutASubject(t *testing.T) {
	_, err := NewCaller(Verified[AccessClaims]{Private: AccessClaims{Username: "hermione"}})
	require.ErrorIs(t, err, ErrMissingSubject)
}

// TestNewCallerKeepsTheSubjectAndTheClaims pins the composition: the caller
// carries the registered claim the private claims do not.
func TestNewCallerKeepsTheSubjectAndTheClaims(t *testing.T) {
	caller, err := NewCaller(Verified[AccessClaims]{
		Standard: Standard{Subject: "0197abc"},
		Private:  AccessClaims{Username: "hermione", Roles: []string{"administrator"}, Permissions: []string{"user:*:read", "user:*:ban"}},
	})
	require.NoError(t, err)

	assert.Equal(t, "0197abc", caller.UserID)
	assert.Equal(t, "hermione", caller.Username)
	assert.True(t, caller.IsAdministrator())
	assert.True(t, caller.HasPermission("user:usr_9:ban"))
	assert.True(t, caller.HasPermission("user:usr_9:read"))
	assert.False(t, caller.HasPermission("user:usr_9:delete"))
	assert.False(t, caller.IsImpersonating())
}

// TestActsForRefusesAnEmptyIdentifier keeps the comparison closed: a request
// that names no account must not match a caller whose subject happens to be
// empty.
func TestActsForRefusesAnEmptyIdentifier(t *testing.T) {
	caller := &Caller{UserID: ""}

	assert.False(t, caller.ActsFor(""))
	assert.False(t, (*Caller)(nil).ActsFor("0197abc"))
	assert.False(t, (*Caller)(nil).IsImpersonating())
}

func TestAccessVerifierRejectsAnotherIssuer(t *testing.T) {
	source := keySource{secret: "0123456789abcdeffedcba9876543210", algorithm: jwa.HS256()}
	signer, err := NewSigner[AccessClaims](mustHMACKey(t, source.secret), jwa.HS256())
	require.NoError(t, err)

	token, err := signer.Sign(AccessClaims{}, Standard{Subject: "0197abc", IssuedAt: time.Now()})
	require.NoError(t, err)

	_, err = NewAccessVerifier(source, "https://saka.example").Verify(t.Context(), token)
	assert.Error(t, err)
}

// TestAccessVerifierPinsTheAlgorithm pins the algorithm defense: the
// verifier is built with the exact algorithm the key material resolves to, so
// a token signed under another algorithm with the same secret is refused
// before any key is tried — the confusion the open verifier note warned of.
func TestAccessVerifierPinsTheAlgorithm(t *testing.T) {
	// The secret is 64 bytes so it satisfies the HS384 minimum the offending
	// signer demands; the pinning is what the assertion is about.
	secret := "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"
	source := keySource{secret: secret, algorithm: jwa.HS256()}

	signer, err := NewSigner[AccessClaims](mustHMACKey(t, secret), jwa.HS384())
	require.NoError(t, err)
	token, err := signer.Sign(AccessClaims{}, Standard{IssuedAt: time.Now()})
	require.NoError(t, err)

	_, err = NewAccessVerifier(source, "https://saka.example").Verify(t.Context(), token)
	assert.Error(t, err, "an HS384 token must not verify under an HS256-only verifier")
}

func mustHMACKey(t *testing.T, secret string) jwk.Key {
	t.Helper()
	key, err := keySource{secret: secret}.hmacKey()
	require.NoError(t, err)
	return key
}
