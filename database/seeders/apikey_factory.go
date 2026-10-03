package seeders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/crypto"
)

// APIKeySeederName is the name this seeder reports under.
const APIKeySeederName = "APIKeySeeder"

// The shape of a presented key, the same draw the apikey service makes: an
// eight-character prefix an operator reads, a separator, and a thirty-two
// character secret a client sends. The lengths live here as literals because
// the service keeps them private; the seeder test pins the two halves
// against the real authenticate path, so a drift fails loudly.
const (
	apiKeyPrefixLength = 8
	apiKeySecretLength = 32
	apiKeySeparator    = "."
)

// scenarioAPIKeys are the machine credentials a development database carries:
// one live key an integration test can present, one at the edge of its
// window, and one already withdrawn. All of them act as the default account.
var scenarioAPIKeys = []scenarioAPIKey{
	{
		name:        "Development Key",
		description: "The standing credential a local integration test presents",
		lifetime:    90 * 24 * time.Hour,
	},
	{
		// At the edge of expiry: authenticate still admits it today, and a
		// run tomorrow refuses it — the boundary the expiry checks police.
		name:        "Expiring Key",
		description: "Almost at its expiry, for the boundary checks",
		lifetime:    time.Hour,
	},
	{
		// Revoked the moment it was created: the row exists, the credential
		// answers nothing.
		name:        "Revoked Key",
		description: "Withdrawn on creation, for the revoked-state checks",
		lifetime:    90 * 24 * time.Hour,
		revoked:     true,
	},
}

// scenarioAPIKey is one key the seeder writes: the name its owner's list
// shows, the window it lives in, and whether it starts withdrawn.
type scenarioAPIKey struct {
	name        string
	description string
	lifetime    time.Duration
	revoked     bool
}

// APIKey returns the seeder for the development machine credentials. It runs
// after the user seeder: every key acts as an account row.
func APIKey() Seeder {
	return Seeder{
		Name:  APIKeySeederName,
		Apply: applyAPIKeys,
	}
}

// applyAPIKeys creates the scenario keys under the default account.
//
// Each insert is guarded by the owner's unique name index, so a second run
// keeps the existing rows and reports them as skipped. The raw credential of
// a key this run created appears exactly once — on its report line — because
// only its hash survives the insert; a dry run can report no credential at
// all, since the one it would draw is not the one a real run would.
func applyAPIKeys(
	ctx context.Context,
	q datastore.Querier,
	dryRun bool,
) (created, skipped []string, err error) {
	ownerID, err := userIDByEmail(ctx, q, DefaultUser.Email)
	if err != nil {
		return nil, nil, err
	}
	if ownerID == (uuid.UUID{}) {
		// The account the user seeder writes is the only owner these keys
		// can act as; without it there is nothing to seed. A development
		// database always runs the full list, so this answers only on a
		// standalone run.
		return created, skipped, nil
	}

	for i := range scenarioAPIKeys {
		key := scenarioAPIKeys[i]

		if dryRun {
			exists, existsErr := apiKeyExists(ctx, q, ownerID, key.name)
			if existsErr != nil {
				return nil, nil, existsErr
			}
			if exists {
				skipped = append(skipped, key.name)
			} else {
				created = append(created, key.name)
			}
			continue
		}

		line, inserted, keyErr := insertAPIKey(ctx, q, ownerID, key)
		if keyErr != nil {
			return nil, nil, keyErr
		}
		if inserted {
			created = append(created, line)
		} else {
			skipped = append(skipped, key.name)
		}
	}
	return created, skipped, nil
}

// insertAPIKey draws one credential, stores its hash, and answers the report
// line — owner, name, and the raw credential a client presents — plus
// whether this run created the row.
func insertAPIKey(ctx context.Context, q datastore.Querier, ownerID uuid.UUID, key scenarioAPIKey) (string, bool, error) {
	raw, err := drawAPIKey()
	if err != nil {
		return "", false, err
	}

	now := time.Now().UTC()
	expiresAt := now.Add(key.lifetime)
	var revokedAt *time.Time
	if key.revoked {
		revokedAt = &now
	}
	description := key.description

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableAPIKeys)
	ib.Cols("id", "user_id", "name", "prefix", "key_hash", "description", "expires_at", "created_at", "revoked_at")
	ib.Values(uuid.NewV7(), ownerID, key.name, apiKeyPrefixOf(raw), crypto.HashTokenBytes(raw),
		description, expiresAt, now, revokedAt)
	ib.SQL("ON CONFLICT DO NOTHING")
	ib.Returning("id")

	query, args := ib.Build()
	var rawID string
	err = q.QueryRow(ctx, query, args...).Scan(&rawID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("apikey seeder: %s: %w", key.name, err)
	}
	if _, err := uuid.Parse(rawID); err != nil {
		return "", false, fmt.Errorf("apikey seeder: %s: %w", key.name, err)
	}
	return DefaultUser.Email + " (" + key.name + " " + raw + ")", true, nil
}

// drawAPIKey draws one presented credential in the shape the apikey service
// defines: prefix, separator, secret, every character alphanumeric.
func drawAPIKey() (string, error) {
	prefix, err := crypto.RandomString(apiKeyPrefixLength, crypto.AlphabetAlphanumeric)
	if err != nil {
		return "", fmt.Errorf("apikey seeder: prefix: %w", err)
	}
	secret, err := crypto.RandomString(apiKeySecretLength, crypto.AlphabetAlphanumeric)
	if err != nil {
		return "", fmt.Errorf("apikey seeder: secret: %w", err)
	}
	return prefix + apiKeySeparator + secret, nil
}

// apiKeyPrefixOf reads the prefix a drawn key carries, the half the row
// stores for an operator to read.
func apiKeyPrefixOf(presented string) string {
	for i, char := range presented {
		if string(char) == apiKeySeparator {
			return presented[:i]
		}
	}
	return presented
}

// apiKeyExists reports whether the owner already holds a key of the name —
// the same uniqueness the `(name, owner)` index enforces.
func apiKeyExists(ctx context.Context, q datastore.Querier, ownerID uuid.UUID, name string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1").From(entity.TableAPIKeys).
		Where(sb.Equal("user_id", ownerID), sb.Equal("name", name))

	query, args := sb.Build()
	var one int
	err := q.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// userIDByEmail reads the identifier of the account the email names, or the
// zero UUID when no account answers.
func userIDByEmail(ctx context.Context, q datastore.Querier, email string) (uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id").From(entity.TableUsers).Where(sb.Equal("email", email))

	query, args := sb.Build()
	var rawID string
	err := q.QueryRow(ctx, query, args...).Scan(&rawID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, nil
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("apikey seeder: %s: %w", email, err)
	}
	parsed, err := uuid.Parse(rawID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("apikey seeder: %s: %w", email, err)
	}
	return parsed, nil
}
