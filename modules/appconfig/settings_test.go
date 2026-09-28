package appconfig

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"
)

// testCipherKey is a 32-byte key in hex, the shape app.secret_key takes.
const testCipherKey = "0f0e0d0c0b0a090807060504030201000102030405060708090a0b0c0d0e0f10"

// settingsService builds the feature over a fresh pool and the test cipher.
func settingsService(t *testing.T) (*Settings, *datastore.Postgres) {
	t.Helper()
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	cipher, err := crypto.NewCipherFromHex(testCipherKey)
	require.NoError(t, err)
	recorder := audit.NewRecorder(slog.New(slog.DiscardHandler))
	settings, err := NewSettings(pool, cipher, recorder)
	require.NoError(t, err)
	return settings, pool
}

// blindSettings builds the feature the way a run without a secret key is
// built: the pool is there, the cipher is not.
func blindSettings(t *testing.T) (*Settings, *datastore.Postgres) {
	t.Helper()
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	recorder := audit.NewRecorder(slog.New(slog.DiscardHandler))
	settings, err := NewSettings(pool, nil, recorder)
	require.NoError(t, err)
	return settings, pool
}

// TestASettingAnswersItsDefaultUntilOverridden covers the catalog model:
// every item answers before anything is written, and the override replaces
// the default, not the item.
func TestASettingAnswersItsDefaultUntilOverridden(t *testing.T) {
	settings, _ := settingsService(t)

	setting, err := settings.GetSetting(t.Context(), "product.name")
	require.NoError(t, err)
	assert.Equal(t, "Tango", setting.Value, "an item at rest answers its catalog default")
	assert.Equal(t, "Tango", setting.Default, "the default is carried with the item")
	assert.False(t, setting.Sealed)
	assert.True(t, setting.Public)

	require.NoError(t, settings.Update(t.Context(), "product.name", "Gringotts"))

	setting, err = settings.GetSetting(t.Context(), "product.name")
	require.NoError(t, err)
	assert.Equal(t, "Gringotts", setting.Value)
	assert.Equal(t, "Tango", setting.Default, "the override never replaces the default")
	require.NotNil(t, setting.UpdatedAt)
}

// TestAnUnknownKeyIsRefusedEverywhere: the catalog owns the keys, so a key
// it does not declare is a refusal on every path — read, write, reset.
func TestAnUnknownKeyIsRefusedEverywhere(t *testing.T) {
	settings, _ := settingsService(t)

	_, err := settings.GetSetting(t.Context(), "made.up.key")
	assert.ErrorIs(t, err, ErrUnknownSetting)

	err = settings.Update(t.Context(), "made.up.key", "x")
	assert.ErrorIs(t, err, ErrUnknownSetting)

	_, err = settings.UpdateFor(t.Context(), "", "made.up.key", "x")
	assert.ErrorIs(t, err, ErrUnknownSetting)

	_, err = settings.ResetFor(t.Context(), "", "made.up.key")
	assert.ErrorIs(t, err, ErrUnknownSetting)
}

