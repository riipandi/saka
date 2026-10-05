package webauthn

import (
	_ "embed"
	"encoding/json/v2"
	"sync"
)

// The authenticator catalog: the AAGUID manifest passkeydeveloper publishes,
// fetched by scripts/get-aaguid.sh and embedded the way Pocket ID embeds
// its own. The upstream payload carries its icons inline as base64 — the
// fetch script strips them, so the binary carries the names only.
//
//go:embed aaguid.json
var aaguidCatalog []byte

// aaguidCatalogOnce guards the lazy parse: the catalog is read on the first
// enrollment that needs a fallback name, not at construction, so a run that
// never enrolls pays nothing for it.
var (
	aaguidCatalogOnce sync.Once
	aaguidCatalogData map[string]aaguidEntry
)

// aaguidEntry is one catalog record: the authenticator's display name. The
// icons come back with the catalog's icon pipeline.
type aaguidEntry struct {
	Name string `json:"name"`
}

// loadAAGUIDCatalog parses the embedded manifest once. A parse failure is
// logged and remembered — the catalog answers empty from then on, and the
// enrollment keeps its plain fallback name.
func loadAAGUIDCatalog() {
	if err := json.Unmarshal(aaguidCatalog, &aaguidCatalogData); err != nil {
		aaguidCatalogData = nil
	}
}

// authenticatorName answers the display name an AAGUID names: the catalog's
// record with the " Passkey" suffix, the way upstream renders it; an
// unknown or absent AAGUID falls back to the plain word. The formatted UUID
// is the catalog's key shape — the column's own.
func authenticatorName(aaguid []byte) string {
	formatted := formatAAGUID(aaguid)
	if formatted == "" || formatted == "00000000-0000-0000-0000-000000000000" {
		return fallbackCredentialName
	}
	aaguidCatalogOnce.Do(loadAAGUIDCatalog)
	if aaguidCatalogData == nil {
		return fallbackCredentialName
	}
	if entry, ok := aaguidCatalogData[formatted]; ok && entry.Name != "" {
		return entry.Name + " Passkey"
	}
	return fallbackCredentialName
}

// fallbackCredentialName is the name an enrollment with no holder name and
// no catalog record answers with.
const fallbackCredentialName = "Passkey"
