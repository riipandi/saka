package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// KeyHexLength is the length of a hex-encoded key.
const KeyHexLength = KeySize * 2

// ErrInvalidKeyEncoding reports a key that is not a hex-encoded 32-byte value.
var ErrInvalidKeyEncoding = errors.New("crypto: key must be 64 hex characters")

// ErrInvalidHMACKey reports an HMAC secret that is not hex-encoded.
var ErrInvalidHMACKey = errors.New("crypto: HMAC key must be hex-encoded")

// GenerateKeyHex returns a fresh random 32-byte key as 64 hex characters.
// Hex keeps the value copy-safe in env files and shell exports.
func GenerateKeyHex() (string, error) {
	return GenerateRandomHex(KeySize)
}

// GenerateRandomHex returns size random bytes as 2*size hex characters.
func GenerateRandomHex(size int) (string, error) {
	if size <= 0 {
		return "", fmt.Errorf("crypto: invalid random size %d", size)
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("crypto: read random bytes: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// ParseKeyHex decodes a hex-encoded 32-byte key.
func ParseKeyHex(encoded string) ([]byte, error) {
	key, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidKeyEncoding, err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidKeyEncoding, len(key))
	}
	return key, nil
}

// NewCipherFromHex builds a Cipher from a hex-encoded 32-byte key.
func NewCipherFromHex(encoded string) (*Cipher, error) {
	key, err := ParseKeyHex(encoded)
	if err != nil {
		return nil, err
	}
	return NewCipher(key)
}

// ParseHMACKeyHex decodes the hex-encoded HMAC secret AUTH_SECRET_KEY holds.
//
// It does not go through ParseKeyHex: that reader is the AES-256 key's, and it
// insists on exactly 32 bytes. An HMAC secret is as long as its algorithm
// requires — 32, 48, or 64 bytes for HS256, HS384, and HS512 — so
// `key:generate --algorithm HS384` writes a value ParseKeyHex would reject.
func ParseHMACKeyHex(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, fmt.Errorf("%w: value is empty", ErrInvalidHMACKey)
	}
	key, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidHMACKey, err)
	}
	if len(key) < 32 {
		return nil, fmt.Errorf("%w: %d bytes is below the HS256 minimum of 32", ErrInvalidHMACKey, len(key))
	}
	return key, nil
}
