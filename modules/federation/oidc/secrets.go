package oidc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"time"
	"uuid"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/pkg/crypto"
)

// Secret is one stored client secret: the hash is the value's whole
// presence, the prefix is what an operator tells rows apart by, and the
// window is the secret's own — several live secrets per client are
// legitimate, because a rotation is an addition followed by a deletion.
type Secret struct {
	ID        string
	Algorithm string
	Hash      string
	Prefix    string
	CreatedAt time.Time
	ExpiresAt *time.Time
}

// SecretView is one secret as the procedures answer it. The hash never
// travels; the activity is the window judged at the instant the view is
// built, not a column.
type SecretView struct {
	ID        string
	Prefix    string
	Active    bool
	CreatedAt time.Time
	ExpiresAt *time.Time
}

// view renders the stored secret at the given instant.
func (s Secret) view(now time.Time) SecretView {
	return SecretView{
		ID:        s.ID,
		Prefix:    s.Prefix,
		CreatedAt: s.CreatedAt,
		ExpiresAt: s.ExpiresAt,
		Active:    s.ExpiresAt == nil || s.ExpiresAt.After(now),
	}
}

// credentials is the JSONB document the Credentials column stores. The
// federated identities — the JWT-profile client authentication — join when
// that feature does; today the document is the secrets alone.
type credentials struct {
	Secrets []Secret `json:"secrets"`
}

// readCredentials renders the stored JSONB onto its document. A column no
// write has filled yet is an empty set, not a failure — the zero document is
// the state a client born without secrets is in.
func readCredentials(raw []byte) credentials {
	var parsed credentials
	if len(raw) == 0 {
		return parsed
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// A document the reader cannot parse names no secret: the view
		// answers the empty set, and the secret procedures see the client
		// as it can act on it. A malformed document is a defect a write
		// would have produced; the read refuses to invent rows from it.
		return credentials{}
	}
	return parsed
}

// CreatedSecret is what a secret creation answers: the view and the raw
// value, shown exactly once.
type CreatedSecret struct {
	Secret SecretView
	Value  string
}

// ListSecrets answers a client's secrets as their views — prefixes and
// windows, never the values.
func (s *Service) ListSecrets(ctx context.Context, id string) ([]SecretView, error) {
	view, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return view.Secrets, nil
}

// CreateSecret mints one more secret. Several live secrets per client are
// legitimate — a rotation is an addition followed by a deletion, never a
// swap — so the write appends to the document the row lock held. The raw
// value exists in this response alone.
func (s *Service) CreateSecret(ctx context.Context, id, secretValue string, expiresAt *time.Time) (CreatedSecret, error) {
	var issued CreatedSecret
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, err := s.lockClient(ctx, tx, id)
		if err != nil {
			return err
		}

		stored := readCredentials(row.Credentials)
		secret, raw, err := s.newSecret(expiresAt)
		if err != nil {
			return err
		}
		if secretValue != "" {
			secret, raw, err = s.suppliedSecret(secretValue, expiresAt)
			if err != nil {
				return err
			}
		}
		stored.Secrets = append(stored.Secrets, secret)
		if err := s.repo.writeCredentials(ctx, tx, id, stored); err != nil {
			return err
		}

		s.audit.Record(ctx, tx, fwaudit.Entry{
			Event:        audit.EventOidcClientSecretCreated,
			Status:       fwaudit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload:      map[string]string{"client_id": id, "secret_id": secret.ID},
		})
		issued = CreatedSecret{Secret: secret.view(s.now()), Value: raw}
		return nil
	})
	if err != nil {
		return CreatedSecret{}, err
	}
	return issued, nil
}

// DeleteSecret withdraws one secret. The spend belongs in the write's WHERE
// in spirit: the document is rewritten only when the filter removed a row,
// so a concurrent rotation and a lost race leave the secret the client
// still holds.
func (s *Service) DeleteSecret(ctx context.Context, id, secretID string) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, err := s.lockClient(ctx, tx, id)
		if err != nil {
			return err
		}

		stored := readCredentials(row.Credentials)
		kept := make([]Secret, 0, len(stored.Secrets))
		found := false
		for _, secret := range stored.Secrets {
			if secret.ID == secretID {
				found = true
				continue
			}
			kept = append(kept, secret)
		}
		if !found {
			return ErrSecretNotFound
		}
		if err := s.repo.writeCredentials(ctx, tx, id, credentials{Secrets: kept}); err != nil {
			return err
		}

		s.audit.Record(ctx, tx, fwaudit.Entry{
			Event:        audit.EventOidcClientSecretDeleted,
			Status:       fwaudit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload:      map[string]string{"client_id": id, "secret_id": secretID},
		})
		return nil
	})
}

// newSecret draws a secret the way the machine credentials are drawn: the
// full alphanumeric alphabet with the crypto source, thirty-two characters.
// Only the SHA-256 hash and the four-character prefix are stored.
func (s *Service) newSecret(expiresAt *time.Time) (Secret, string, error) {
	raw, err := crypto.RandomString(32, crypto.AlphabetAlphanumeric)
	if err != nil {
		return Secret{}, "", fmt.Errorf("oidc: draw secret: %w", err)
	}
	return sealSecret(raw, expiresAt, s.now())
}

// suppliedSecret stores a value the operator already holds, so a credential
// issued elsewhere can be carried over. The hash is of the value as it will
// be presented — nothing is trimmed or reshaped. The carried-over value is
// hashed the way passwords are hashed — a salted, memory-hard PHC digest,
// not the bare SHA-256 the machine-minted secrets use: an operator-chosen
// value is a low-entropy guessable one, and RFC 9700 asks the store not to
// make it cheap to attack.
func (s *Service) suppliedSecret(raw string, expiresAt *time.Time) (Secret, string, error) {
	digest, err := crypto.NewPasswordHasher().Hash(raw)
	if err != nil {
		return Secret{}, "", fmt.Errorf("oidc: hash supplied secret: %w", err)
	}
	prefix := raw
	if len(prefix) > 4 {
		prefix = prefix[:4]
	}
	return Secret{
		ID:        uuid.NewV7().String(),
		Algorithm: "phc",
		Hash:      digest,
		Prefix:    prefix,
		CreatedAt: s.now(),
		ExpiresAt: expiresAt,
	}, raw, nil
}

// sealSecret reduces a raw value to its stored presence: the hash, the
// prefix, and the identifiers the views read.
func sealSecret(raw string, expiresAt *time.Time, now time.Time) (Secret, string, error) {
	digest := sha256.Sum256([]byte(raw))
	prefix := raw
	if len(prefix) > 4 {
		prefix = prefix[:4]
	}
	return Secret{
		ID:        uuid.NewV7().String(),
		Algorithm: "sha256",
		Hash:      hex.EncodeToString(digest[:]),
		Prefix:    prefix,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}, raw, nil
}
