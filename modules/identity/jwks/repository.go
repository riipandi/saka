package jwks

import (
	"context"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/crypto"
)

// Repository reads the keys the database publishes, and writes the
// provisioning and retirement a rotation runs.
type Repository struct {
	db     datastore.Querier
	cipher *crypto.Cipher
}

// NewRepository builds the repository over the shared pool. The cipher
// seals a generated pair's private half; it is the auth secret's cipher,
// the same one the unseal path opens with, so a generated row always
// carries the current fingerprint.
func NewRepository(db datastore.Querier, cipher *crypto.Cipher) *Repository {
	return &Repository{db: db, cipher: cipher}
}

// ActiveSigningKeys returns the keys that are currently valid for signature
// verification: active, marked `sig`, and either without an expiry or with one
// still in the future.
//
// The private key column is not selected, so a private key cannot reach a
// caller of this method. A row whose stored public key cannot be read as a
// key is skipped rather than failing the whole set: one unusable row must not
// take the published keyset down for every client.
func (r *Repository) ActiveSigningKeys(ctx context.Context) ([]StoredKey, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("jwks: no database is wired")
	}
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key_id", "algorithm", "public_key", "expires_at")
	sb.From(TableJWKS)
	sb.Where(
		sb.Equal("is_active", true),
		sb.Equal("use_for", UseSignature),
		sb.Or(
			sb.IsNull("expires_at"),
			sb.GreaterThan("expires_at", time.Now()),
		),
	)
	sb.OrderBy("key_id")
	query, args := sb.Build()

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("jwks: read active keys: %w", err)
	}
	defer rows.Close()

	var keys []StoredKey
	for rows.Next() {
		var key StoredKey
		if err := rows.Scan(&key.KeyID, &key.Algorithm, &key.PublicKey, &key.ExpiresAt); err != nil {
			return nil, fmt.Errorf("jwks: scan active key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jwks: read active keys: %w", err)
	}
	return keys, nil
}

// ActiveSigningKeyPairs returns the same rows as ActiveSigningKeys, with the
// sealed private key in place of the public one. It is the read the OAuth
// provider signs from; the publishing path must not use it.
func (r *Repository) ActiveSigningKeyPairs(ctx context.Context) ([]SigningKeyPair, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("jwks: no database is wired")
	}
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key_id", "algorithm", "private_key", "seal_fp")
	sb.From(TableJWKS)
	sb.Where(
		sb.Equal("is_active", true),
		sb.Equal("use_for", UseSignature),
		sb.IsNotNull("private_key"),
		sb.Or(
			sb.IsNull("expires_at"),
			sb.GreaterThan("expires_at", time.Now()),
		),
	)
	sb.OrderBy("key_id")
	query, args := sb.Build()

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("jwks: read active key pairs: %w", err)
	}
	defer rows.Close()

	var keys []SigningKeyPair
	for rows.Next() {
		var key SigningKeyPair
		if err := rows.Scan(&key.KeyID, &key.Algorithm, &key.PrivateKey, &key.SealFP); err != nil {
			return nil, fmt.Errorf("jwks: scan active key pair: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jwks: read active key pairs: %w", err)
	}
	return keys, nil
}

// insertProvisionedPair stores one generated pair. The conflict is a
// safety net, not the idempotency: a race that inserts twice would mint
// two signing rows — refused rather than resolved. The seeders and the
// jwks:generate command share the insert, so every provisioning path
// writes the same row shape.
func insertProvisionedPair(ctx context.Context, db datastore.Querier, pair ProvisionedPair) error {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(TableJWKS)
	sb.Cols("key_id", "algorithm", "key_type", "public_key", "private_key", "seal_fp", "use_for", "is_active")
	sb.Values(pair.KeyID, pair.Algorithm, pair.KeyType, []byte(pair.PublicKey), []byte(pair.SealedKey), pair.SealFP, UseSignature, true)

	query, args := sb.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("store signing key pair: %w", err)
	}
	return nil
}

// RetireStaleSeals deactivates every active signing row whose seal
// fingerprint no longer matches current. It is the write half of the
// auth-secret rotation: rows sealed by a key the process no longer holds
// can never be unsealed again, so they are retired in place — the row and
// its kid stay in the rotation history, the published set stops offering
// them. It answers the number of rows retired.
func (r *Repository) RetireStaleSeals(ctx context.Context, current string) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("jwks: no database is wired")
	}
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(TableJWKS)
	sb.Set(sb.Assign("is_active", false), sb.Assign("updated_at", time.Now()))
	sb.Where(
		sb.Equal("is_active", true),
		sb.NotEqual("seal_fp", current),
	)
	query, args := sb.Build()
	tag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("jwks: retire stale seals: %w", err)
	}
	return tag.RowsAffected(), nil
}

// InsertProvisionedPair stores one generated pair, the Sealer's write
// half of a rotation. It shares the insert the seeders and the jwks:generate
// command use, so every provisioning path writes the same row shape.
func (r *Repository) InsertProvisionedPair(ctx context.Context, pair ProvisionedPair) error {
	return insertProvisionedPair(ctx, r.db, pair)
}

// Querier exposes the write surface the invalidation's audit record rides.
func (r *Repository) Querier() datastore.Querier { return r.db }

// GeneratePair mints one fresh signing pair sealed for this database's
// current auth secret. It is the PairGenerator the service's auto-
// invalidation runs; the free-standing GeneratePairWith is the same
// builder for callers that hold no repository.
func (r *Repository) GeneratePair(algorithm string) (ProvisionedPair, error) {
	return GeneratePairWith(algorithm, r.cipher)
}
