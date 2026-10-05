package seeders_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/seeders"
	"github.com/riipandi/saka/modules/appconfig"
)

// The catalog the settings seeder writes, in the order its report carries
// them: the catalog's own order, which the feature sorts nowhere — the
// seeder writes in declaration order and the listing answers by key.
var expectedSettings = func() []string {
	defs := appconfig.Catalog()
	keys := make([]string, 0, len(defs))
	for _, def := range defs {
		if !def.Sealed {
			keys = append(keys, def.Key)
		}
	}
	return keys
}()

// TestSettingsSeederFillsTheCatalog pins what a fresh database gains: one
// row per non-sealed catalog item, at the value the catalog declares.
func TestSettingsSeederFillsTheCatalog(t *testing.T) {
	pool := newSeededPool(t)

	results, err := seeders.Run(t.Context(), pool, false, seeders.Settings())
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, expectedSettings, results[0].Created)
	assert.Equal(t, len(expectedSettings), countRows(t, pool, `SELECT count(*) FROM public.app_settings`))

	// The value each row rests at is the catalog's own, not a seed-time
	// invention: the two sources must not drift.
	for _, def := range appconfig.Catalog() {
		if def.Sealed {
			continue
		}
		assert.Equal(t, def.Default, settingValue(t, pool, def.Key), def.Key)
	}
}

// TestSettingsSeederIsIdempotent runs the seeder twice: the second run
// creates nothing — a key the table already rests is an override the seeder
// never overwrites.
func TestSettingsSeederIsIdempotent(t *testing.T) {
	pool := newSeededPool(t)

	_, err := seeders.Run(t.Context(), pool, false, seeders.Settings())
	require.NoError(t, err)

	results, err := seeders.Run(t.Context(), pool, false, seeders.Settings())
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Empty(t, results[0].Created)
	assert.Equal(t, expectedSettings, results[0].Skipped)
	assert.Equal(t, len(expectedSettings), countRows(t, pool, `SELECT count(*) FROM public.app_settings`))
}

// TestSettingsSeedsWithTheRest pins the seam the commands use: All carries
// the seeder for migrate:seed and System carries it for initialize, so a
// fresh database is filled by either.
func TestSettingsSeedsWithTheRest(t *testing.T) {
	pool := newSeededPool(t)

	results := runSeeders(t, pool, false)

	assert.Equal(t, seeders.SettingsSeederName, results[len(results)-1].Name)
	assert.Equal(t, len(expectedSettings), countRows(t, pool, `SELECT count(*) FROM public.app_settings`))
}

// TestSettingsSeederDryRunReportsWithoutWriting pins the dry run: the report
// names the rows an apply would create, and the table stays empty.
func TestSettingsSeederDryRunReportsWithoutWriting(t *testing.T) {
	pool := newSeededPool(t)

	results, err := seeders.Run(t.Context(), pool, true, seeders.Settings())
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, expectedSettings, results[0].Created)
	assert.Zero(t, countRows(t, pool, `SELECT count(*) FROM public.app_settings`))

	// Once the rows rest, the same dry run reports them as skipped.
	_, err = seeders.Run(t.Context(), pool, false, seeders.Settings())
	require.NoError(t, err)

	results, err = seeders.Run(t.Context(), pool, true, seeders.Settings())
	require.NoError(t, err)
	assert.Empty(t, results[0].Created)
	assert.Equal(t, expectedSettings, results[0].Skipped)
}

// settingValue reads one row the way the feature's own scan would.
func settingValue(t *testing.T, pool *datastore.Postgres, key string) string {
	t.Helper()

	var value string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT value FROM public.app_settings WHERE key = $1`, key).Scan(&value))
	return value
}
