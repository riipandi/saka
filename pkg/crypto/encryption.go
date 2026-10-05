package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidKeySize reports a key that is not 32 bytes.
var ErrInvalidKeySize = errors.New("crypto: AES-256 requires a 32-byte key")

// ErrCiphertextTooShort reports a value shorter than the nonce prefix.
var ErrCiphertextTooShort = errors.New("crypto: ciphertext is too short")

// ErrMissingPrefix reports a recoverable value stored without the
// enc: marker.
var ErrMissingPrefix = errors.New("crypto: value is missing the enc: prefix")

// Cipher encrypts and decrypts values with AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
	key  []byte
}

// NewCipher builds a Cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKeySize
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES block: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	stored := make([]byte, len(key))
	copy(stored, key)
	return &Cipher{aead: aead, key: stored}, nil
}

// EncPrefix marks a recoverable value sealed by this package; every
// stored ciphertext must carry it exactly once, case-sensitively, at
// the beginning of the string, with no surrounding whitespace. It is
// metadata, not part of the plaintext or ciphertext. Values without
// it are invalid — there is no unprefixed legacy format, and the
// prefix is not a password-hash marker.
const EncPrefix = "enc:"

// Encrypt seals plaintext with a fresh random nonce and returns the
// canonical "enc:<ciphertext>" form.
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	rand.Read(nonce) // never returns an error per the crypto/rand contract

	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return EncPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Fingerprint returns a short, non-secret identifier of the sealing key:
// the first 8 bytes of the SHA-256 over the raw key, hex-encoded. Stored
// ciphertext records the fingerprint of the key that sealed it, so a
// reader can tell "this row was sealed by a key I no longer hold" apart
// from a corrupt row without attempting a decrypt or leaking key material.
func (c *Cipher) Fingerprint() string {
	sum := sha256.Sum256(c.key)
	return hex.EncodeToString(sum[:8])
}

// AuthSealKey derives the 32-byte AES key AUTH_SECRET_KEY seals with. The
// HMAC secret's length follows its signing algorithm (32, 48, or 64 bytes
// for HS256, HS384, HS512), while AES-256 needs exactly 32, so the key is
// the SHA-256 over the secret's raw bytes: any length seals, the same
// secret always derives the same key, and rotating the secret rotates the
// derivation with it.
func AuthSealKey(hmacSecret []byte) []byte {
	key := sha256.Sum256(hmacSecret)
	return key[:]
}

// NewAuthCipher builds the Cipher auth-related sealing uses. The secret is
// AUTH_SECRET_KEY's hex form, and the AES key is derived from it — see
// AuthSealKey. It is deliberately separate from NewCipherFromHex: the
// application secret is consumed raw, the auth secret is consumed through
// the derivation, so a caller cannot seal auth material with the wrong
// half by passing the wrong string.
func NewAuthCipher(hmacSecretHex string) (*Cipher, error) {
	raw, err := ParseHMACKeyHex(hmacSecretHex)
	if err != nil {
		return nil, fmt.Errorf("crypto: auth seal key: %w", err)
	}
	return NewCipher(AuthSealKey(raw))
}

// Decrypt opens a value produced by Encrypt. The prefix must appear
// exactly once at the beginning, case-sensitively; values without it
// are rejected, and plaintext is never treated as encrypted data.
func (c *Cipher) Decrypt(encoded string) (string, error) {
	rest, ok := strings.CutPrefix(encoded, EncPrefix)
	if !ok {
		return "", ErrMissingPrefix
	}
	data, err := base64.RawStdEncoding.DecodeString(rest)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	if len(data) < c.aead.NonceSize() {
		return "", ErrCiphertextTooShort
	}

	nonce, ciphertext := data[:c.aead.NonceSize()], data[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}
