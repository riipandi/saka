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
	"go.jetify.com/typeid"
)

// Environment variable names of the generated secret keys.
const (
	EnvAppSecretKey  = "APP_SECRET_KEY"
	EnvAuthSecretKey = "AUTH_SECRET_KEY"
)

// DefaultSignatureAlgorithm is the JWT algorithm the signing key pair
// uses when none is given — the default saka initialize and
// jwks:generate store with.
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
var generatedKeyOrder = []string{EnvAppSecretKey, EnvAuthSecretKey}

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

// KeyGenerator creates the application secret keys. It emits
// APP_SECRET_KEY and AUTH_SECRET_KEY: the signing key pair is the
// database's (provisioned by saka initialize, rotated by jwks:generate),
// so no key-pair material is written to the environment.
type KeyGenerator struct {
	secretAlgorithm string
}

// NewKeyGenerator builds a KeyGenerator for a signature algorithm name.
// Symmetric algorithms (HS*) select AUTH_SECRET_KEY's algorithm; an
// asymmetric name has no role here anymore and is refused. An empty name
// keeps the default.
func NewKeyGenerator(algorithm string) (*KeyGenerator, error) {
	generator := &KeyGenerator{
		secretAlgorithm: DefaultSecretAlgorithm,
	}
	if algorithm == "" {
		return generator, nil
	}

	alg, ok := jwa.LookupSignatureAlgorithm(algorithm)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}
	if !alg.IsSymmetric() {
		return nil, fmt.Errorf(
			"%w: %q signs the key pair's role, and the signing key pair lives in the database (saka jwks:generate)",
			ErrUnsupportedAlgorithm, algorithm)
	}
	if _, supported := hmacKeySize(algorithm); !supported {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}
	generator.secretAlgorithm = algorithm
	return generator, nil
}

// Algorithms returns the HMAC algorithm in use.
func (g *KeyGenerator) Algorithms() (secret string) {
	return g.secretAlgorithm
}

// Generate returns a fresh APP_SECRET_KEY and the HMAC secret.
func (g *KeyGenerator) Generate() (GeneratedKeys, error) {
	appSecret, err := GenerateKeyHex()
	if err != nil {
		return nil, err
	}
	keys := GeneratedKeys{EnvAppSecretKey: appSecret}

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
// GenerateKeyPair produces a fresh asymmetric signing key pair for the
// database's public.jwks rows: the private half as sealable JWK JSON, the
// public half as publishable JWK JSON, both carrying the algorithm and a
// shared `jwk_` TypeID kid. This is the generator jwks:generate and
// saka initialize store from — the environment never holds key-pair
// material.
func GenerateKeyPair(algorithm string) (private, public string, err error) {
	material, err := rawKeyPair(algorithm)
	if err != nil {
		return "", "", err
	}

	priv, err := jwk.Import(material.private)
	if err != nil {
		return "", "", fmt.Errorf("crypto: import private key: %w", err)
	}
	if metadataErr := setKeyMetadata(priv, algorithm); metadataErr != nil {
		return "", "", metadataErr
	}

	pub, err := jwk.PublicKeyOf(priv)
	if err != nil {
		return "", "", fmt.Errorf("crypto: derive public key: %w", err)
	}
	// The pair shares one kid: a token's header names it and the published
	// set matches it, so the derived public half copies the private half's
	// id rather than minting its own. Only the algorithm is stamped here.
	if algErr := pub.Set(jwk.AlgorithmKey, algorithm); algErr != nil {
		return "", "", fmt.Errorf("crypto: set alg: %w", algErr)
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

// setKeyMetadata stamps the algorithm and a `jwk_` TypeID key ID on the
// key so the published JWKS can be matched by `kid` and so every signing
// key the deployment holds names itself the same way — the database rows
// of the OAuth provider use the same prefix.
func setKeyMetadata(key jwk.Key, algorithm string) error {
	if err := key.Set(jwk.AlgorithmKey, algorithm); err != nil {
		return fmt.Errorf("crypto: set alg: %w", err)
	}
	kid, err := typeid.New[jWKID]()
	if err != nil {
		return fmt.Errorf("crypto: mint kid: %w", err)
	}
	if err := key.Set(jwk.KeyIDKey, kid.String()); err != nil {
		return fmt.Errorf("crypto: set kid: %w", err)
	}
	return nil
}

// jWKIDPrefix is the TypeID prefix of a signing key's `kid`, shared with
// the stored rows the OAuth provider signs from. The prefix lives here
// because the generator stamps the kid into the JWK JSON a deployment
// keeps in its configuration.
type jWKIDPrefix struct{}

func (jWKIDPrefix) Prefix() string { return "jwk" }

// jWKID is the typed identifier a generated key pair carries as `kid`.
type jWKID = typeid.TypeID[jWKIDPrefix]

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

// DecodeJWK reads a key from the base64-encoded JSON encodeJWK writes — the
// form the jwks table's columns store.
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

// rawKeyPair generates the raw private key material an algorithm names.
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