// TestASealedValueRestsEncryptedAndReadsOpened is the feature's reason to
// exist: the table never holds the plaintext of a sealed item, and every
// reader — helper or RPC — still answers the value the writer meant.
func TestASealedValueRestsEncryptedAndReadsOpened(t *testing.T) {
	settings, pool := settingsService(t)

	// The catalog carries no sealed item today; the seal path is driven by
	// declaring one here, the way a feature would by adding its entry.
	if _, err := pool.Exec(t.Context(), `DELETE FROM public.settings`); err != nil {
		require.NoError(t, err)
	}
	sealed := SettingDef{Key: "smtp.relay", Default: "", Sealed: true}
	settings.byKey["smtp.relay"] = sealed

	require.NoError(t, settings.Update(t.Context(), "smtp.relay", "s3cret"))

	var resting string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT value FROM public.settings WHERE key = $1`, "smtp.relay").Scan(&resting))
	assert.True(t, isSealed(resting),
		"a sealed item must rest encrypted, not in the clear")

	value, err := settings.Get(t.Context(), "smtp.relay")
	require.NoError(t, err)
	assert.Equal(t, "s3cret", value, "the reader must open what the writer sealed")

	listed, err := settings.List(t.Context())
	require.NoError(t, err)
	for _, item := range listed {
		if item.Key == "smtp.relay" {
			assert.Equal(t, "s3cret", item.Value, "the administrator's list opens sealed rows")
			assert.True(t, item.Sealed)
		}
	}
}

// TestGetOnASealedRowWithoutACipherFails pins the fail-closed path: a run
// with no secret key cannot open a sealed row, so it refuses rather than
// answering ciphertext as if it were the value.
func TestGetOnASealedRowWithoutACipherFails(t *testing.T) {
	settings, pool := blindSettings(t)

	cipher, err := crypto.NewCipherFromHex(testCipherKey)
	require.NoError(t, err)
	sealed, err := cipher.Encrypt("s3cret")
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO public.settings (key, value) VALUES ($1, $2)`, "orphan.key", sealed)
	require.NoError(t, err)

	settings.byKey["orphan.key"] = SettingDef{Key: "orphan.key", Default: "", Sealed: true}
	_, err = settings.Get(t.Context(), "orphan.key")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingCipher)
}

