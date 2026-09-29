package seeders

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/pkg/crypto"
)

// JWKSKeyPair is the material one provisioning run produces: the sealed
// private half and the publishable public half, both JWK JSON, sharing one
// `jwk_` kid and one algorithm.
type JWKSKeyPair struct {
	KeyID     string
	Algorithm string
	KeyType   string
	PublicKey string
	SealedKey string
}

// GenerateJWKSKeyPair produces a fresh signing key pair for the jwks table.
// algorithm names an asymmetric JWS algorithm; a symmetric name is refused —
// a shared secret has no publishable half. The private half is sealed with
// cipher before it leaves this function, so plaintext private material never
// crosses a caller's hands.
func GenerateJWKSKeyPair(algorithm string, cipher *crypto.Cipher) (JWKSKeyPair, error) {
	private, public, err := crypto.GenerateKeyPair(algorithm)
	if err != nil {
		return JWKSKeyPair{}, fmt.Errorf("generate key pair: %w", err)
	}

	// The columns carry the JWK JSON documents, not the base64 envelope the
	// generator writes: the published set and the unseal path both parse the
	// column value directly. Both halves are re-encoded here, so the row and
	// the reader agree on the shape.
	privateParsed, err := crypto.DecodeJWK(private)
	if err != nil {
		return JWKSKeyPair{}, fmt.Errorf("read private key: %w", err)
	}
	publicParsed, err := crypto.DecodeJWK(public)
	if err != nil {
		return JWKSKeyPair{}, fmt.Errorf("read public key: %w", err)
	}
	privateDoc, err := json.Marshal(privateParsed)
	if err != nil {
		return JWKSKeyPair{}, fmt.Errorf("encode private key: %w", err)
	}
	publicDoc, err := json.Marshal(publicParsed)
	if err != nil {
		return JWKSKeyPair{}, fmt.Errorf("encode public key: %w", err)
	}

	// The public half's kid and alg are what the row stores; the same stamp
	// rides the private half, so the pair answers one name.
	kid, _ := publicParsed.KeyID()
	keyType := publicParsed.KeyType().String()

	sealed, err := cipher.Encrypt(string(privateDoc))
	if err != nil {
		return JWKSKeyPair{}, fmt.Errorf("seal private key: %w", err)
	}
	return JWKSKeyPair{
		KeyID:     kid,
		Algorithm: algorithm,
		KeyType:   keyType,
		PublicKey: string(publicDoc),
		SealedKey: sealed,
	}, nil
}

// JWKSKeyPairSeederName is the name this seeder reports under.
const JWKSKeyPairSeederName = "JWKSKeyPairSeeder"

// JWKS returns the seeder that provisions the database's first signing key
// pair. The database is the one signing authority — the environment carries
// no key-pair material — and a fresh database starts with none, so the seed
// writes one row rather than leaving sign-in to fail closed.
//
// cipher must be non-nil: a run without the application secret cannot seal
// the private half, and a plaintext private key must never rest in the
// table. A key that already rests (any active row) skips the provisioning —
// the seeder never rotates or replaces what is there; jwks:generate does.
func JWKS(cipher *crypto.Cipher, algorithm string) Seeder {
	return Seeder{
		Name:  JWKSKeyPairSeederName,
		Apply: applyJWKS(cipher, algorithm),
	}
}

// applyJWKS writes the first signing row when the table holds none.
func applyJWKS(cipher *crypto.Cipher, algorithm string) func(context.Context, datastore.Querier, bool) ([]string, []string, error) {
	return func(ctx context.Context, q datastore.Querier, dryRun bool) (created, skipped []string, err error) {
		created = []string{}

		count, err := countJWKSRows(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		if count > 0 {
			return created, []string{"existing signing key"}, nil
		}
		if dryRun {
			return []string{"jwk (first key pair)"}, nil, nil
		}

		pair, err := GenerateJWKSKeyPair(algorithm, cipher)
		if err != nil {
			return nil, nil, err
		}
		if err := InsertJWKSRow(ctx, q, pair); err != nil {
			return nil, nil, err
		}
		return []string{pair.KeyID}, nil, nil
	}
}

// countJWKSRows answers how many signing keys the table holds. Any row —
// active or retired — counts: a retired one means the deployment already
// provisioned, and a second provisioning would mint a key the rotation
// history never named.
func countJWKSRows(ctx context.Context, q datastore.Querier) (int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(jwks.TableJWKS)

	query, args := sb.Build()
	var total int64
	if err := q.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count jwks rows: %w", err)
	}
	return total, nil
}

// InsertJWKSRow stores one provisioned pair. The conflict is a safety net,
// not the idempotency: the count guard above decides, and a race that
// inserts twice would mint two first keys — refused rather than resolved.
// jwks:generate reuses the insert so both paths write the same shape.
func InsertJWKSRow(ctx context.Context, q datastore.Querier, pair JWKSKeyPair) error {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(jwks.TableJWKS)
	sb.Cols("key_id", "algorithm", "key_type", "public_key", "private_key", "use_for", "is_active")
	sb.Values(pair.KeyID, pair.Algorithm, pair.KeyType, []byte(pair.PublicKey), []byte(pair.SealedKey), jwks.UseSignature, true)

	query, args := sb.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("store signing key pair: %w", err)
	}
	return nil
}

// jwkKeyIDFor was removed: provisioning reads the kid off the public JWK.
