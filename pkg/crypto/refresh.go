package crypto

import (
	"crypto/rand"
	"encoding/base64"
)

// RefreshTokenBytes is the entropy of a refresh token: 256 bits, the draw a
// credential the server holds only as a hash needs.
const RefreshTokenBytes = 32

// RefreshTokenPair is the token the caller sees beside the hash the session
// row stores. The raw value exists in exactly one response and is never
// written down; the hash is the row's whole presence.
type RefreshTokenPair struct {
	Plain string
	Hash  string
}

// NewRefreshTokenPair draws a refresh token: 256 bits from the crypto source,
// base64url without padding — the form a client holds whole — hashed with
// SHA-256 into the hex form the session row stores.
//
// It lives here rather than beside the session feature because two issuers
// draw it — the sign-in that opens a session and the renewal that keeps one
// alive — and a token's entropy must not depend on which procedure issued it.
func NewRefreshTokenPair() (RefreshTokenPair, error) {
	raw := make([]byte, RefreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return RefreshTokenPair{}, err
	}
	plain := base64.RawURLEncoding.EncodeToString(raw)
	return RefreshTokenPair{Plain: plain, Hash: HashHexToken(plain)}, nil
}

// HashRefreshToken is the storage form of a refresh token: the SHA-256 of the
// presented value, hex-encoded. Every issuer hashes the same way, so a row a
// sign-in wrote is a row a renewal can rotate.
func HashRefreshToken(presented string) string {
	return HashHexToken(presented)
}
