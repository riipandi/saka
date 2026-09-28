package crypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
)

// Environment variable names of the generated secret keys.
const (
	EnvAppSecretKey   = "APP_SECRET_KEY"
	EnvAuthPrivateKey = "AUTH_PRIVATE_KEY"
	EnvAuthPublicKey  = "AUTH_PUBLIC_KEY"
	EnvAuthSecretKey  = "AUTH_SECRET_KEY"
)

// DefaultSignatureAlgorithm is the JWT algorithm used for the key pair
// when none is given.
const DefaultSignatureAlgorithm = "ES256"

// DefaultSecretAlgorithm is the HMAC algorithm used for AUTH_SECRET_KEY
// when none is given.
const DefaultSecretAlgorithm = "HS256"

// rsaKeyBits is the modulus size for the RSA-based algorithms.
const rsaKeyBits = 4096

// ErrUnsupportedAlgorithm reports a signature algorithm without a generator.
var ErrUnsupportedAlgorithm = errors.New("crypto: unsupported signature algorithm")

// GeneratedKeys holds generated values keyed by environment variable name.
type GeneratedKeys map[string]string

// generatedKeyOrder is the order used when reporting generated variables.
var generatedKeyOrder = []string{EnvAppSecretKey, EnvAuthPrivateKey, EnvAuthPublicKey, EnvAuthSecretKey}

// Names returns the generated variable names in canonical order.
func (k GeneratedKeys) Names() []string {
	names := make([]string, 0, len(k))
	for _, name := range generatedKeyOrder {
		if _, ok := k[name]; ok {
			names = append(names, name)
		}
	}
	return names
}

// KeyGenerator creates the application secret keys. It always emits
// APP_SECRET_KEY, AUTH_PRIVATE_KEY, AUTH_PUBLIC_KEY, and AUTH_SECRET_KEY:
// the key pair and the HMAC secret are independent, so an algorithm
// selects the role it can fill and the other role keeps its default.
type KeyGenerator struct {
	keyAlgorithm    string
	secretAlgorithm string
}

// NewKeyGenerator builds a KeyGenerator for a signature algorithm name.
// Symmetric algorithms (HS*) set AUTH_SECRET_KEY; asymmetric algorithms
// set the key pair. An empty name keeps both defaults.
func NewKeyGenerator(algorithm string) (*KeyGenerator, error) {
	generator := &KeyGenerator{
		keyAlgorithm:    DefaultSignatureAlgorithm,
		secretAlgorithm: DefaultSecretAlgorithm,
	}
	if algorithm == "" {
		return generator, nil
	}

	alg, ok := jwa.LookupSignatureAlgorithm(algorithm)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}
	if alg.IsSymmetric() {
		if _, supported := hmacKeySize(algorithm); !supported {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
		}
		generator.secretAlgorithm = algorithm
		return generator, nil
	}
	if !supportsKeyPair(algorithm) {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}
	generator.keyAlgorithm = algorithm
	return generator, nil
}

// Algorithms returns the key-pair and HMAC algorithms in use.
func (g *KeyGenerator) Algorithms() (keyPair, secret string) {
	return g.keyAlgorithm, g.secretAlgorithm
}

// Generate returns a fresh APP_SECRET_KEY, a signing key pair encoded as
// base64 JWK JSON, and the HMAC secret.
func (g *KeyGenerator) Generate() (GeneratedKeys, error) {
	appSecret, err := GenerateKeyHex()
	if err != nil {
		return nil, err
	}
	keys := GeneratedKeys{EnvAppSecretKey: appSecret}

	private, public, err := g.keyPair()
	if err != nil {
		return nil, err
	}
	keys[EnvAuthPrivateKey] = private
	keys[EnvAuthPublicKey] = public

	alg, _ := jwa.LookupSignatureAlgorithm(g.secretAlgorithm)
	secret, err := symmetricSecret(alg)
	if err != nil {
		return nil, err
	}
	keys[EnvAuthSecretKey] = secret
	return keys, nil
}

// keyPair generates the signing key and returns both sides as
// base64-encoded JWK JSON.
func (g *KeyGenerator) keyPair() (private, public string, err error) {
	material, err := rawKeyPair(g.keyAlgorithm)
	if err != nil {
		return "", "", err
	}

	priv, err := jwk.Import(material.private)
	if err != nil {
		return "", "", fmt.Errorf("crypto: import private key: %w", err)
	}
	if metadataErr := setKeyMetadata(priv, g.keyAlgorithm); metadataErr != nil {
		return "", "", metadataErr
	}

	pub, err := jwk.PublicKeyOf(priv)
	if err != nil {
		return "", "", fmt.Errorf("crypto: derive public key: %w", err)
	}
	if metadataErr := setKeyMetadata(pub, g.keyAlgorithm); metadataErr != nil {
		return "", "", metadataErr
	}

	private, err = encodeJWK(priv)
	if err != nil {
		return "", "", err
	}
	public, err = encodeJWK(pub)
	if err != nil {
		return "", "", err
	}
	return private, public, nil
}

