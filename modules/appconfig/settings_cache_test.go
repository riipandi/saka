package appconfig

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/cache"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"
)

// defaultTestTTL is the lifetime the test cache's entries carry — the
// driver's own default, since the feature passes zero and means "the
// configured one".
const defaultTestTTL = 0

// cachedSettings builds the feature over an in-memory cache, the way the
// registry builds it when caching is on.
func cachedSettings(t *testing.T) (*Settings, cache.Cache, *datastore.Postgres) {
	t.Helper()
	return cachedSettingsWith(t, Catalog())
}

// cachedSettingsWith builds the feature over an explicit catalog and an
// in-memory cache, the way a test drives shapes the shipped one does not
// carry.
func cachedSettingsWith(t *testing.T, defs []SettingDef) (*Settings, cache.Cache, *datastore.Postgres) {
	t.Helper()
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	cipher := mustCipher(t)
	recorder := audit.NewRecorder(slog.New(slog.DiscardHandler))
	kvCache := cache.NewMemory(0, defaultTestTTL)
	settings, err := newSettings(pool, cipher, recorder, defs, kvCache)
	require.NoError(t, err)
	return settings, kvCache, pool
}

// publicValue answers what the public listing carries for one key.
func publicValue(settings []Setting, key string) string {
	for _, setting := range settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}

// TestAChangeDropsTheCachedPublicListing pins the revalidation the public
// surface depends on: a listing served from the cache carries the change
// the moment it commits, because Update drops the entry the write touches.
func TestAChangeDropsTheCachedPublicListing(t *testing.T) {
	settings, kvCache, _ := cachedSettingsWith(t, []SettingDef{
		{Key: "site.title", Default: "Tango", Public: true},
	})
	ctx := t.Context()

	_, err := settings.ListPublic(ctx)
	require.NoError(t, err)
	_, ok := kvCache.Get(ctx, nil, listPublicCacheKey)
	require.True(t, ok, "the first listing is cached")

	require.NoError(t, settings.Update(ctx, "site.title", "Blackwood"))
	_, ok = kvCache.Get(ctx, nil, listPublicCacheKey)
	assert.False(t, ok, "the update dropped the cached listing")

	listing, err := settings.ListPublic(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Blackwood", publicValue(listing, "site.title"),
		"the listing answers the value the change wrote")

	require.NoError(t, settings.Reset(ctx, "site.title"))
	listing, err = settings.ListPublic(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Tango", publicValue(listing, "site.title"),
		"the reset restored the catalog default, through a cache the reset also dropped")
}

// TestTheSameRevalidationHoldsForTheRPCWritePath pins UpdateFor and
// ResetFor — the procedures an administrator calls — dropping the entries
// the same way their helper twins do, after their transaction commits.
func TestTheSameRevalidationHoldsForTheRPCWritePath(t *testing.T) {
	settings, kvCache, _ := cachedSettingsWith(t, []SettingDef{
		{Key: "site.notice", Default: "", Public: true},
	})
	ctx := t.Context()

	_, err := settings.ListPublic(ctx)
	require.NoError(t, err)

	_, err = settings.UpdateFor(ctx, "018f0000-0000-7000-8000-000000000001", "site.notice", "Back in print")
	require.NoError(t, err)
	_, ok := kvCache.Get(ctx, nil, listPublicCacheKey)
	assert.False(t, ok, "UpdateFor dropped the cached listing")

	_, err = settings.ListPublic(ctx)
	require.NoError(t, err)

	_, err = settings.ResetFor(ctx, "018f0000-0000-7000-8000-000000000001", "site.notice")
	require.NoError(t, err)
	_, ok = kvCache.Get(ctx, nil, listPublicCacheKey)
	assert.False(t, ok, "ResetFor dropped the cached listing")
}

// TestAGateValueIsReadThroughOnce pins the feature-gate read: the value a
// gate asks for is answered fresh after the change that touched it, and it
// parses through the typed getters the same as an uncached read would.
func TestAGateValueIsReadThroughOnce(t *testing.T) {
	settings, kvCache, _ := cachedSettings(t)
	ctx := t.Context()

	// The shipped catalog carries the end-session switch, a bool gate.
	require.NoError(t, settings.Update(ctx, SettingOIDCEndSessionRevokesConsent, "true"))

	value, err := settings.GetBool(ctx, SettingOIDCEndSessionRevokesConsent)
	require.NoError(t, err)
	assert.True(t, value)
	_, ok := kvCache.Get(ctx, nil, gateCacheKeyPrefix+SettingOIDCEndSessionRevokesConsent)
	require.True(t, ok, "the gate's value is cached")

	// A change drops the gate's own entry, so the next read is the table's.
	require.NoError(t, settings.Update(ctx, SettingOIDCEndSessionRevokesConsent, "false"))
	value, err = settings.GetBool(ctx, SettingOIDCEndSessionRevokesConsent)
	require.NoError(t, err)
	assert.False(t, value, "the gate answers the change, not the cached value")
}

// TestASealedValueIsNeverCached pins the secret rule: a sealed item's
// opened value is a secret, so it crosses no cache — the entry the reads
// of an unsealed key store, a sealed key's read never does.
func TestASealedValueIsNeverCached(t *testing.T) {
	_, kvCache, pool := cachedSettings(t)
	ctx := t.Context()

	// A catalog of one sealed item, built the way the constructor accepts:
	// the shipped catalog publishes nothing sealed, but a deployment's
	// feature may declare one.
	sealed, err := newSettings(pool, mustCipher(t), audit.NewRecorder(slog.New(slog.DiscardHandler)),
		[]SettingDef{{Key: "secret.token", Default: "", Sealed: true}}, kvCache)
	require.NoError(t, err)

	require.NoError(t, sealed.Update(ctx, "secret.token", "s3cret"))

	value, err := sealed.Get(ctx, "secret.token")
	require.NoError(t, err)
	assert.Equal(t, "s3cret", value, "the sealed value opened on the way out")

	_, ok := kvCache.Get(ctx, nil, gateCacheKeyPrefix+"secret.token")
	assert.False(t, ok, "a secret's opened value crosses no cache")
}

// mustCipher builds the cipher the sealed test reads through.
func mustCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	cipher, err := crypto.NewCipherFromHex(testCipherKey)
	require.NoError(t, err)
	return cipher
}
