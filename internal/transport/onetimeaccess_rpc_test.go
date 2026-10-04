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

	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/framework/kernel"
	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/transport"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/modules/identity/onetimeaccess"
	"github.com/riipandi/saka/modules/identity/signin"
	"github.com/riipandi/saka/pkg/testutils"
)

// The fixture account, named after the test-copywriting convention.
const hermioneCodeAccount = "01a0da3e-1111-7000-8000-000000000001"

// oneTimeAccessPool opens a migrated database with one account, so the
// exchange has somewhere to land.
func oneTimeAccessPool(t *testing.T) *datastore.Postgres {
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
		ApplicationName: "transport_onetimeaccess_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })

	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, first_name, last_name, display_name)
		VALUES ($1, $2::citext, $2::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger')`,
		hermioneCodeAccount, "hermione")
	require.NoError(t, err)
	return pool
}

// newOneTimeAccessRouter mounts the real one-time access feature — its
// exchange signs in through the real sign-in issuer, so the procedure answers
// with a token pair — over the transport's guard, with the caller the test
// asks for.
func newOneTimeAccessRouter(t *testing.T, auth transport.Authenticator, pool *datastore.Postgres) http.Handler {
	t.Helper()

	cfg := config.Default()
	cfg.Auth.SecretKey = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"

	// The mailer reports configured on any host and dials nothing until a
	// message is submitted; the queue is built over the pool with its
	// processors registered and its workers never started, so an enqueue
	// stays pending for the assertions to read.
	cfg.Mailer.SMTPHost = "localhost"
	m, err := fwmailer.New(cfg.MailerOptions(), nil)
	require.NoError(t, err)
	templates, err := mailer.NewTemplates(mailer.SenderFrom(cfg))
	require.NoError(t, err)
	mail := fwmailer.NewService(m, templates)

	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		NumWorkers:   1,
		ReleaseAfter: 10 * time.Minute,
	})
	require.NoError(t, err)

	issuer := signin.NewService(cfg, pool, signin.NewRepository(pool),
		jwks.NewService(cfg, nil, nil, nil), fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), nil)
	service := onetimeaccess.NewService(cfg, pool, issuer,
		fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), mail, client, nil)

	return transport.NewRouter(transport.Options{
		Config:        cfg,
		Checker:       health.NewChecker(),
		Authenticator: auth,
		Modules:       []kernel.Module{onetimeaccess.NewModule(service)},
	})
}

// TestTheOneTimeAccessGuardIsDeclared pins the four procedures' rules end to
// end: the two administrative ones refuse a caller without a credential or
// without the role, and the two public ones admit a caller who holds neither,
// which is what the exchange needs — it is the sign-in itself.
func TestTheOneTimeAccessGuardIsDeclared(t *testing.T) {
	pool := oneTimeAccessPool(t)

	for name, tc := range map[string]struct {
		procedure string
		body      string
		auth      transport.Authenticator
		status    int
		code      string
	}{
		"create token without a credential": {
			procedure: authnv1connect.OneTimeAccessServiceCreateTokenProcedure,
			body:      `{"id":"` + wireID(t, hermioneCodeAccount) + `"}`,
			auth:      callerAuthenticator("", false, false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
		"create token without the role": {
			procedure: authnv1connect.OneTimeAccessServiceCreateTokenProcedure,
			body:      `{"id":"` + wireID(t, hermioneCodeAccount) + `"}`,
			auth:      callerAuthenticator(wireID(t, hermioneCodeAccount), false, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"admin email without a credential": {
			procedure: authnv1connect.OneTimeAccessServiceRequestEmailAsAdminProcedure,
			body:      `{"id":"` + wireID(t, hermioneCodeAccount) + `"}`,
			auth:      callerAuthenticator("", false, false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
		"admin email without the role": {
			procedure: authnv1connect.OneTimeAccessServiceRequestEmailAsAdminProcedure,
			body:      `{"id":"` + wireID(t, hermioneCodeAccount) + `"}`,
			auth:      callerAuthenticator(wireID(t, hermioneCodeAccount), false, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		// The public procedures admit a caller who holds nothing. The
		// refusals below come from the service behind the guard, not from
		// the guard: an unknown code is an authentication failure the
		// feature reports, and a switched-off email path is the
		// configuration's answer — either proves the request got past the
		// guard to reach one.
		"exchange admits an anonymous caller": {
			procedure: authnv1connect.OneTimeAccessServiceExchangeTokenProcedure,
			body:      `{"token":"abcdef"}`,
			auth:      callerAuthenticator("", false, false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
		"email ask admits an anonymous caller": {
			procedure: authnv1connect.OneTimeAccessServiceRequestEmailProcedure,
			body:      `{"email":"hermione@example.com"}`,
			auth:      callerAuthenticator("", false, false),
			status:    http.StatusForbidden,
			code:      "permission_denied",
		},
	} {
		t.Run(name, func(t *testing.T) {
			router := newOneTimeAccessRouter(t, tc.auth, pool)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequest(t, tc.procedure, tc.body))

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.code)
		})
	}
}

// TestTheOneTimeAccessLoopEndsInASession runs the flow an administrator and a
// holder walk: the admin issues a code, the holder exchanges it, and the
// answer is the token pair of the session the code opened.
func TestTheOneTimeAccessLoopEndsInASession(t *testing.T) {
	pool := oneTimeAccessPool(t)
	router := newOneTimeAccessRouter(t, callerAuthenticator(wireID(t, hermioneCodeAccount), true, false), pool)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.OneTimeAccessServiceCreateTokenProcedure,
		`{"id":"`+wireID(t, hermioneCodeAccount)+`"}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var created struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.Len(t, created.Token, 6, "the default window's code is the short form")

	// The holder's exchange is an anonymous call: no credential, only the
	// code.
	holder := newOneTimeAccessRouter(t, callerAuthenticator("", false, false), pool)
	rec = httptest.NewRecorder()
	holder.ServeHTTP(rec, rpcRequest(t, authnv1connect.OneTimeAccessServiceExchangeTokenProcedure,
		`{"token":"`+created.Token+`"}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var exchanged struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		SessionID    string `json:"session_id"`
		User         struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &exchanged))
	assert.Equal(t, "hermione", exchanged.User.Username)
	assert.NotEmpty(t, exchanged.AccessToken)
	assert.NotEmpty(t, exchanged.RefreshToken)
	assert.NotEmpty(t, exchanged.SessionID)

	// The code is spent: a second exchange with the same value answers the
	// refusal an unknown code does.
	rec = httptest.NewRecorder()
	holder.ServeHTTP(rec, rpcRequest(t, authnv1connect.OneTimeAccessServiceExchangeTokenProcedure,
		`{"token":"`+created.Token+`"}`))
	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
}
