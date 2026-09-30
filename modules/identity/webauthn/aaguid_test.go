package webauthn

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aaguidFromCatalog answers one AAGUID the embedded manifest carries, so a
// test exercises the real catalog rather than a fixture.
func aaguidFromCatalog(t *testing.T) [16]byte {
	t.Helper()
	aaguidCatalogOnce.Do(loadAAGUIDCatalog)
	require.NotNil(t, aaguidCatalogData, "the embedded catalog parsed")
	for key := range aaguidCatalogData {
		raw, err := hex.DecodeString(strings.ReplaceAll(strings.ToLower(key), "-", ""))
		require.NoError(t, err, "the catalog's keys are dashed UUIDs")
		require.Len(t, raw, 16)
		var aaguid [16]byte
		copy(aaguid[:], raw)
		return aaguid
	}
	require.Fail(t, "the catalog carries no entries")
	return [16]byte{}
}

// TestEnrollmentNamesAnUnknownAuthenticatorFromTheCatalog pins the fallback
// chain: a holder-named credential keeps its name, an unnamed one with a
// catalog AAGUID takes the catalog's name with the Passkey suffix, and an
// unknown AAGUID falls back to the plain word.
func TestEnrollmentNamesAnUnknownAuthenticatorFromTheCatalog(t *testing.T) {
	catalogAAGUID := aaguidFromCatalog(t)

	assert.Contains(t, authenticatorName(catalogAAGUID[:]), " Passkey")
	assert.Equal(t, fallbackCredentialName, authenticatorName([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}))
	assert.Equal(t, fallbackCredentialName, authenticatorName(nil))
	assert.Equal(t, fallbackCredentialName, authenticatorName(make([]byte, 16)), "the zero AAGUID names nothing")
}
