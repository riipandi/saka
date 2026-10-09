package router_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jetify.com/typeid"

	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/transport/router"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/modules/identity/session"
	"github.com/riipandi/saka/modules/identity/signin"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/modules/identity/webauthn"

	conttest "github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/jwtutils"
	"github.com/riipandi/saka/pkg/testutils"
)

// The fixture account, named after the test-copywriting convention.
const hermioneSessionOwner = "01a0da3e-1111-7000-8000-000000000001"

// sessionPool opens a migrated database with one account.
func sessionPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator := conttest.TestMigrator(t, migrationDB)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "transport_session_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(t.Context()) })

	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, first_name, last_name, display_name)
		VALUES ($1, 'hermione', 'hermione@example.com', 'Hermione', 'Granger', 'Hermione Granger')`,
		hermioneSessionOwner)
	require.NoError(t, err)
	return pool
}

// newSessionRouter mounts the real session feature over the transport's
// guard, with the caller the test asks for. The issuer behind the service is
// the real sign-in service, so a renewal signs through the same key source
// the opening does.
func newSessionRouter(t *testing.T, auth router.Authenticator, pool *datastore.Postgres) (http.Handler, *signin.Service) {
	t.Helper()

	cfg := config.Default()
	cfg.Auth.SecretKey = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"
	issuer := signin.NewService(cfg, pool, signin.NewRepository(pool),
		jwks.NewService(cfg, nil, nil, nil), fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	users := user.NewService(pool, nil, nil, nil)
	service := session.NewService(pool, issuer, users,
		fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))

	// The step-up proofs are spent by the webauthn feature's consumer, built
	// over the same pool; the settings reader is nil because the consumption
	// reads no setting.
	cfg.App.BaseURL = "http://localhost:3000"
	reauth, err := webauthn.NewService(cfg, pool, webauthn.NewRepository(), issuer, nil,
		fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), nil)
	require.NoError(t, err)

	return router.NewRouter(router.Options{
		Config:           cfg,
		Checker:          health.NewChecker(),
		Authenticator:    auth,
		Modules:          []kernel.Module{session.NewModule(service)},
		Reauthentication: reauth,
	}), issuer
}

// sessionCallerAuthenticator answers the caller a bearer token produces, with
// the session identifier the claims carry.
func sessionCallerAuthenticator(subject, sessionID string, admin bool) router.Authenticator {
	return func(ctx context.Context, req *http.Request) (any, error) {
		if subject == "" {
			return nil, connect.NewError(connect.CodeUnauthenticated, "authentication required")
		}
		claims := jwtutils.AccessClaims{Username: subject, Roles: adminRoles(admin), SessionID: sessionID}
		return &jwtutils.Caller{UserID: subject, AccessClaims: claims}, nil
	}
}

// TestTheSessionLifecycleEndsInAStamp runs the flow a client walks: sign in,
// read the session, renew the pair, list, and sign out — every step over the
// real procedure, the renewal rotating the row the opening wrote.
func TestTheSessionLifecycleEndsInAStamp(t *testing.T) {
	pool := sessionPool(t)

	cfg := config.Default()
	cfg.Auth.SecretKey = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"
	issuer := signin.NewService(cfg, pool, signin.NewRepository(pool),
		jwks.NewService(cfg, nil, nil, nil), fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), nil)
	account := signin.Account{ID: mustUUID(t, hermioneSessionOwner), Username: "hermione",
		Email: "hermione@example.com", DisplayName: "Hermione Granger"}
	result, err := issuer.IssueSession(t.Context(), pool, &account, signin.ProviderCredential,
		audit.EventSignIn, signin.SessionParams{})
	require.NoError(t, err)

	router, _ := newSessionRouter(t, sessionCallerAuthenticator(wireID(t, hermioneSessionOwner), result.SessionID, false), pool)

	// The session answers: the row the opening wrote, with the caller's own
	// marked.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceGetSessionProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got struct {
		Session struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"session"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, result.SessionID, got.Session.ID)
	assert.True(t, got.Session.Current)

	// The renewal rotates in place: the session identifier survives, the
	// refresh token does not, and the spent token answers the refusal an
	// unknown one does.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceRefreshProcedure,
		`{"refresh_token":"`+result.RefreshToken+`"}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var refreshed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		SessionID    string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refreshed))
	assert.Equal(t, result.SessionID, refreshed.SessionID)
	assert.NotEqual(t, result.RefreshToken, refreshed.RefreshToken)

	// The list answers the one session the account holds.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceListSessionsProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// The sign-out stamps the row. The access token keeps passing the guard
	// — the statelessness the protocol settles — but the session it names
	// answers the ended failure, and the refresh token dies with the stamp.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceSignOutProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceGetSessionProcedure, `{}`))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "the session has ended")

	// The account-scoped procedures honour the same gate: the list a
	// sign-out ends is not served to the credential that signed out.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceListSessionsProcedure, `{}`))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "the session has ended")

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceRefreshProcedure,
		`{"refresh_token":"`+refreshed.RefreshToken+`"}`))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())

	// The reuse walk needs a session the fake caller's identifier names, so
	// a second opening runs it: the renewal rotates, the replayed original
	// is refused, and the refusal comes with a revocation — the one
	// competent explanation of a replayed refresh token is a duplicated
	// credential, and the session it named ends with it.
	stamp, err := issuer.IssueSession(t.Context(), pool, &account, signin.ProviderCredential,
		audit.EventSignIn, signin.SessionParams{})
	require.NoError(t, err)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceRefreshProcedure,
		`{"refresh_token":"`+stamp.RefreshToken+`"}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceRefreshProcedure,
		`{"refresh_token":"`+stamp.RefreshToken+`"}`))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceListSessionsProcedure, `{}`))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "the session has ended")
}

// TestTheBulkSignOutsSweepTheAccountSessions runs the two sweeps a holder
// walks from a client they still trust: the other sessions die first, then
// the sweep that includes the caller's own row — every step over the real
// procedure, the counts answering what each sweep actually stamped.
func TestTheBulkSignOutsSweepTheAccountSessions(t *testing.T) {
	pool := sessionPool(t)

	// Three live rows: the caller's own — the guard answers the session the
	// claims name only while its row is live — and two extras the sweep has
	// something to close.
	insertSession := func(name string) string {
		t.Helper()
		_, err := pool.Exec(t.Context(), `
			INSERT INTO public.sessions (user_id, provider, token_hash, user_agent, remember, created_at, expires_at)
			VALUES ($1, 'credential', $2, 'test-agent/1.0', false, now() - interval '1 minute', now() + interval '24 hours')`,
			hermioneSessionOwner, crypto.HashRefreshToken("refresh-token-"+name))
		require.NoError(t, err)
		var rawID string
		require.NoError(t, pool.QueryRow(t.Context(),
			`SELECT id FROM public.sessions WHERE token_hash = $1`,
			crypto.HashRefreshToken("refresh-token-"+name)).Scan(&rawID))
		sid, err := typeid.FromUUID[session.SessionID](rawID)
		require.NoError(t, err)
		return sid.String()
	}
	current := insertSession("current")
	insertSession("extra-a")
	insertSession("extra-b")

	router, _ := newSessionRouter(t, sessionCallerAuthenticator(wireID(t, hermioneSessionOwner), current, false), pool)

	// The other-sessions sweep stamps the two extras and keeps the caller's
	// own row: the count is what the call ended.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceSignOutOtherSessionsProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var others struct {
		RevokedCount int    `json:"revoked_count"`
		Message      string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &others))
	assert.Equal(t, 2, others.RevokedCount)

	// The all-sessions sweep stamps the caller's own row, and a second call
	// finds nothing live: the account is fully swept, and the answer says
	// so without writing anything.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceSignOutAllSessionsProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var all struct {
		RevokedCount int `json:"revoked_count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &all))
	assert.Equal(t, 1, all.RevokedCount)

	// The caller's own row is dead now, and the session surface honours
	// that: the sweep answers the ended failure instead of running, because
	// a credential the surface no longer honours cannot manage sessions.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.SessionServiceSignOutAllSessionsProcedure, `{}`))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "the session has ended")
}

