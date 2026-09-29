package jwtutils

import (
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// Signer creates signed JWTs with typed private claims.
type Signer[T any] struct {
	key       jwk.Key
	algorithm jwa.SignatureAlgorithm
	issuer    string
	audience  []string
	ttl       time.Duration
	typ       string
}

// NewSigner builds a signer with the given key and algorithm.
func NewSigner[T any](key jwk.Key, algorithm jwa.SignatureAlgorithm) (*Signer[T], error) {
	if key == nil {
		return nil, ErrMissingKey
	}
	if err := validateHMACKeySize(key, algorithm); err != nil {
		return nil, err
	}
	return &Signer[T]{key: key, algorithm: algorithm}, nil
}

// minHMACKeySize returns the minimum key size for an HMAC algorithm.
func minHMACKeySize(alg jwa.SignatureAlgorithm) int {
	switch alg.String() {
	case "HS256":
		return 32
	case "HS384":
		return 48
	case "HS512":
		return 64
	default:
		return 0
	}
}

// validateHMACKeySize checks the minimum size of an HMAC key.
func validateHMACKeySize(key jwk.Key, alg jwa.SignatureAlgorithm) error {
	minimum := minHMACKeySize(alg)
	if minimum == 0 {
		return nil
	}
	sym, ok := key.(jwk.SymmetricKey)
	if !ok {
		return nil
	}
	octets, exists := sym.Octets()
	if !exists || len(octets) >= minimum {
		return nil
	}
	return fmt.Errorf("%w: %s requires at least %d bytes, got %d", ErrWeakHMACKey, alg, minimum, len(octets))
}

// WithIssuer sets the default issuer claim.
func (s *Signer[T]) WithIssuer(issuer string) *Signer[T] {
	clone := *s
	clone.issuer = issuer
	return &clone
}

// WithAudience sets the default audience claim.
func (s *Signer[T]) WithAudience(audience ...string) *Signer[T] {
	clone := *s
	clone.audience = audience
	return &clone
}

// WithTTL sets the default token lifetime.
func (s *Signer[T]) WithTTL(ttl time.Duration) *Signer[T] {
	clone := *s
	clone.ttl = ttl
	return &clone
}

// WithTyp sets the JOSE type member the compact token's header carries —
// the discriminator a relying party reads before it reads the claims, the
// way RFC 9068's at+jwt names an access token and the logout token
// names itself logout+jwt. An empty typ (the default) sets no member.
func (s *Signer[T]) WithTyp(typ string) *Signer[T] {
	clone := *s
	clone.typ = typ
	return &clone
}

// Sign creates a compact JWT from the registered and private claims.
func (s *Signer[T]) Sign(claims T, std Standard) (string, error) {
	tok := jwt.New()

	issuedAt := std.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = time.Now()
	}
	if std.Issuer != "" || s.issuer != "" {
		issuer := std.Issuer
		if issuer == "" {
			issuer = s.issuer
		}
		if err := tok.Set(jwt.IssuerKey, issuer); err != nil {
			return "", err
		}
	}
	if std.Subject != "" {
		if err := tok.Set(jwt.SubjectKey, std.Subject); err != nil {
			return "", err
		}
	}
	audience := std.Audience
	if len(audience) == 0 {
		audience = s.audience
	}
	if len(audience) > 0 {
		if err := tok.Set(jwt.AudienceKey, audience); err != nil {
			return "", err
		}
	}
	if err := tok.Set(jwt.IssuedAtKey, issuedAt); err != nil {
		return "", err
	}
	if !std.NotBefore.IsZero() {
		if err := tok.Set(jwt.NotBeforeKey, std.NotBefore); err != nil {
			return "", err
		}
	}
	if !std.ExpiresAt.IsZero() || s.ttl > 0 {
		expiresAt := std.ExpiresAt
		if expiresAt.IsZero() {
			expiresAt = issuedAt.Add(s.ttl)
		}
		if err := tok.Set(jwt.ExpirationKey, expiresAt); err != nil {
			return "", err
		}
	}
	if std.JWTID != "" {
		if err := tok.Set(jwt.JwtIDKey, std.JWTID); err != nil {
			return "", err
		}
	}

	flat, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	var private map[string]any
	if unmarshalErr := json.Unmarshal(flat, &private); unmarshalErr != nil {
		return "", unmarshalErr
	}
	for name, value := range private {
		if registeredClaims[name] {
			return "", fmt.Errorf("%w: %q", ErrReservedClaim, name)
		}
		if setErr := tok.Set(name, value); setErr != nil {
			return "", setErr
		}
	}

	// The JOSE type member is a header, not a claim: it rides the
	// protected headers the signing options carry, so a discriminating
	// verifier reads it before it reads any claim.
	if s.typ != "" {
		headers := jws.NewHeaders()
		if headersErr := headers.Set(jws.TypeKey, s.typ); headersErr != nil {
			return "", headersErr
		}
		signed, signErr := jwt.Sign(tok, jwt.WithKey(s.algorithm, s.key, jws.WithProtectedHeaders(headers)))
		if signErr != nil {
			return "", signErr
		}
		return string(signed), nil
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(s.algorithm, s.key))
	if err != nil {
		return "", err
	}
	return string(signed), nil
}
