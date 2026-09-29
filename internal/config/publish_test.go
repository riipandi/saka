package config

import (
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// planted is the secret value a test plants into the configuration. If a
// planted value reaches the published document, the redaction leaked.
const planted = "plant-hogwarts-secret-0123456789"

// plantedConfig builds a configuration with every secret set, so the full
// document has a redaction to carry for each.
func plantedConfig() Config {
	cfg := Default()
	cfg.App.SecretKey = planted
	cfg.Auth.SecretKey = planted
	cfg.Database.URL = "postgres://app:hunter2@db.example.com:5432/pocketid"
	cfg.KVStore.URL = "redis://:hunter2@cache.example.com:6379/2"
	cfg.Mailer.SMTPPassword = planted
	cfg.Storage.S3.AccessKey = planted
	cfg.Storage.S3.SecretKey = planted
	cfg.OTEL.Headers = map[string]string{"authorization": planted}
	return cfg
}

// TestThePublicScopeCarriesNoSecret pins the anonymous body: the public
// scope fills the public facts alone, so no secret field is even populated.
func TestThePublicScopeCarriesNoSecret(t *testing.T) {
	cfg := plantedConfig()

	document, err := json.Marshal(cfg.Published(false))
	require.NoError(t, err)

	assert.NotContains(t, string(document), planted)
	assert.NotContains(t, string(document), "redacted",
		"the public scope carries no secret key at all, redacted or not")
	assert.NotContains(t, string(document), "private_key")
}

// TestTheFullScopeRedactsEverySecret pins the administrator's body: a set
// secret is the placeholder the fail-safe print uses, an unset one stays
// empty and omitted, and the datastore URLs are the reduced targets.
func TestTheFullScopeRedactsEverySecret(t *testing.T) {
	cfg := plantedConfig()

	document, err := json.Marshal(cfg.Published(true))
	require.NoError(t, err)

	// Nothing planted may survive, and every secret slot the document
	// carries is the placeholder.
	assert.NotContains(t, string(document), planted)
	for _, key := range []string{"secret_key", "smtp_password", "secret_key"} {
		assert.Contains(t, string(document), `"`+key+`":"[redacted]"`, key)
	}

	// The datastore URLs are the reduced targets, not the credentials.
	var body map[string]any
	require.NoError(t, json.Unmarshal(document, &body))
	database := body["database"].(map[string]any)
	assert.Equal(t, "db.example.com:5432/pocketid", database["url"])
	kvstore := body["kvstore"].(map[string]any)
	assert.Equal(t, "cache.example.com:6379/2", kvstore["url"])

	headers := body["otel"].(map[string]any)["headers"].(map[string]any)
	assert.Equal(t, "[redacted]", headers["authorization"],
		"a header's name is kept and its value redacted")
}

// TestTheFullScopeLeavesAnUnsetSecretOmitted pins the distinction the
// endpoint owes its reader: a key that was never set is absent, so a reader
// can tell "not configured" from "configured and hidden".
func TestTheFullScopeLeavesAnUnsetSecretOmitted(t *testing.T) {
	cfg := Default()
	cfg.Mailer.SMTPPassword = "hunter2-but-real"

	document, err := json.Marshal(cfg.Published(true))
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(document, &body))
	mailer := body["mailer"].(map[string]any)
	assert.Equal(t, "[redacted]", mailer["smtp_password"])

	// app.secret_key and the auth key material were never set: they are not
	// in the body at all, not even as the placeholder.
	app := body["app"].(map[string]any)
	assert.NotContains(t, app, "secret_key")
	auth := body["auth"].(map[string]any)
	assert.NotContains(t, auth, "private_key")
}

// TestTheFullScopeCarriesTheRealNonSecretValues keeps the redaction from
// swallowing the document: the administrative values an operator reads are
// the resolved ones.
func TestTheFullScopeCarriesTheRealNonSecretValues(t *testing.T) {
	cfg := plantedConfig()
	cfg.App.Mode = ModeStaging
	cfg.Server.Port = 4080

	document, err := json.Marshal(cfg.Published(true))
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(document, &body))
	app := body["app"].(map[string]any)
	assert.Equal(t, ModeStaging, app["mode"])
	assert.Equal(t, float64(4080), body["server"].(map[string]any)["port"])
}

// TestEverySecretKeyPublishesItsRedaction is the drift guard: every key the
// one secretKeys list names must reach the full document only through the
// redaction path. A new secret published from the configuration directly —
// a field wired to the wrong copy — fails here, because its probe would
// still be in the body. The two datastore URLs are the exception on purpose:
// their reduced form carries the target and no credential, so the assertion
// for them is that the credentials are gone.
func TestEverySecretKeyPublishesItsRedaction(t *testing.T) {
	cfg := Default()
	fillStrings(reflect.ValueOf(&cfg).Elem(), probeSecret)

	document, err := json.Marshal(cfg.Published(true))
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(document, &body))

	for _, key := range secretKeys {
		value, ok := valueAtPath(body, key)
		require.True(t, ok, "%s must be in the full document", key)
		if key == "database.url" || key == "kvstore.url" {
			assert.NotContains(t, value, "user:pass", key)
			continue
		}
		// A map key, otel.headers, is a secret as a whole: every entry's
		// value is the placeholder, the names kept.
		if headers, mapKey := value.(map[string]any); mapKey {
			for name, entry := range headers {
				assert.Equal(t, redacted, entry, key+"."+name)
			}
			continue
		}
		assert.Equal(t, redacted, value, key)
	}
}

// valueAtPath reads the dotted path into the parsed document, the shape the
// endpoint answers with.
func valueAtPath(body map[string]any, path string) (any, bool) {
	var current any = body
	for segment := range strings.SplitSeq(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
