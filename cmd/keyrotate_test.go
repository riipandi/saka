//go:build debug

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/testutils"
)

// rotatedSeeds holds one sealed value per surface, sealed under the old
// secrets by the test and re-read after the rotation, so every surface's
// walk is proven against real rows rather than empty tables.
type rotatedSeeds struct {
	webhookSecret  string
	oauthSecret    string
	scimToken      string
	totpSecret     string
	settingValue   string
	jwksPrivateDoc string
}

// seedRotationRows inserts one sealed row per surface, sealed under the
// env file's secrets. The catalog carries no Sealed definition today, so
// the app_settings row rides a probe key — the walk is catalog-agnostic
// by contract: the enc: prefix is what it trusts (the write path's
// ErrReservedPrefix guard keeps plaintext rows from ever carrying it).
func seedRotationRows(t *testing.T, pool *datastore.Postgres) rotatedSeeds {
	t.Helper()

	oldApp, err := crypto.NewCipherFromHex(testSecret)
	require.NoError(t, err)
	oldAuth, err := crypto.NewAuthCipher(testSecret)
	require.NoError(t, err)

	seed := rotatedSeeds{
		webhookSecret:  "the webhook signing secret",
		oauthSecret:    "the oauth client secret",
		scimToken:      "the scim bearer token",
		totpSecret:     "the totp enrollment secret",
		settingValue:   "the sealed setting value",
		jwksPrivateDoc: `{"kty":"RSA","n":"x","e":"AQAB"}`,
	}

	seal := func(c *crypto.Cipher, v string) string {
		sealed, sealErr := c.Encrypt(v)
		require.NoError(t, sealErr)
		return sealed
	}

	require.NoError(t, pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		for _, stmt := range []string{
			// webhook: one endpoint with a secret.
			`INSERT INTO public.webhook_endpoints (name, endpoint, method, secret_enc)
			 VALUES ('probe-endpoint', 'https://probe.example.com/hook', 'POST', ` + quote(seal(oldApp, seed.webhookSecret)) + `)`,
			// oauth: one custom connection, secret sealed.
			`INSERT INTO public.oauth_connections (kind, provider, display_name, client_id, client_secret, enabled)
			 VALUES ('custom', 'probe', 'Probe', 'probe-client', ` + quote(seal(oldApp, seed.oauthSecret)) + `, true)`,
			// scim: the token's FK needs a client row first.
			`INSERT INTO public.oidc_clients (id, name) VALUES ('probe-client', 'Probe')`,
			`INSERT INTO public.scim_service_providers (endpoint, token, oidc_client_id)
			 VALUES ('https://probe.example.com/scim', ` + quote(seal(oldApp, seed.scimToken)) + `, 'probe-client')`,
			// mfa: rides the account the initialize seeder created.
			`INSERT INTO public.mfa_totp (user_id, name, secret)
			 SELECT id, 'probe', ` + quote(seal(oldAuth, seed.totpSecret)) + ` FROM public.users LIMIT 1`,
			// appconfig: the probe key's value rides the enc: prefix.
			`INSERT INTO public.app_settings (key, value)
			 VALUES ('probe.sealed', ` + quote(seal(oldApp, seed.settingValue)) + `)`,
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	}))

	// jwks: initialize provisioned the signing pair — prove the walk
	// meets it by asserting the row is there, sealed under the old auth
	// cipher.
	row := pool.QueryRow(t.Context(), "SELECT private_key FROM public.jwks WHERE private_key IS NOT NULL LIMIT 1")
	var private []byte
	require.NoError(t, row.Scan(&private), "initialize provisioned a signing pair")
	opened, err := oldAuth.Decrypt(string(private))
	require.NoError(t, err)
	seed.jwksPrivateDoc = opened
	return seed
}

// quote wraps a value in SQL single quotes, doubling the embedded ones.
func quote(v string) string {
	out := "'"
	for _, r := range v {
		if r == '\'' {
			out += "''"
		} else {
			out += string(r)
		}
	}
	return out + "'"
}

// rotationConfig points the config layer at the file a rotation run
// reads: the database and both secrets as env directives, and the issuer
// Validate demands (a directive-less issuer is the loader's fill from
// app.base_url, which this test file does not name).
func rotationConfig(t *testing.T) {
	t.Helper()

	useConfig(t, `{
		"database": {"url": "env:DATABASE_URL"},
		"app": {"secret_key": "env:APP_SECRET_KEY"},
		"auth": {"secret_key": "env:AUTH_SECRET_KEY", "issuer": "http://localhost:3000"}
	}`)
}

// runRotateCmd runs key:rotate with args and answers its stdout.
func runRotateCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	root := testRoot(&out, stdin, keyRotateCmd)
	err := root.Run(context.Background(), append([]string{"saka", "key:rotate"}, args...))
	return out.String(), err
}

// readSealed reads one surface's sealed column for the probe row, so the
// assertions decrypt what the database actually holds.
func readSealed(t *testing.T, pool *datastore.Postgres, query string) []byte {
	t.Helper()
	var value []byte
	require.NoError(t, pool.QueryRow(t.Context(), query).Scan(&value))
	return value
}