// TestARevocationNamingNoSessionAnswersNotFound pins the wire code a target
// the caller cannot name gets: a well-formed identifier that names no row —
// theirs or another account's — is the not-found refusal the guard spells,
// never the internal failure an unmapped service error would leak.
func TestARevocationNamingNoSessionAnswersNotFound(t *testing.T) {
	pool := sessionPool(t)

	insertSession := func(name string) string {
		t.Helper()
		_, err := pool.Exec(t.Context(), `
			INSERT INTO public.sessions (user_id, provider, token_hash, user_agent, remember, created_at, expires_at)
			VALUES ($1, 'credential', $2, 'test-agent/1.0', false, now() - interval '1 minute', now() + interval '24 hours')`,
			hermioneSessionOwner, crypto.HashRefreshToken("refresh-token-"+name))
		require.NoError(t, err)
		var rawID string
		require.NoError(t, pool.QueryRow(t.Context(),
			`SELECT id FROM public.sessions WHERE token_hash = $1`,
			crypto.HashRefreshToken("refresh-token-"+name)).Scan(&rawID))
		sid, err := typeid.FromUUID[session.SessionID](rawID)
		require.NoError(t, err)
		return sid.String()
	}
	current := insertSession("current")

	router, _ := newSessionRouter(t, sessionCallerAuthenticator(wireID(t, hermioneSessionOwner), current, false), pool)

	// Each guarded call spends its own proof: the token is minted fresh per
	// subtest, the way a client re-proves before every sensitive call.
	proof := func(t *testing.T) map[string]string {
		t.Helper()
		_, err := pool.Exec(t.Context(), `
			INSERT INTO public.auth_tokens (user_id, token_hash, purpose, created_at, expires_at)
			VALUES ($1, $2, 'reauthentication', now(), now() + interval '5 minutes')`,
			hermioneSessionOwner, crypto.HashHexToken("step-up-"+t.Name()))
		require.NoError(t, err)
		return map[string]string{"X-Saka-Reauthentication": "step-up-" + t.Name()}
	}

	for name, body := range map[string]string{
		"unknown identifier": `{"id":"sess_00000000000000000000000000"}`,
		"foreign identifier": `{"id":"sess_01a0da3e11117000800000000001"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequestWithHeaders(t, authnv1connect.SessionServiceRevokeSessionProcedure, body, proof(t)))
			assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			assert.NotContains(t, rec.Body.String(), "internal", rec.Body.String())
		})
	}
}

// TestTheSessionGuardIsDeclared pins the session procedures' rule: every one
// but Refresh is session-only, so a caller without a credential is refused as
// unauthenticated, a machine credential is refused with the not-found shape —
// the surface does not exist for a credential that has no session — and a
// bearer whose claims carry no session identifier is refused as
// unauthenticated too, because a token without one is not a session. Refresh
// is public at the guard and rides the body's token, which the lifecycle test
// exercises without a header.
func TestTheSessionGuardIsDeclared(t *testing.T) {
	pool := sessionPool(t)

	noSessionCaller := func(subject string) router.Authenticator {
		return callerAuthenticator(subject, false, false)
	}

	for name, tc := range map[string]struct {
		procedure string
		body      string
		auth      router.Authenticator
		status    int
		code      string
	}{
		"sign out without a credential": {
			procedure: authnv1connect.SessionServiceSignOutProcedure,
			body:      `{}`,
			auth:      callerAuthenticator("", false, false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
		"sign out with a machine credential": {
			procedure: authnv1connect.SessionServiceSignOutProcedure,
			body:      `{}`,
			auth:      machineAuthenticator(hermioneSessionOwner, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"list without a credential": {
			procedure: authnv1connect.SessionServiceListSessionsProcedure,
			body:      `{}`,
			auth:      callerAuthenticator("", false, false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
		"list with a machine credential": {
			procedure: authnv1connect.SessionServiceListSessionsProcedure,
			body:      `{}`,
			auth:      machineAuthenticator(hermioneSessionOwner, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"revoke with a machine credential": {
			procedure: authnv1connect.SessionServiceRevokeSessionProcedure,
			body:      `{"id":"sess_01a0da3e11117000800000000001"}`,
			auth:      machineAuthenticator(hermioneSessionOwner, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"get session without the session claim": {
			procedure: authnv1connect.SessionServiceGetSessionProcedure,
			body:      `{}`,
			auth:      noSessionCaller(hermioneSessionOwner),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
		"refresh with an expired access token": {
			// Refresh rides the public surfaces: the credential it spends is
			// the body's refresh token, so the guard lets a request through
			// that carries none — the service judges the token itself.
			procedure: authnv1connect.SessionServiceRefreshProcedure,
			body:      `{"refresh_token":"x"}`,
			auth:      callerAuthenticator("", false, false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
	} {
		t.Run(name, func(t *testing.T) {
			router, _ := newSessionRouter(t, tc.auth, pool)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequest(t, tc.procedure, tc.body))

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.code)
		})
	}
}
