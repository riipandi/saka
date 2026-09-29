package jwks

import (
	"time"

	"go.jetify.com/typeid"
)

// TableJWKS is the table the published key set reads. The rows are
// written by the jwks:generate command, not by this module.
const TableJWKS = "jwks"

// UseSignature is the `use_for` value of a key that signs and verifies.
// A published JWKS carries only these; an `enc` key is never handed to a
// client that asked how to check a signature.
const UseSignature = "sig"

// KeyUsageSignature is the JWK `use` value of a published key, the field a
// client reads to know the key verifies a signature rather than encrypts.
// It is the RFC 7517 spelling of UseSignature, which names a column.
const KeyUsageSignature = "sig"

// JWKSKeyIDPrefix is the TypeID prefix of a stored signing key's identifier.
// The kid rides every token's JOSE header, so the reader of a token can tell
// what the key names without a lookup.
type JWKSKeyIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (JWKSKeyIDPrefix) Prefix() string { return "jwk" }

// JWKSKeyID is the typed identifier of one row of the jwks table, in its
// wire form.
type JWKSKeyID = typeid.TypeID[JWKSKeyIDPrefix]

// StoredKey is one row of TableJWKS, reduced to what publishing a key
// needs. The private key column is deliberately absent: this repository
// reads the public side only, so a private key cannot reach the endpoint
// through it. The key type is not carried either — it is inside the stored
// JWK, and a second copy could disagree with it.
type StoredKey struct {
	// KeyID is the `kid` a token names to select this key.
	KeyID string
	// Algorithm is the JWS algorithm the key is used with.
	Algorithm string
	// PublicKey is the stored public key. It is UTF-8 JWK JSON for a key
	// the provider generated, and an unprefixed or malformed value is a
	// row this module refuses rather than publishes.
	PublicKey []byte
	// ExpiresAt is when the key stops being valid. A nil value never
	// expires; an expired row is filtered out by the query.
	ExpiresAt *time.Time
}

// SigningKeyPair is one row of TableJWKS with the sealed private key, the
// shape the OAuth provider signs from. It exists so the provider can mint
// tokens with a database key; the publishing path never sees this type.
type SigningKeyPair struct {
	// KeyID is the `kid` a token names to select this key.
	KeyID string
	// Algorithm is the JWS algorithm the key is used with.
	Algorithm string
	// PrivateKey is the sealed (`enc:`) private JWK JSON of the row.
	PrivateKey []byte
	// SealFP is the fingerprint of the key that sealed PrivateKey. A
	// reader whose current fingerprint differs knows the auth secret
	// rotated under this row; the row is retired, not decrypted.
	SealFP string
}
