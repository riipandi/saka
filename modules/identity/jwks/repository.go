package jwks

import (
	"context"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/tango/internal/datastore"
)

// Repository reads the keys the database publishes.
type Repository struct {
	db datastore.Querier
}

// NewRepository builds the repository over the shared pool.
func NewRepository(db datastore.Querier) *Repository {
	return &Repository{db: db}
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
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key_id", "algorithm", "private_key")
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
		if err := rows.Scan(&key.KeyID, &key.Algorithm, &key.PrivateKey); err != nil {
			return nil, fmt.Errorf("jwks: scan active key pair: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jwks: read active key pairs: %w", err)
	}
	return keys, nil
}
