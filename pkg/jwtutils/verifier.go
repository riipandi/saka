package jwtutils

import (
	"errors"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// Errors returned by the signer and verifier.
var (
	ErrMissingKey    = errors.New("jwtutils: signing key is required")
	ErrMissingKeySet = errors.New("jwtutils: verification key set is empty")
	// ErrReservedClaim reports a private claim that uses a registered name.
	ErrReservedClaim = errors.New("jwtutils: private claim collides with a registered claim")
	// ErrWeakHMACKey reports an HMAC key shorter than the algorithm minimum.
	ErrWeakHMACKey = errors.New("jwtutils: HMAC key too short")
	// ErrMissingSubject reports a verified token that names no account. A
	// caller without a subject cannot be compared against an identifier, so
	// it is refused at the door rather than handed on as an empty identity.
	ErrMissingSubject = errors.New("jwtutils: token subject is required")
)

// Verifier checks JWTs and decodes their typed private claims.
type Verifier[T any] struct {
	key            jwk.Key
	algorithm      jwa.SignatureAlgorithm
	keySet         jwk.Set
	issuer         string
	audience       string
	clockSkew      time.Duration
	requiredClaims []string
}

// NewVerifier builds a verifier with a key and signature algorithm.
func NewVerifier[T any](key jwk.Key, algorithm jwa.SignatureAlgorithm) (*Verifier[T], error) {
	if key != nil {
		if err := validateHMACKeySize(key, algorithm); err != nil {
			return nil, err
		}
	}
	return &Verifier[T]{key: key, algorithm: algorithm}, nil
}

// WithKeySet configures verification with a JWKS key set.
func (v *Verifier[T]) WithKeySet(set jwk.Set) *Verifier[T] {
	clone := *v
	clone.keySet = set
	return &clone
}

// WithIssuer requires a matching issuer claim.
func (v *Verifier[T]) WithIssuer(issuer string) *Verifier[T] {
	clone := *v
	clone.issuer = issuer
	return &clone
}

// WithAudience requires a matching audience claim.
func (v *Verifier[T]) WithAudience(audience string) *Verifier[T] {
	clone := *v
	clone.audience = audience
	return &clone
}

// WithClockSkew sets the allowed clock difference for time claims.
func (v *Verifier[T]) WithClockSkew(skew time.Duration) *Verifier[T] {
	clone := *v
	clone.clockSkew = skew
	return &clone
}

// WithRequiredClaims requires the named registered claims.
func (v *Verifier[T]) WithRequiredClaims(names ...string) *Verifier[T] {
	clone := *v
	clone.requiredClaims = append(clone.requiredClaims, names...)
	return &clone
}

// Verify checks the token and decodes its typed private claims.
func (v *Verifier[T]) Verify(encoded string) (Verified[T], error) {
	opts := []jwt.ParseOption{}
	switch {
	case v.keySet != nil:
		if v.keySet.Len() == 0 {
			return Verified[T]{}, ErrMissingKeySet
		}
		opts = append(opts, jwt.WithKeySet(v.keySet, jws.WithRequireKid(true)))
	case v.key != nil:
		opts = append(opts, jwt.WithKey(v.algorithm, v.key))
	default:
		return Verified[T]{}, ErrMissingKey
	}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}
	if v.clockSkew > 0 {
		opts = append(opts, jwt.WithAcceptableSkew(v.clockSkew))
	}
	for _, name := range v.requiredClaims {
		opts = append(opts, jwt.WithRequiredClaim(name))
	}

	tok, err := jwt.Parse([]byte(encoded), opts...)
	if err != nil {
		return Verified[T]{}, err
	}

	private, err := decodePrivate[T](tok)
	if err != nil {
		return Verified[T]{}, err
	}

	out := Verified[T]{Private: private}
	if issuer, ok := tok.Issuer(); ok {
		out.Issuer = issuer
	}
	if subject, ok := tok.Subject(); ok {
		out.Subject = subject
	}
	if audience, ok := tok.Audience(); ok {
		out.Audience = audience
	}
	if expiresAt, ok := tok.Expiration(); ok {
		out.ExpiresAt = expiresAt
	}
	if issuedAt, ok := tok.IssuedAt(); ok {
		out.IssuedAt = issuedAt
	}
	if notBefore, ok := tok.NotBefore(); ok {
		out.NotBefore = notBefore
	}
	if jwtID, ok := tok.JwtID(); ok {
		out.JWTID = jwtID
	}
	return out, nil
}
