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
	return NewSettings(pool, cipher, recorder), pool
}

// blindSettings builds the feature the way a run without a secret key is
// built: the pool is there, the cipher is not.
func blindSettings(t *testing.T) (*Settings, *datastore.Postgres) {
	t.Helper()
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	recorder := audit.NewRecorder(slog.New(slog.DiscardHandler))
	return NewSettings(pool, nil, recorder), pool
}

// TestSetRoundTripsAPlainValue covers the base case: what Set writes, Get
// reads back, unchanged and in the clear.
func TestSetRoundTripsAPlainValue(t *testing.T) {
	settings, _ := settingsService(t)

	require.NoError(t, settings.Set(t.Context(), "product.name", "Hogwarts", false, false))

	value, err := settings.Get(t.Context(), "product.name")
	require.NoError(t, err)
	assert.Equal(t, "Hogwarts", value)
}

// TestASealedValueRestsEncryptedAndReadsOpened is the feature's reason to
// exist: the table never holds the plaintext of a sensitive row, and every
// reader — helper or RPC — still answers the value the writer meant.
func TestASealedValueRestsEncryptedAndReadsOpened(t *testing.T) {
	settings, pool := settingsService(t)

	require.NoError(t, settings.Set(t.Context(), "smtp.relay", "s3cret", true, false))

	var resting string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT value FROM public.settings WHERE key = $1`, "smtp.relay").Scan(&resting))
	assert.True(t, isSealed(resting),
		"a sensitive value must rest sealed, not in the clear")

	value, err := settings.Get(t.Context(), "smtp.relay")
	require.NoError(t, err)
	assert.Equal(t, "s3cret", value, "the reader must open what the writer sealed")
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

	_, err = settings.Get(t.Context(), "orphan.key")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingCipher)
}

// TestSetWithoutACipherRefusesTheSensitiveWrite: a run with no secret key
// serves plain settings and refuses the one write it cannot protect.
func TestSetWithoutACipherRefusesTheSensitiveWrite(t *testing.T) {
	settings, _ := blindSettings(t)

	err := settings.Set(t.Context(), "smtp.relay", "s3cret", true, false)
	require.ErrorIs(t, err, ErrSealUnavailable)

	_, getErr := settings.Get(t.Context(), "smtp.relay")
	assert.ErrorIs(t, getErr, ErrUnknownSetting, "a refused write must not leave a row")
}

// TestASealedSettingCannotBePublic: the unauthenticated read publishes
// every public row verbatim, so the pair is refused at the write and
// checked at the table.
func TestASealedSettingCannotBePublic(t *testing.T) {
	settings, _ := settingsService(t)

	err := settings.Set(t.Context(), "brand.secret", "s3cret", true, true)
	require.ErrorIs(t, err, ErrSealedNotPublic)
}

// TestAPlainWriteMayNotForgeTheSealedPrefix keeps the read unambiguous: the
// enc: prefix is how a value declares itself sealed, so a plain write that
// begins with it is refused rather than stored as an unreadable value.
func TestAPlainWriteMayNotForgeTheSealedPrefix(t *testing.T) {
	settings, _ := settingsService(t)

	err := settings.Set(t.Context(), "brand.note", "enc:not-really-sealed", false, false)
	require.ErrorIs(t, err, ErrReservedPrefix)
}

// TestSetUpsertsTheRow covers the replace: the same key written twice rests
// once, with the second call's value and flags.
func TestSetUpsertsTheRow(t *testing.T) {
	settings, pool := settingsService(t)

	require.NoError(t, settings.Set(t.Context(), "product.name", "Hogwarts", false, false))
	require.NoError(t, settings.Set(t.Context(), "product.name", "Gringotts", false, true))

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.settings WHERE key = $1`, "product.name").Scan(&count))
	assert.Equal(t, 1, count)

	setting, err := settings.GetSetting(t.Context(), "product.name")
	require.NoError(t, err)
	assert.Equal(t, "Gringotts", setting.Value)
	assert.True(t, setting.Public)
	require.NotNil(t, setting.UpdatedAt, "an update must stamp the row")
}

// TestDeleteRemovesTheRowAndReportsAnUnknownKey covers both outcomes of a
// delete: the row is gone, and a key that names nothing is a refusal.
func TestDeleteRemovesTheRowAndReportsAnUnknownKey(t *testing.T) {
	settings, _ := settingsService(t)

	require.NoError(t, settings.Set(t.Context(), "product.name", "Hogwarts", false, false))
	require.NoError(t, settings.Delete(t.Context(), "product.name"))

	_, err := settings.Get(t.Context(), "product.name")
	assert.ErrorIs(t, err, ErrUnknownSetting)

	err = settings.Delete(t.Context(), "product.name")
	assert.ErrorIs(t, err, ErrUnknownSetting, "deleting an absent key must be a refusal, not a silence")
}

