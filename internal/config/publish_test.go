package config

import (
	"encoding/json/v2"
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
	cfg.Auth.PrivateKey = planted
	cfg.Auth.PublicKey = planted
	cfg.Auth.SecretKey = planted
	cfg.Database.URL = "postgres://app:hunter2@db.example.com:5432/pocketid"
	cfg.KVStore.URL = "redis://:hunter2@cache.example.com:6379/2"
	cfg.Mailer.SMTPPassword = planted
	cfg.Storage.S3.AccessKeyID = planted
	cfg.Storage.S3.AccessKeySecret = planted
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
	for _, key := range []string{"secret_key", "private_key", "public_key", "smtp_password", "access_key_secret"} {
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
