package seeders

import (
	"context"
	"errors"
	"fmt"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/appconfig"
)

// SettingsSeederName is the name this seeder reports under.
const SettingsSeederName = "SettingsSeeder"

// Settings returns the seeder that fills the settings catalog: one row per
// non-sealed catalog item at its default value, so a fresh database answers
// the same listing a live one does and an operator edits from rows instead
// of an empty table. A key the table already rests is skipped — the seeder
// never overwrites an override.
//
// A sealed item is skipped on purpose: its stored form is ciphertext and the
// seeder holds no cipher, so the item waits for its first Update rather than
// resting in the clear.
func Settings() Seeder {
	return Seeder{
		Name:  SettingsSeederName,
		Apply: applySettings,
	}
}

// applySettings writes the catalog defaults.
//
// The conflict is the idempotency: `ON CONFLICT DO NOTHING` with a returning
// key tells created from skipped in the same statement, so a re-run reports
// the rows it left alone. A dry run reads the table instead of writing it and
// reports what the insert would create.
func applySettings(ctx context.Context, q datastore.Querier, dryRun bool) (created, skipped []string, err error) {
	created, skipped = []string{}, []string{}

	for _, def := range appconfig.Catalog() {
		if def.Sealed {
			skipped = append(skipped, def.Key+" (sealed)")
			continue
		}

		if dryRun {
			exists, existsErr := settingRests(ctx, q, def.Key)
			if existsErr != nil {
				return nil, nil, existsErr
			}
			if exists {
				skipped = append(skipped, def.Key)
				continue
			}
			created = append(created, def.Key)
			continue
		}

		ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
		ib.InsertInto(appconfig.SettingTable)
		ib.Cols("key", "value")
		ib.Values(def.Key, def.Default)
		ib.SQL("ON CONFLICT (key) DO NOTHING RETURNING key")

		query, args := ib.Build()
		var key string
		scanErr := q.QueryRow(ctx, query, args...).Scan(&key)
		switch {
		case scanErr == nil:
			created = append(created, def.Key)
		case errors.Is(scanErr, datastore.ErrNoRows):
			skipped = append(skipped, def.Key)
		default:
			return nil, nil, fmt.Errorf("seed settings %s: %w", def.Key, scanErr)
		}
	}
	return created, skipped, nil
}

// settingRests answers whether the table holds a row for the key — the dry
// run's stand-in for the conflict the live insert resolves.
func settingRests(ctx context.Context, q datastore.Querier, key string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key")
	sb.From(appconfig.SettingTable)
	sb.Where(sb.Equal("key", key))

	query, args := sb.Build()
	var resting string
	if scanErr := q.QueryRow(ctx, query, args...).Scan(&resting); scanErr != nil {
		if errors.Is(scanErr, datastore.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("seed settings %s: %w", key, scanErr)
	}
	return true, nil
}