// TestListPublishesOnlyPublicRowsToTheUnauthenticatedRead covers the split
// the surface is named for: List answers everything, ListPublic only the
// rows flagged public.
func TestListPublishesOnlyPublicRowsToTheUnauthenticatedRead(t *testing.T) {
	settings, _ := settingsService(t)

	require.NoError(t, settings.Set(t.Context(), "brand.name", "Hogwarts", false, true))
	require.NoError(t, settings.Set(t.Context(), "product.quota", "42", false, false))
	require.NoError(t, settings.Set(t.Context(), "smtp.relay", "s3cret", true, false))

	all, err := settings.List(t.Context())
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, []string{"brand.name", "product.quota", "smtp.relay"},
		[]string{all[0].Key, all[1].Key, all[2].Key}, "the list is ordered by key")
	assert.Equal(t, "s3cret", all[2].Value, "the administrator's list opens sealed rows")

	public, err := settings.ListPublic(t.Context())
	require.NoError(t, err)
	require.Len(t, public, 1)
	assert.Equal(t, "brand.name", public[0].Key)
	assert.Equal(t, "Hogwarts", public[0].Value)
}

// TestTheSettingChangeLeavesAnAuditRecordWithoutTheValue covers the audit
// contract: a change through the RPC surface records the key and the flags
// in the same transaction, and never the value.
func TestTheSettingChangeLeavesAnAuditRecordWithoutTheValue(t *testing.T) {
	settings, pool := settingsService(t)

	caller := seedUser(t, pool, "granger", "granger@example.com")
	_, err := settings.SetFor(t.Context(), caller.String(), "smtp.relay", "s3cret", true, false)
	require.NoError(t, err)
	require.NoError(t, settings.DeleteFor(t.Context(), caller.String(), "smtp.relay"))

	var records int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event IN ($1, $2)`,
		audit.EventSettingDeleted, audit.EventSettingUpdated).Scan(&records))
	assert.Equal(t, 2, records, "both halves of the ceremony leave their record")

	var payload string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT payload::text FROM public.audit_logs WHERE event = $1`,
		audit.EventSettingUpdated).Scan(&payload))
	assert.Contains(t, payload, "smtp.relay")
	assert.Contains(t, payload, `"sealed"`)
	assert.NotContains(t, payload, "s3cret", "the audit record must never carry the value")
}

// TestTheTypedGettersParseTheStoredValue covers the helper surface another
// feature reads through, including the loud failure on a value that does
// not parse.
func TestTheTypedGettersParseTheStoredValue(t *testing.T) {
	settings, _ := settingsService(t)

	require.NoError(t, settings.Set(t.Context(), "product.seats", "42", false, false))
	require.NoError(t, settings.Set(t.Context(), "product.trial", "true", false, false))

	seats, err := settings.GetInt64(t.Context(), "product.seats")
	require.NoError(t, err)
	assert.Equal(t, int64(42), seats)

	trial, err := settings.GetBool(t.Context(), "product.trial")
	require.NoError(t, err)
	assert.True(t, trial)

	name, err := settings.GetString(t.Context(), "product.name")
	assert.ErrorIs(t, err, ErrUnknownSetting, "an absent key is refused, not zeroed")
	assert.Empty(t, name)

	require.NoError(t, settings.Set(t.Context(), "product.seats", "forty-two", false, false))
	_, err = settings.GetInt64(t.Context(), "product.seats")
	assert.Error(t, err, "a value that does not parse must fail loudly")
}

// TestTheTableRefusesAPublicRowThatRestsSealed is the database's own half of
// the invariant: even a writer that skips the service cannot make the
// unauthenticated read carry a ciphertext.
func TestTheTableRefusesAPublicRowThatRestsSealed(t *testing.T) {
	settings, pool := settingsService(t)

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.settings (key, value, public)
		VALUES ('brand.forced', 'enc:whatever', TRUE)`)
	require.Error(t, err, "the check constraint must refuse the pair")
	_ = settings
}

// isSealed answers whether a stored value rests sealed — the same test the
// reader makes, stated here for the assertions.
func isSealed(stored string) bool {
	return strings.HasPrefix(stored, crypto.EncPrefix)
}
