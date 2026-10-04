package transport_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	auditlogv1connect "github.com/riipandi/saka/codegen/proto/go/saka/auditlog/v1/auditlogv1connect"
	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/transport"
	"github.com/riipandi/saka/modules/auditlog"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/testutils"
)

// The fixture accounts, named after the test-copywriting convention.
const (
	hermioneAccount = "01a0da1c-cb41-779d-bd02-99b3eb5da32a"
	langdonAccount  = "01a0da2e-1111-7aaa-8bbb-000000000001"
)

// auditPool opens a migrated database with the fixture accounts and one record
// for each, so a list has something to answer and the guard has a caller whose
// activity exists.
func auditPool(t *testing.T) *datastore.Postgres {
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
		ApplicationName: "transport_audit_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })

	for id, name := range map[string]string{
		hermioneAccount: "hermione",
		langdonAccount:  "langdon",
	} {
		_, err := pool.Exec(t.Context(), `
			INSERT INTO public.users (id, username, email, first_name, last_name, display_name)
			VALUES ($1, $2::citext, $2::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger')`,
			id, name)
		require.NoError(t, err)
	}

	recorder := fwaudit.NewRecorder(slog.New(slog.DiscardHandler))
	recorder.Record(t.Context(), pool, fwaudit.Entry{Event: audit.EventSignIn, UserID: hermioneAccount})
	recorder.Record(t.Context(), pool, fwaudit.Entry{Event: audit.EventSignIn, UserID: langdonAccount})
	return pool
}

// newAuditRouter mounts the audit-log area over the transport's guard, with
// the caller the test asks for.
func newAuditRouter(t *testing.T, auth transport.Authenticator, pool *datastore.Postgres) http.Handler {
	t.Helper()

	service := auditlog.NewService(pool, auditlog.NewRepository(), slog.New(slog.DiscardHandler))
	return transport.NewRouter(transport.Options{
		Config:        config.Default(),
		Checker:       health.NewChecker(),
		Authenticator: auth,
		Modules:       []kernel.Module{auditlog.NewModule(service, slog.New(slog.DiscardHandler))},
	})
}

// wireID renders an account's row identifier in the wire form the contracts
// carry, the shape the real issuer signs into the token's subject.
func wireID(t *testing.T, raw string) string {
	t.Helper()
	id, err := user.IDFromUUIDString(raw)
	require.NoError(t, err)
	return id.String()
}

// TestTheAuditListAnswersTheCallersOwnRecordsOnly is the procedure's contract
// end to end: the account comes from the caller's token, so the page holds
// that account's records and no other's.
func TestTheAuditListAnswersTheCallersOwnRecordsOnly(t *testing.T) {
	pool := auditPool(t)
	router := newAuditRouter(t, callerAuthenticator(wireID(t, hermioneAccount), false, false), pool)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, auditlogv1connect.AuditLogServiceListProcedure, `{}`))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `"event":"sign_in"`)
	assert.Contains(t, body, `"username":"hermione"`)
	assert.NotContains(t, body, `"username":"langdon"`,
		"another account's activity must not be in the caller's page")
	assert.Contains(t, body, `"status":"success"`)
}

// TestTheAuditListRefusesADelegatedCaller is the rule that makes
// `Authenticated` fit a self-service procedure with no target in the request:
// an account's own activity is not a surface an impersonation may read as if
// it were the account. The refusal is not_found, the shape that discloses
// least.
func TestTheAuditListRefusesADelegatedCaller(t *testing.T) {
	pool := auditPool(t)
	router := newAuditRouter(t, callerAuthenticator(wireID(t, hermioneAccount), false, true), pool)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, auditlogv1connect.AuditLogServiceListProcedure, `{}`))

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "not_found")
}

// TestTheAdministrativeAuditProceduresRefuseACallerWithoutTheRole covers the
// three administrative procedures: each is refused before any service runs,
// with the same answer, so a caller cannot tell an administrative procedure
// from an absent one.
func TestTheAdministrativeAuditProceduresRefuseACallerWithoutTheRole(t *testing.T) {
	pool := auditPool(t)
	router := newAuditRouter(t, callerAuthenticator(wireID(t, hermioneAccount), false, false), pool)

	for name, tc := range map[string]struct {
		procedure string
		body      string
	}{
		"list all":       {auditlogv1connect.AuditLogServiceListAllProcedure, `{}`},
		"list for user":  {auditlogv1connect.AuditLogServiceListForUserProcedure, `{"user_id":"` + langdonAccount + `"}`},
		"filter options": {auditlogv1connect.AuditLogServiceFilterOptionsProcedure, `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequest(t, tc.procedure, tc.body))

			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "not_found")
		})
	}
}

// TestTheAdministrativeAuditProceduresAnswerAnAdministrator covers the other
// half: with the role, the same three procedures reach the service and answer
// the records they were asked for.
func TestTheAdministrativeAuditProceduresAnswerAnAdministrator(t *testing.T) {
	pool := auditPool(t)
	router := newAuditRouter(t, callerAuthenticator(wireID(t, langdonAccount), true, false), pool)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, auditlogv1connect.AuditLogServiceListAllProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"username":"hermione"`,
		"the administrative list must reach another account's records")
	assert.Contains(t, rec.Body.String(), `"username":"langdon"`)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, auditlogv1connect.AuditLogServiceListForUserProcedure,
		`{"user_id":"`+wireID(t, hermioneAccount)+`"}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"username":"hermione"`)
	assert.NotContains(t, rec.Body.String(), `"username":"langdon"`)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, auditlogv1connect.AuditLogServiceFilterOptionsProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"events":["sign_in"]`)
	assert.Contains(t, rec.Body.String(), `"username":"hermione"`)
	assert.Contains(t, rec.Body.String(), `"username":"langdon"`)
}

// TestTheAuditListAnswersNoCallerWithUnauthenticated keeps the two refusals
// apart: a request with no credential is unauthenticated, because the answer
// is to present one, while a caller who may not run the procedure is
// not_found.
func TestTheAuditListAnswersNoCallerWithUnauthenticated(t *testing.T) {
	pool := auditPool(t)
	router := newAuditRouter(t, callerAuthenticator("", false, false), pool)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, auditlogv1connect.AuditLogServiceListProcedure, `{}`))

	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "unauthenticated")
}
