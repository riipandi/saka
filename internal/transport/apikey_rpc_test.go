package transport_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apikeyv1connect "github.com/riipandi/saka/codegen/proto/go/saka/apikey/v1/apikeyv1connect"
	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/framework/kernel"
	appauthz "github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/transport"
	"github.com/riipandi/saka/internal/transport/middleware"
	"github.com/riipandi/saka/modules/apikey"
	"github.com/riipandi/saka/modules/identity"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/pkg/jwtutils"
	"github.com/riipandi/saka/pkg/testutils"

	"uuid"
)

// The fixture accounts, named after the test-copywriting convention.
const (
	hermioneKeyOwner = "01a0da3e-1111-7000-8000-000000000001"
	ronKeyOwner      = "01a0da3e-1111-7000-8000-000000000002"
)

// machineAuthenticator answers the caller an API key produces: the owner's
// claims with the machine credential kind, which is what the guard's session
// rule reads.
func machineAuthenticator(subject string, admin bool) transport.Authenticator {
	return func(ctx context.Context, req *http.Request) (any, error) {
		claims := jwtutils.AccessClaims{Username: subject, Roles: adminRoles(admin)}
		return &jwtutils.Caller{UserID: subject, AccessClaims: claims,
			Credential: jwtutils.CredentialAPIKey}, nil
	}
}