// setKeyMetadata stamps the algorithm and a thumbprint-derived key ID on
// the key so the published JWKS can be matched by `kid`.
func setKeyMetadata(key jwk.Key, algorithm string) error {
	if err := key.Set(jwk.AlgorithmKey, algorithm); err != nil {
		return fmt.Errorf("crypto: set alg: %w", err)
	}
	if err := jwk.AssignKeyID(key); err != nil {
		return fmt.Errorf("crypto: assign kid: %w", err)
	}
	return nil
}

// encodeJWK serializes a key to base64-encoded JSON. Raw (unpadded)
// base64 keeps the value free of `=` so it stays readable unquoted in
// an env file.
func encodeJWK(key jwk.Key) (string, error) {
	encoded, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("crypto: marshal JWK: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(encoded), nil
}

// DecodeJWK reads a key from the base64-encoded JSON encodeJWK writes. It is
// the other half of that pair, so a caller reading AUTH_PRIVATE_KEY or
// AUTH_PUBLIC_KEY does not restate the encoding.
func DecodeJWK(encoded string) (jwk.Key, error) {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode JWK: %w", err)
	}
	key, err := jwk.ParseKey(raw)
	if err != nil {
		return nil, fmt.Errorf("crypto: parse JWK: %w", err)
	}
	return key, nil
}

// keyMaterial holds a generated raw private key.
type keyMaterial struct {
	private any
}

// supportsKeyPair reports whether the algorithm has a raw key generator.
func supportsKeyPair(algorithm string) bool {
	switch algorithm {
	case "ES256", "ES384", "ES512", "EdDSA",
		"RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		return true
	default:
		return false
	}
}

func rawKeyPair(algorithm string) (keyMaterial, error) {
	switch algorithm {
	case "ES256":
		return generateECDSA(elliptic.P256())
	case "ES384":
		return generateECDSA(elliptic.P384())
	case "ES512":
		return generateECDSA(elliptic.P521())
	case "EdDSA":
		return generateEd25519()
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		return generateRSA()
	default:
		return keyMaterial{}, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}
}

// generateECDSA creates an ECDSA key on the given curve.
func generateECDSA(curve elliptic.Curve) (keyMaterial, error) {
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		return keyMaterial{}, fmt.Errorf("crypto: generate ECDSA key: %w", err)
	}
	return keyMaterial{private: key}, nil
}

func generateEd25519() (keyMaterial, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return keyMaterial{}, fmt.Errorf("crypto: generate Ed25519 key: %w", err)
	}
	return keyMaterial{private: key}, nil
}

func generateRSA() (keyMaterial, error) {
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return keyMaterial{}, fmt.Errorf("crypto: generate RSA key: %w", err)
	}
	return keyMaterial{private: key}, nil
}

// symmetricSecret returns a hex-encoded secret matching the HMAC
// algorithm minimum key length.
func symmetricSecret(alg jwa.SignatureAlgorithm) (string, error) {
	size, ok := hmacKeySize(alg.String())
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
	return GenerateRandomHex(size)
}

// hmacKeySize returns the minimum key length in bytes per RFC 7518.
func hmacKeySize(algorithm string) (int, bool) {
	switch algorithm {
	case "HS256":
		return 32, true
	case "HS384":
		return 48, true
	case "HS512":
		return 64, true
	default:
		return 0, false
	}
}

// HMACAlgorithmForSecret is the algorithm an HMAC secret of a given length
// signs with. It is the single source of the length-to-algorithm rule:
// key:generate sizes the secret by it (hmacKeySize) and the jwks service
// reads the algorithm back from the secret's length by it, so the two ends
// of AUTH_SECRET_KEY cannot drift apart.
//
// The length is the only signal a hex secret carries — there is no alg
// member on a symmetric key — so a secret sized outside this table (shorter
// than 32, or between the table's steps) reports not-ok and the caller
// refuses it.
func HMACAlgorithmForSecret(length int) (string, bool) {
	switch {
	case length >= 64:
		return "HS512", true
	case length >= 48:
		return "HS384", true
	case length >= 32:
		return "HS256", true
	default:
		return "", false
	}
}