// TestASealedWriteWithoutACipherRefuses: a run with no secret key serves
// plain settings and refuses the one write it cannot protect.
func TestASealedWriteWithoutACipherRefuses(t *testing.T) {
	settings, pool := blindSettings(t)
	settings.byKey["smtp.relay"] = SettingDef{Key: "smtp.relay", Default: "", Sealed: true}

	err := settings.Update(t.Context(), "smtp.relay", "s3cret")
	require.ErrorIs(t, err, ErrSealUnavailable)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.settings`).Scan(&count))
	assert.Zero(t, count, "a refused write must not leave a row")
}

// TestAPlainWriteMayNotForgeTheSealedPrefix keeps the read unambiguous: the
// enc: prefix is how a value declares itself sealed, so a plain item whose
// write begins with it is refused rather than stored as an unreadable value.
func TestAPlainWriteMayNotForgeTheSealedPrefix(t *testing.T) {
	settings, _ := settingsService(t)

	err := settings.Update(t.Context(), "product.name", "enc:not-really-sealed")
	require.ErrorIs(t, err, ErrReservedPrefix)
}

// TestResetRestoresTheCatalogDefault covers the reset and its idempotence:
// the override is dropped, the default answers again, and resetting an item
// already at its default changes nothing.
func TestResetRestoresTheCatalogDefault(t *testing.T) {
	settings, pool := settingsService(t)

	require.NoError(t, settings.Update(t.Context(), "product.name", "Gringotts"))
	_, err := settings.ResetFor(t.Context(), "01a0da1c-cb41-779d-bd02-99b3eb5da999", "product.name")
	require.NoError(t, err)

	setting, err := settings.GetSetting(t.Context(), "product.name")
	require.NoError(t, err)
	assert.Equal(t, "Tango", setting.Value)
	assert.Nil(t, setting.UpdatedAt, "a reset item carries no override instant")

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.settings`).Scan(&count))
	assert.Zero(t, count, "a reset removes the override, it does not write the default")

	// The second reset is the item already at its default: unchanged, and
	// no override left behind.
	_, err = settings.ResetFor(t.Context(), "", "product.name")
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.settings`).Scan(&count))
	assert.Zero(t, count)
}

// TestListMergesTheCatalogWithTheOverrides covers the two reads: List
// answers every catalog item, ListPublic only the ones flagged public,
// both ordered by key and both carrying effective values.
func TestListMergesTheCatalogWithTheOverrides(t *testing.T) {
	settings, _ := settingsService(t)

	require.NoError(t, settings.Update(t.Context(), "product.announcement", "Expecto Patronum"))
	require.NoError(t, settings.Update(t.Context(), "product.support_email", "support@example.com"))

	all, err := settings.List(t.Context())
	require.NoError(t, err)
	require.Len(t, all, len(Catalog()))
	for i := 1; i < len(all); i++ {
		assert.LessOrEqual(t, all[i-1].Key, all[i].Key, "the list is ordered by key")
	}

	public, err := settings.ListPublic(t.Context())
	require.NoError(t, err)
	require.Len(t, public, 3, "every starter item is public")
	values := map[string]string{}
	for _, item := range public {
		values[item.Key] = item.Value
	}
	assert.Equal(t, "Expecto Patronum", values["product.announcement"])
	assert.Equal(t, "Tango", values["product.name"], "an item with no override answers its default")
}

// TestTheSettingChangeLeavesAnAuditRecordWithoutTheValue covers the audit
// contract: a change through the RPC surface records the key and the flags
// in the same transaction, and never the value.
func TestTheSettingChangeLeavesAnAuditRecordWithoutTheValue(t *testing.T) {
	settings, pool := settingsService(t)

	caller := seedUser(t, pool, "granger", "granger@example.com")
	_, err := settings.UpdateFor(t.Context(), caller.String(), "product.announcement", "s3cret")
	require.NoError(t, err)
	_, err = settings.ResetFor(t.Context(), caller.String(), "product.announcement")
	require.NoError(t, err)

	var records int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event IN ($1, $2)`,
		audit.EventSettingReset, audit.EventSettingUpdated).Scan(&records))
	assert.Equal(t, 2, records, "both halves of the ceremony leave their record")

	var payload string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT payload::text FROM public.audit_logs WHERE event = $1`,
		audit.EventSettingUpdated).Scan(&payload))
	assert.Contains(t, payload, "product.announcement")
	assert.NotContains(t, payload, "s3cret", "the audit record must never carry the value")
}

// TestTheCatalogRefusesAPublicSealedItem pins the constructor's check: the
// unauthenticated read publishes every public item's value verbatim, so a
// catalog entry claiming both is a broken deployment, not a runtime answer.
func TestTheCatalogRefusesAPublicSealedItem(t *testing.T) {
	testutils.SkipWithoutDocker(t)
	pool := migratedPool(t)

	_, err := newSettings(pool, nil, nil,
		[]SettingDef{{Key: "bad.item", Default: "x", Sealed: true, Public: true}})
	require.ErrorIs(t, err, ErrInvalidCatalog)

	_, err = newSettings(pool, nil, nil, []SettingDef{
		{Key: "dup.item", Default: "x"},
		{Key: "dup.item", Default: "y"},
	})
	require.ErrorIs(t, err, ErrInvalidCatalog, "a catalog that names a key twice is broken too")
}

// TestTheTypedGettersParseTheStoredValue covers the helper surface another
// feature reads through, including the loud failure on a value that does
// not parse.
func TestTheTypedGettersParseTheStoredValue(t *testing.T) {
	settings, _ := settingsService(t)

	require.NoError(t, settings.Update(t.Context(), "product.name", "42"))

	seats, err := settings.GetInt64(t.Context(), "product.name")
	require.NoError(t, err)
	assert.Equal(t, int64(42), seats)

	_, err = settings.GetBool(t.Context(), "product.name")
	assert.Error(t, err, "a value that does not parse must fail loudly")
}

// TestTheTableKeepsTheOverrideShapeOnly pins the schema the code scans: the
// table holds key, value, and the instants — the flags live in the catalog.
func TestTheTableKeepsTheOverrideShapeOnly(t *testing.T) {
	settings, pool := settingsService(t)

	require.NoError(t, settings.Update(t.Context(), "product.name", "Gringotts"))

	rows, err := pool.Query(t.Context(), `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'settings'
		ORDER BY ordinal_position`)
	require.NoError(t, err)
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var column string
		require.NoError(t, rows.Scan(&column))
		columns = append(columns, column)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"key", "value", "created_at", "updated_at"}, columns)
}

// isSealed answers whether a stored value rests sealed — the same test the
// reader makes, stated here for the assertions.
func isSealed(stored string) bool {
	return strings.HasPrefix(stored, crypto.EncPrefix)
}