// apiKeyPool opens a migrated database with one administrator and one plain
// account, so a key has owners with different roles.
func apiKeyPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := openTestMigrators(t, migrationDB)
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "transport_apikey_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(t.Context()) })

	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, first_name, last_name, display_name)
		VALUES
			($1, 'hermione', 'hermione@example.com', 'Hermione', 'Granger', 'Hermione Granger'),
			($2, 'ron',      'ron@example.com',      'Ron',      'Weasley',  'Ron Weasley')`,
		hermioneKeyOwner, ronKeyOwner)
	require.NoError(t, err)

	// Ron's key reaches the administrative view, which now reads the role
	// the account holds rather than a column: seed the grant the claims
	// will carry.
	grantAdministratorRole(t, pool, mustUUID(t, ronKeyOwner))
	return pool
}

// grantAdministratorRole seeds the system role row and grants it to the
// account, the way the authorization seeder would.
func grantAdministratorRole(t *testing.T, pool *datastore.Postgres, userID uuid.UUID) {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.roles (id, name, slug, type)
		VALUES ('01900000-0000-7000-8000-0000000000aa', 'Administrator', 'administrator', 'system')
		ON CONFLICT (slug) DO NOTHING`)
	require.NoError(t, err)

	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.user_roles (user_id, role_id)
		SELECT $1, r.id FROM public.roles r WHERE r.slug = 'administrator'
		ON CONFLICT DO NOTHING`, userID)
	require.NoError(t, err)
}

// newAPIKeyRouter mounts the real key feature over the transport's guard,
// with the caller the test asks for.
func newAPIKeyRouter(t *testing.T, auth transport.Authenticator, pool *datastore.Postgres) http.Handler {
	t.Helper()

	service := apikey.NewService(pool,
		fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	return transport.NewRouter(transport.Options{
		Config:        config.Default(),
		Checker:       health.NewChecker(),
		Authenticator: auth,
		Modules:       []kernel.Module{apikey.NewModule(service)},
	})
}

// TestTheAPIKeyGuardIsDeclared pins the surface's rule: the four management
// procedures are session-only, so a machine credential is refused with the
// not_found shape — the surface does not exist for the credential that
// cannot revoke itself — and the administrative view is a plain admin
// procedure a machine credential may serve, because its owner's role is its
// role.
func TestTheAPIKeyGuardIsDeclared(t *testing.T) {
	pool := apiKeyPool(t)

	for name, tc := range map[string]struct {
		procedure string
		body      string
		auth      transport.Authenticator
		status    int
		code      string
	}{
		"create without a credential": {
			procedure: apikeyv1connect.ApiKeyServiceCreateAPIKeyProcedure,
			body:      `{"name":"hogwarts-library","expires_at":"2030-01-01T00:00:00Z"}`,
			auth:      callerAuthenticator("", false, false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
		"create with a machine credential": {
			procedure: apikeyv1connect.ApiKeyServiceCreateAPIKeyProcedure,
			body:      `{"name":"hogwarts-library","expires_at":"2030-01-01T00:00:00Z"}`,
			auth:      machineAuthenticator(hermioneKeyOwner, true),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"list with a machine credential": {
			procedure: apikeyv1connect.ApiKeyServiceListAPIKeysProcedure,
			body:      `{}`,
			auth:      machineAuthenticator(hermioneKeyOwner, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"renew with a machine credential": {
			procedure: apikeyv1connect.ApiKeyServiceRenewAPIKeyProcedure,
			body:      `{"id":"01a0da3e-1111-7000-8000-000000000009","expires_at":"2030-01-01T00:00:00Z"}`,
			auth:      machineAuthenticator(hermioneKeyOwner, true),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"revoke with a machine credential": {
			procedure: apikeyv1connect.ApiKeyServiceRevokeAPIKeyProcedure,
			body:      `{"id":"01a0da3e-1111-7000-8000-000000000009"}`,
			auth:      machineAuthenticator(hermioneKeyOwner, true),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"list all without the role": {
			procedure: apikeyv1connect.ApiKeyServiceListAllAPIKeysProcedure,
			body:      `{}`,
			auth:      callerAuthenticator(hermioneKeyOwner, false, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"list all without a credential": {
			procedure: apikeyv1connect.ApiKeyServiceListAllAPIKeysProcedure,
			body:      `{}`,
			auth:      callerAuthenticator("", false, false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
	} {
		t.Run(name, func(t *testing.T) {
			router := newAPIKeyRouter(t, tc.auth, pool)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequest(t, tc.procedure, tc.body))

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.code)
		})
	}
}

// TestTheAPIKeyAuthenticatesThroughTheHeader runs the credential end to end:
// a key the service issued authenticates an X-API-Key request as its owner —
// an administrator's key administers — and the same header is refused on the
// surface that manages the keys, because the real authenticator answers a
// machine caller there.
func TestTheAPIKeyAuthenticatesThroughTheHeader(t *testing.T) {
	pool := apiKeyPool(t)

	cfg := config.Default()
	cfg.Auth.SecretKey = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"

	keyService := apikey.NewService(pool,
		fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	issued, err := keyService.Create(t.Context(), mustUUID(t, ronKeyOwner), apikey.CreateParams{
		Name:      "hogwarts-library",
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	})
	require.NoError(t, err)

	// The real authenticator, the composition root's shape: the bearer half
	// over the key set, the machine half over the key service, joined by the
	// middleware the transport owns.
	auth := middleware.APIKeyAuth(identity.Authenticate(jwks.NewService(cfg, nil, nil, nil), cfg), keyService)
	router := transport.NewRouter(transport.Options{
		Config:        cfg,
		Checker:       health.NewChecker(),
		Authenticator: auth,
		Modules:       []kernel.Module{apikey.NewModule(keyService)},
	})

	// The header authenticates the request as the owner: an administrator's
	// key reaches the administrative view, and the answer is the wire view
	// of the key that asked.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, apiKeyHeaderRequest(t, apikeyv1connect.ApiKeyServiceListAllAPIKeysProcedure, `{}`, issued.Raw))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var listed struct {
		APIKeys []struct {
			Name      string `json:"name"`
			Prefix    string `json:"prefix"`
			OwnerID   string `json:"owner_id"`
			ExpiresAt string `json:"expires_at"`
		} `json:"api_keys"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed.APIKeys, 1)
	assert.Equal(t, "hogwarts-library", listed.APIKeys[0].Name)
	assert.Equal(t, issued.Key.Prefix, listed.APIKeys[0].Prefix)
	assert.Equal(t, ronKeyOwner, listed.APIKeys[0].OwnerID)

	// The same header is refused on the surface that manages the keys: the
	// machine credential does not exist there.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, apiKeyHeaderRequest(t, apikeyv1connect.ApiKeyServiceCreateAPIKeyProcedure,
		`{"name":"another-key","expires_at":"2030-01-01T00:00:00Z"}`, issued.Raw))
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	// A key that is not a key answers the refusal a bad bearer does.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, apiKeyHeaderRequest(t, apikeyv1connect.ApiKeyServiceListAllAPIKeysProcedure, `{}`, "nobody.knows"))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
}

// mustUUID parses one of the fixture identifiers.
// adminRoles spells the claim an administrator's token carries, and the
// empty set a non-administrator's does not.
func adminRoles(admin bool) []string {
	if admin {
		return []string{appauthz.AdministratorRole}
	}
	return nil
}

func mustUUID(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(raw)
	require.NoError(t, err)
	return id
}

// apiKeyHeaderRequest builds an RPC request that presents an X-API-Key
// header, the way a machine client does.
func apiKeyHeaderRequest(t *testing.T, procedure, body, key string) *http.Request {
	t.Helper()

	req := rpcRequest(t, procedure, body)
	req.Header.Set("X-API-Key", key)
	return req
}
