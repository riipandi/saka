// Package jwtutils signs and verifies JWTs with typed private claims.
package jwtutils

import (
	"encoding/json/v2"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwt"
)

// registeredClaims lists claims handled by Standard.
var registeredClaims = map[string]bool{
	"iss": true, "sub": true, "aud": true,
	"exp": true, "nbf": true, "iat": true, "jti": true,
}

// Standard carries the registered claims managed by the Signer and
// surfaced by the Verifier.
type Standard struct {
	Issuer    string
	Subject   string
	Audience  []string
	ExpiresAt time.Time
	IssuedAt  time.Time
	NotBefore time.Time
	JWTID     string
}

// Verified contains the registered and private claims of a checked token.
type Verified[T any] struct {
	Standard
	Private T
}

// PrivateClaimDecoder customizes reconstruction of typed private claims.
type PrivateClaimDecoder interface {
	DecodePrivateClaims(params map[string]any) error
}

// decodePrivate reconstructs typed claims from non-registered claims.
func decodePrivate[T any](tok jwt.Token) (T, error) {
	params := make(map[string]any, len(tok.Keys()))
	for _, name := range tok.Keys() {
		if registeredClaims[name] {
			continue
		}
		var value any
		if err := tok.Get(name, &value); err != nil {
			var zero T
			return zero, err
		}
		params[name] = value
	}

	var claims T
	if dec, ok := any(&claims).(PrivateClaimDecoder); ok {
		return claims, dec.DecodePrivateClaims(params)
	}

	raw, err := json.Marshal(params)
	if err != nil {
		return claims, err
	}
	return claims, json.Unmarshal(raw, &claims)
}