func TestKeyRotateReSealsEverySurface(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)
	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	rotationConfig(t)
	_, err = runInitializeCmd(t, initializeCmd, "--env-file="+envFile)
	require.NoError(t, err)

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })

	seed := seedRotationRows(t, pool)

	// Dry run first: the counts and the key check, zero writes — the
	// rows still open under the old secrets afterwards.
	out, err := runRotateCmd(t, "", "--env-file="+envFile, "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "no writes were made")

	newAppKey, err := crypto.GenerateKeyHex()
	require.NoError(t, err)
	newAuthKey, err := crypto.GenerateKeyHex()
	require.NoError(t, err)

	// The rotation: explicit keys, the env file overwritten.
	out, err = runRotateCmd(t, "", "--env-file="+envFile, "--overwrite",
		"--new-key="+newAppKey, "--new-auth-key="+newAuthKey)
	require.NoError(t, err)
	assert.Contains(t, out, "re-sealed")

	newApp, err := crypto.NewCipherFromHex(newAppKey)
	require.NoError(t, err)
	newAuth, err := crypto.NewAuthCipher(newAuthKey)
	require.NoError(t, err)

	opens := func(c *crypto.Cipher, v []byte) string {
		opened, openErr := c.Decrypt(string(v))
		require.NoError(t, openErr)
		return opened
	}

	assert.Equal(t, seed.webhookSecret, opens(newApp,
		readSealed(t, pool, `SELECT secret_enc FROM public.webhook_endpoints WHERE name='probe-endpoint'`)))
	assert.Equal(t, seed.oauthSecret, opens(newApp,
		readSealed(t, pool, `SELECT client_secret FROM public.oauth_connections WHERE provider='probe'`)))
	assert.Equal(t, seed.scimToken, opens(newApp,
		readSealed(t, pool, `SELECT token FROM public.scim_service_providers WHERE endpoint='https://probe.example.com/scim'`)))
	assert.Equal(t, seed.settingValue, opens(newApp,
		readSealed(t, pool, `SELECT value FROM public.app_settings WHERE key='probe.sealed'`)))
	assert.Equal(t, seed.totpSecret, opens(newAuth,
		readSealed(t, pool, `SELECT secret FROM public.mfa_totp WHERE name='probe'`)))
	assert.Equal(t, seed.jwksPrivateDoc, opens(newAuth,
		readSealed(t, pool, `SELECT private_key FROM public.jwks WHERE key_id=(SELECT key_id FROM public.jwks WHERE private_key IS NOT NULL LIMIT 1)`)))

	// The jwks row's fingerprint names the new auth cipher, so the
	// service's own reads accept the re-sealed row.
	var sealFP string
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT seal_fp FROM public.jwks WHERE private_key IS NOT NULL LIMIT 1").Scan(&sealFP))
	assert.Equal(t, newAuth.Fingerprint(), sealFP)

	// The old secrets open nothing any more.
	oldApp, err := crypto.NewCipherFromHex(testSecret)
	require.NoError(t, err)
	oldAuth, err := crypto.NewAuthCipher(testSecret)
	require.NoError(t, err)
	_, err = oldApp.Decrypt(string(readSealed(t, pool, `SELECT secret_enc FROM public.webhook_endpoints WHERE name='probe-endpoint'`)))
	assert.Error(t, err)
	_, err = oldAuth.Decrypt(string(readSealed(t, pool, `SELECT secret FROM public.mfa_totp WHERE name='probe'`)))
	assert.Error(t, err)

	// The env file carries the new keys, the deployment's next boot
	// resolves them.
	file, err := os.ReadFile(envFile)
	require.NoError(t, err)
	assert.Contains(t, string(file), "APP_SECRET_KEY="+newAppKey)
	assert.Contains(t, string(file), "AUTH_SECRET_KEY="+newAuthKey)
}

func TestKeyRotateAbortsOnAWrongOldKeyLeavingZeroWrites(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)
	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	rotationConfig(t)
	_, err = runInitializeCmd(t, initializeCmd, "--env-file="+envFile)
	require.NoError(t, err)

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })

	seedRotationRows(t, pool)

	// Point the configuration at a wrong application secret: the walk
	// fails on the first row it cannot open, the transaction rolls back,
	// and every surface is exactly as it was.
	wrongEnv := filepath.Join(t.TempDir(), ".env.wrong")
	wrongKey, err := crypto.GenerateKeyHex()
	require.NoError(t, err)
	body := "DATABASE_URL=" + dsn + "\n" +
		"APP_SECRET_KEY=" + wrongKey + "\n" +
		"AUTH_SECRET_KEY=" + testSecret + "\n"
	require.NoError(t, os.WriteFile(wrongEnv, []byte(body), 0o600))

	_, err = runRotateCmd(t, "", "--env-file="+wrongEnv, "--new-key="+testSecret, "--new-auth-key="+testSecret)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook endpoint secrets",
		"the abort is the walk's refusal at the first row the wrong key cannot open, not a validation failure: %s", err)

	// The untouched proof: the totp row still opens under the original
	// auth secret (the abort happened on an application-cipher surface),
	// and the webhook row still opens under the original application
	// secret.
	oldApp, err := crypto.NewCipherFromHex(testSecret)
	require.NoError(t, err)
	oldAuth, err := crypto.NewAuthCipher(testSecret)
	require.NoError(t, err)
	_, err = oldApp.Decrypt(string(readSealed(t, pool, `SELECT secret_enc FROM public.webhook_endpoints WHERE name='probe-endpoint'`)))
	require.NoError(t, err, "the abort left the webhook row sealed under the original secret")
	_, err = oldAuth.Decrypt(string(readSealed(t, pool, `SELECT secret FROM public.mfa_totp WHERE name='probe'`)))
	require.NoError(t, err, "the abort left zero writes on the auth surfaces too")
}
