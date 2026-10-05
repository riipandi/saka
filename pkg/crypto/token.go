package crypto

// Token minting and hashing: the shared contract every token table follows.
// The holder proves ownership by presenting the value back; only its hash is
// stored, so the mint is the one moment the secret exists in the clear.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
)

// Alphabet sets a feature draws its codes from. AlphabetAlphanumeric covers
// machine credentials that only a client presents; AlphabetUnambiguous drops
// the glyphs a human misreads (0/O, 1/l/I) because its holders type the value
// from a phone reading an email — the digits 0 and 1 go with the letters.
const (
	AlphabetAlphanumeric = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	AlphabetUnambiguous  = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

// hexTokenEntropy is the randomness of one hex token: 256 bits, so the hash
// in the database is the whole defense against a leak.
const hexTokenEntropy = 32

// NewHexToken mints one raw token: 256 bits of randomness as 64 lowercase
// hexadecimal characters. Hex keeps the token URL-safe without dashes or
// symbols — the form every token that travels a URL takes — so it survives a
// query string, a QR code, and a copy-paste through any chat client
// unchanged. The value is shown to its holder exactly once and only its
// HashHexToken is stored.
func NewHexToken() (string, error) {
	return RandomHexToken(hexTokenEntropy)
}

// RandomHexToken draws nbytes of crypto randomness as lowercase hex — 2n
// characters. The URL-travelling default is NewHexToken.
func RandomHexToken(nbytes int) (string, error) {
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// HashHexToken is the storage form of a presented credential: the SHA-256 of
// the value, hex-encoded. Every issuer hashes the same way, so a row one
// flow wrote is a row another flow can judge.
func HashHexToken(presented string) string {
	sum := sha256.Sum256([]byte(presented))
	return hex.EncodeToString(sum[:])
}

// HashTokenBytes is the same hash as raw bytes, the form a BYTEA column
// stores.
func HashTokenBytes(presented string) []byte {
	sum := sha256.Sum256([]byte(presented))
	return sum[:]
}

// RandomString draws length characters from the alphabet with the crypto
// source, drawing per character so a short code is still a real draw and not
// a modulo bias.
func RandomString(length int, alphabet string) (string, error) {
	out := make([]byte, length)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("crypto: random: %w", err)
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}
