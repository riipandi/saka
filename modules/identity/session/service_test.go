package session

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/jwtutils"
	conttest "github.com/riipandi/saka/pkg/testutils"

	"uuid"

	"go.jetify.com/typeid"
)

// migratedPool opens a database the migrations have built, so the session
// table exists.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "session_test")
}

// fakeIssuer is the signing half a test controls: the token is a constant, so
// a renewal's answer is assertable, and the windows are short enough that the
// clock a test moves puts a session past them.
type fakeIssuer struct{ lifetime time.Duration }

func (f *fakeIssuer) SignSessionToken(_ context.Context, subject string, _ jwtutils.AccessClaims, _ SessionID, _ time.Time) (string, error) {
	return "fake-token-for-" + subject, nil
}

func (f *fakeIssuer) SessionLifetime(_ context.Context, _ bool) time.Duration { return f.lifetime }

func (f *fakeIssuer) AccessTokenTTL() time.Duration { return 15 * time.Minute }

// testService builds the service with a recorder that writes for real, and
// answers the clock knob a test moves to put a session past its window
// without writing an expiry the database's own check refuses.
// wireOf renders an account's row identifier in the wire form the claims
// carry, the shape the service procedures take.
func wireOf(t *testing.T, raw uuid.UUID) string {
	t.Helper()
	return user.FormatID(raw)
}

func testService(t *testing.T, pool *datastore.Postgres) (*Service, *time.Time) {
	t.Helper()

	users := user.NewService(pool, nil, nil, nil)
	now := time.Now()
	service := NewService(pool, &fakeIssuer{lifetime: 14 * 24 * time.Hour}, users,
		fwaudit.NewRecorder(slog.New(slog.NewTextHandler(os.Stderr, nil))), slog.New(slog.DiscardHandler))
	service.now = func() time.Time { return now }
	return service, &now
}

// jump moves the test's clock and answers the instant it now reads.
func jump(t *testing.T, now *time.Time, d time.Duration) time.Time {
	t.Helper()
	*now = now.Add(d)
	return *now
}

// seedAccount inserts an account directly and answers its identifier. A
// session belongs to an account, so one must exist before the session does.
func seedAccount(t *testing.T, pool *datastore.Postgres, username string) uuid.UUID {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (username, email, first_name, last_name, display_name)
		VALUES ($1::citext, $1::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger')`,
		username)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TableUsers)
	sb.Where(sb.Equal("username", username))

	query, args := sb.Build()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// seedSession inserts a session row the way a sign-in would, under a known
// refresh token, and answers the row's identifier and the token. The id is
// the database's default, read back and typed the way every reader of the
// table types it.
func seedSession(t *testing.T, pool *datastore.Postgres, userID uuid.UUID, name, provider string, remember bool) (SessionID, string) {
	t.Helper()

	token := "refresh-token-" + name

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableSessions)
	ib.Cols("user_id", "provider", "token_hash", "user_agent", "remember", "created_at", "expires_at")
	ib.Values(userID, provider, crypto.HashRefreshToken(token), "test-agent/1.0", remember,
		time.Now().Add(-time.Minute), time.Now().Add(24*time.Hour))

	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TableSessions)
	sb.Where(sb.Equal("token_hash", crypto.HashRefreshToken(token)))

	query, args = sb.Build()
	var rawID string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&rawID))
	sid, err := typeid.FromUUID[SessionID](rawID)
	require.NoError(t, err)
	return sid, token
}

// seedAdmin inserts an administrator account and answers its identifier.
// Impersonation's protections need a target the rules refuse: an
// administrator is the one account another administrator may not wear.
func seedAdmin(t *testing.T, pool *datastore.Postgres, username string) uuid.UUID {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (username, email, display_name)
		VALUES ($1::citext, $1::text || '@example.com', 'Vittoria Vetra')`,
		username)
	require.NoError(t, err)
	seedAdministratorRole(t, pool, username)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TableUsers)
	sb.Where(sb.Equal("username", username))

	query, args := sb.Build()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// seedAdministratorRole grants the administrator role to the account the
// username names, creating the system role row first. The column is_admin is
// gone: the role is the elevation, so a test that seeds an administrator
// seeds the grant.
func seedAdministratorRole(t *testing.T, pool *datastore.Postgres, username string) {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.roles (id, name, slug, type)
		VALUES ('01900000-0000-7000-8000-0000000000aa', 'Administrator', 'administrator', 'system')
		ON CONFLICT (slug) DO NOTHING`)
	require.NoError(t, err)

	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.user_roles (user_id, role_id)
		SELECT u.id, r.id
		FROM public.users u JOIN public.roles r ON r.slug = 'administrator'
		WHERE u.username = $1
		ON CONFLICT DO NOTHING`, username)
	require.NoError(t, err)
}

// auditCount reads how many records the log holds of one event about one
// session.
func auditCount(t *testing.T, pool *datastore.Postgres, event, sessionID string) int {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TableAuditLogs)
	sb.Where(sb.Equal("event", event), sb.Equal("resource_id", sessionID))

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}

func TestSignOutStampsTheRowAndTheRefreshTokenDies(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	sid, token := seedSession(t, pool, userID, "one", "credential", false)

	outcome, err := service.SignOut(t.Context(), sid.String(), wireOf(t, userID))
	require.NoError(t, err)
	assert.False(t, outcome.Already)
	assert.False(t, outcome.Expired)

	// The row survives with its stamp: the view carries the instant and the
	// ender, and the refresh token the row held opens nothing from now on.
	row, err := service.repo.GetSession(t.Context(), pool, sid)
	require.NoError(t, err)
	require.NotNil(t, row.RevokedAt)
	require.NotNil(t, row.RevokedBy)
	assert.Equal(t, userID, *row.RevokedBy)
	_, err = service.repo.FindActiveByTokenHash(t.Context(), pool,
		crypto.HashRefreshToken(token), time.Now())
	assert.ErrorIs(t, err, datastore.ErrNoRows)

	// A second sign-out is the success it is: the caller's intent is the
	// state the session is in, and the answer says so — nothing was
	// written, not even the audit record the first sign-out earned.
	again, err := service.SignOut(t.Context(), sid.String(), wireOf(t, userID))
	require.NoError(t, err)
	assert.True(t, again.Already)
	assert.Equal(t, 1, auditCount(t, pool, audit.EventSignOut, sid.UUID()))
}

func TestAnEndedSessionCannotManageSessions(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, now := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	live, _ := seedSession(t, pool, userID, "live", "credential", false)
	dead, _ := seedSession(t, pool, userID, "dead", "credential", false)

	// The stamped row: the holder signed out from this client, and the
	// surface no longer honours the credential it left behind.
	_, err := service.SignOut(t.Context(), dead.String(), wireOf(t, userID))
	require.NoError(t, err)

	_, _, err = service.ListSessions(t.Context(), dead.String(), wireOf(t, userID), 1, 10)
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, err = service.SignOutOtherSessions(t.Context(), dead.String(), wireOf(t, userID))
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, err = service.SignOutAllSessions(t.Context(), dead.String(), wireOf(t, userID))
	assert.ErrorIs(t, err, ErrSessionEnded)
	assert.ErrorIs(t, service.RevokeSession(t.Context(), dead.String(), wireOf(t, userID), live.String()), ErrSessionEnded)

	// A live row still manages as before, until its own window closes.
	_, _, err = service.ListSessions(t.Context(), live.String(), wireOf(t, userID), 1, 10)
	require.NoError(t, err)

	// The window closing without a stamp is the same refusal: the row is
	// still unstamped, and no procedure of the surface answers for it.
	live2, _ := seedSession(t, pool, userID, "live-2", "credential", false)
	jump(t, now, 48*time.Hour)
	_, _, err = service.ListSessions(t.Context(), live2.String(), wireOf(t, userID), 1, 10)
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, err = service.SignOutOtherSessions(t.Context(), live2.String(), wireOf(t, userID))
	assert.ErrorIs(t, err, ErrSessionEnded)
}

// seedShortSession inserts a session whose window is nearly closed — the
// table's write-time expiry check only compares against the insert instant,
// so a minute of future is enough, and the clock jump after it makes the row
// expired without any further write to it.
func seedShortSession(t *testing.T, pool *datastore.Postgres, userID uuid.UUID, name string) SessionID {
	t.Helper()

	token := "refresh-token-" + name

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableSessions)
	ib.Cols("user_id", "provider", "token_hash", "user_agent", "remember", "created_at", "expires_at")
	ib.Values(userID, "credential", crypto.HashRefreshToken(token), "test-agent/1.0", false,
		time.Now().Add(-time.Minute), time.Now().Add(time.Minute))

	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TableSessions)
	sb.Where(sb.Equal("token_hash", crypto.HashRefreshToken(token)))

	query, args = sb.Build()
	var rawID string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&rawID))
	sid, err := typeid.FromUUID[SessionID](rawID)
	require.NoError(t, err)
	return sid
}

// TestSignOutOtherSweepLeavesExpiredUnstampedRowsAlone pins the sweep's
// liveness predicate: a session whose window closed — but whose stamp was
// never written, because nothing writes on expiry — is not live, and the
// sweep must leave it exactly as it is. Stamping it would re-evaluate the
// table's `expires_at > CURRENT_TIMESTAMP` check on the updated row and
// refuse the whole write: the bulk sign-outs of any account holding such a
// row would answer 500 (the release probe caught exactly that).
func TestSignOutOtherSweepLeavesExpiredUnstampedRowsAlone(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, now := testService(t, pool)
	userID := seedAccount(t, pool, "norbert")
	current, _ := seedSession(t, pool, userID, "current", "credential", false)
	fresh, _ := seedSession(t, pool, userID, "fresh", "credential", false)
	stale := seedShortSession(t, pool, userID, "stale")

	// The stale row's window closes under the service's clock; the fresh and
	// current rows keep hours.
	jump(t, now, 2*time.Minute)

	count, err := service.SignOutOtherSessions(t.Context(), current.String(), wireOf(t, userID))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	_, _, err = service.GetSession(t.Context(), current.String())
	require.NoError(t, err)
	_, _, err = service.GetSession(t.Context(), fresh.String())
	assert.ErrorIs(t, err, ErrSessionEnded)

	// The expired row keeps its NULL stamp: the sweep neither counted it nor
	// touched it.
	var stamped int
	require.NoError(t, pool.QueryRow(t.Context(),
		"select count(*) from sessions where id = $1 and revoked_at is not null", stale.UUID()).Scan(&stamped))
	assert.Equal(t, 0, stamped)
}

func TestSignOutOtherSessionsSweepsEveryLiveRowButTheCallerOwn(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	current, currentToken := seedSession(t, pool, userID, "current", "credential", false)
	otherA, _ := seedSession(t, pool, userID, "other-a", "credential", true)
	otherB, _ := seedSession(t, pool, userID, "other-b", "one_time_access", false)
	stranger := seedAccount(t, pool, "vittoria")
	strangerSID, _ := seedSession(t, pool, stranger, "stranger", "credential", false)

	// A row that was stamped before the sweep is outside it: the sweep ends
	// live rows, and an ended one is not its business.
	ended, _ := seedSession(t, pool, userID, "ended", "credential", false)
	_, signOutErr := service.SignOut(t.Context(), ended.String(), wireOf(t, userID))
	require.NoError(t, signOutErr)

	count, err := service.SignOutOtherSessions(t.Context(), current.String(), wireOf(t, userID))
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// The caller's own row stays live, the swept ones do not, and the
	// account the sweep never names is untouched.
	_, _, err = service.GetSession(t.Context(), current.String())
	require.NoError(t, err)
	_, _, err = service.GetSession(t.Context(), otherA.String())
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, _, err = service.GetSession(t.Context(), otherB.String())
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, _, err = service.GetSession(t.Context(), strangerSID.String())
	require.NoError(t, err)

	// Each swept row carries its own record, named with the reason the
	// sweep answered.
	assert.Equal(t, 1, auditCount(t, pool, audit.EventSessionRevoked, otherA.UUID()))
	assert.Equal(t, 1, auditCount(t, pool, audit.EventSessionRevoked, otherB.UUID()))
	assert.Equal(t, 0, auditCount(t, pool, audit.EventSessionRevoked, current.UUID()))

	// A second sweep finds nothing: the rows it ended are stamped, and the
	// success is the state the account is already in.
	count, err = service.SignOutOtherSessions(t.Context(), current.String(), wireOf(t, userID))
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// The swept refresh tokens open nothing; the kept one still does.
	_, err = service.Refresh(t.Context(), "refresh-token-other-a")
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, err = service.Refresh(t.Context(), currentToken)
	require.NoError(t, err)
}

func TestSignOutAllSessionsEndsTheCallerOwnRowToo(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	userID := seedAccount(t, pool, "vittoria")
	current, currentToken := seedSession(t, pool, userID, "current", "credential", false)
	other, _ := seedSession(t, pool, userID, "other", "credential", true)

	count, err := service.SignOutAllSessions(t.Context(), current.String(), wireOf(t, userID))
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// The caller's own row is stamped like the rest: the session it named
	// answers the ended failure from here on, and its refresh token opens
	// nothing.
	_, _, err = service.GetSession(t.Context(), current.String())
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, _, err = service.GetSession(t.Context(), other.String())
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, err = service.Refresh(t.Context(), currentToken)
	assert.ErrorIs(t, err, ErrSessionEnded)

	// Each stamped row carries its own record under the all scope's reason.
	assert.Equal(t, 1, auditCount(t, pool, audit.EventSessionRevoked, current.UUID()))
	assert.Equal(t, 1, auditCount(t, pool, audit.EventSessionRevoked, other.UUID()))
}

func TestSignOutOfAnExpiredSessionStampsAndSaysSo(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, now := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	sid, _ := seedSession(t, pool, userID, "one", "credential", false)

	// The window closed a day ago; the row was never stamped, because the
	// expiry refusal is the write that rolls back. The sign-out closes the
	// book an expiry left open, and the answer says which one happened.
	jump(t, now, 48*time.Hour)
	outcome, err := service.SignOut(t.Context(), sid.String(), wireOf(t, userID))
	require.NoError(t, err)
	assert.True(t, outcome.Expired)
	assert.False(t, outcome.Already)

	// The stamp is the write it always was, and the record says the session
	// ended under its own event.
	row, err := service.repo.GetSession(t.Context(), pool, sid)
	require.NoError(t, err)
	require.NotNil(t, row.RevokedAt)
	assert.Equal(t, 1, auditCount(t, pool, audit.EventSignOut, sid.UUID()))
}

func TestGetSessionAnswersTheLiveRowAndRefusesAnEndedOne(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, now := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	sid, _ := seedSession(t, pool, userID, "one", "credential", true)

	row, view, err := service.GetSession(t.Context(), sid.String())
	require.NoError(t, err)
	assert.Equal(t, sid.String(), row.ID.String())
	assert.True(t, row.Remember)
	assert.Equal(t, "hermione", view.Username)

	// A session past its window is the ended failure: the token verified,
	// the session behind it did not survive.
	jump(t, now, 15*24*time.Hour)
	_, _, err = service.GetSession(t.Context(), sid.String())
	assert.ErrorIs(t, err, ErrSessionEnded)
}

func TestRevokeSessionEndsOneOfTheAccountsAndRefusesAnOthers(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	second := seedAccount(t, pool, "ron")
	current, _ := seedSession(t, pool, userID, "current", "credential", false)
	other, _ := seedSession(t, pool, userID, "other", "credential", true)
	theirs, _ := seedSession(t, pool, second, "theirs", "credential", false)

	require.NoError(t, service.RevokeSession(t.Context(), current.String(), wireOf(t, userID), other.String()))
	row, err := service.repo.GetSession(t.Context(), pool, other)
	require.NoError(t, err)
	require.NotNil(t, row.RevokedAt)
	assert.Equal(t, 1, auditCount(t, pool, audit.EventSessionRevoked, other.UUID()))

	// A session another account holds is the not-found failure, the same
	// as an unknown identifier: the owner's list is the only way to learn
	// which sessions exist.
	assert.ErrorIs(t, service.RevokeSession(t.Context(), current.String(), wireOf(t, userID), theirs.String()), ErrSessionNotFound)
	assert.ErrorIs(t, service.RevokeSession(t.Context(), current.String(), wireOf(t, userID), "sess_000000000000000000000000a"), ErrSessionNotFound)

	// A caller whose own session has ended cannot manage sessions at all:
	// the gate is the caller's own row, not the target's.
	assert.ErrorIs(t, service.RevokeSession(t.Context(), other.String(), wireOf(t, userID), theirs.String()), ErrSessionEnded)

	// Ending the current session is what SignOut does; the event names the
	// happening so the log can tell the two apart.
	third, _ := seedSession(t, pool, userID, "third", "credential", false)
	require.NoError(t, service.RevokeSession(t.Context(), third.String(), wireOf(t, userID), current.String()))
	assert.Equal(t, 1, auditCount(t, pool, audit.EventSessionRevoked, current.UUID()))
}

func TestListSessionsAnswersTheAccountsOwnNewestFirst(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	second := seedAccount(t, pool, "ron")
	first, _ := seedSession(t, pool, userID, "first", "credential", false)
	time.Sleep(10 * time.Millisecond)
	secondOfFirst, _ := seedSession(t, pool, userID, "second", "one_time_access", true)
	seedSession(t, pool, second, "theirs", "credential", false)

	rows, pagination, err := service.ListSessions(t.Context(), first.String(), wireOf(t, userID), 1, 10)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, 2, *pagination.TotalItems)
	assert.Equal(t, secondOfFirst.String(), rows[0].ID.String(),
		"the newest session answers first")
	assert.Equal(t, first.String(), rows[1].ID.String())
}

func TestRefreshRotatesTheTokenAndKeepsTheSession(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	sid, token := seedSession(t, pool, userID, "one", "credential", false)

	refreshed, err := service.Refresh(t.Context(), token)
	require.NoError(t, err)

	// The session keeps its identity: the id is the row's, the answer names
	// the account, and the row now carries the renewal's secret and stamp.
	assert.Equal(t, sid.String(), refreshed.SessionID)
	assert.Equal(t, "fake-token-for-"+wireOf(t, userID), refreshed.AccessToken)
	assert.Equal(t, "hermione", refreshed.User.Username)
	row, err := service.repo.GetSession(t.Context(), pool, sid)
	require.NoError(t, err)
	require.NotNil(t, row.RefreshedAt)

	// The new credential works; the spent one opens nothing.
	_, err = service.repo.FindActiveByTokenHash(t.Context(), pool,
		crypto.HashRefreshToken(refreshed.RefreshToken), time.Now())
	assert.NoError(t, err)
	_, err = service.repo.FindActiveByTokenHash(t.Context(), pool,
		crypto.HashRefreshToken(token), time.Now())
	assert.ErrorIs(t, err, datastore.ErrNoRows)

	// A revoked session answers the same failure an unknown token does:
	// the rotation's write carries the gate, so a session ended between the
	// read and the write costs the new secret and nothing else.
	_, err = service.SignOut(t.Context(), sid.String(), wireOf(t, userID))
	require.NoError(t, err)
	_, err = service.Refresh(t.Context(), refreshed.RefreshToken)
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, err = service.Refresh(t.Context(), "not-a-token")
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, err = service.Refresh(t.Context(), "")
	assert.ErrorIs(t, err, ErrSessionEnded)
}

// The delegation's window is a hard bound: a renewal rotates the secret but
// hands the row no lifetime beyond the hour the impersonation opened, and
// once the window has passed the delegation renews no further.
// The inactivity gate: a session whose last activity rests older than
// `session.inactivity_timeout` is refused and revoked on the spot — the row
// is stamped, so the stolen credential the caller holds cannot wait out the
// gate and replay later. A session inside the window renews as before.
func TestAnIdleSessionIsRefusedAndRotatedOut(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, now := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	sid, token := seedSession(t, pool, userID, "one", "credential", false)
	service.WithSettings(stubBound{seconds: int64(time.Hour.Seconds())})

	// Inside the window the renewal answers.
	jump(t, now, 30*time.Minute)
	_, err := service.Refresh(t.Context(), token)
	require.NoError(t, err)

	// Past the window — the last renewal is the jump above, so the clock
	// moves two hours past it — the same token is refused, and the row is
	// revoked rather than left resting.
	jump(t, now, 2*time.Hour)
	_, err = service.Refresh(t.Context(), token)
	assert.ErrorIs(t, err, ErrSessionEnded)
	row, err := service.repo.GetSession(t.Context(), pool, sid)
	require.NoError(t, err)
	assert.NotNil(t, row.RevokedAt, "an idle session is rotated out, not left resting")
}

// stubBound answers the duration keys with one value: the renewal's view of
// the catalog in a test.
type stubBound struct {
	seconds int64
}

func (s stubBound) GetInt64(context.Context, string) (int64, error) {
	return s.seconds, nil
}

func TestRefreshCapsADelegatedSessionAtTheImpersonationWindow(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	admin := seedAccount(t, pool, "robert_langdon")
	target := seedAccount(t, pool, "sophie_neveu")

	out, err := service.ImpersonateUser(t.Context(), wireOf(t, admin), "robert_langdon",
		wireOf(t, target), "reproducing the vault's missing entry")
	require.NoError(t, err)

	refreshed, err := service.Refresh(t.Context(), out.RefreshToken)
	require.NoError(t, err)

	// The renewal still answers, but the window it reports is the remainder
	// of the delegation's hour, not a fresh short lifetime.
	assert.Less(t, refreshed.RefreshExpiresIn, int32((12 * time.Hour).Seconds()),
		"the reported window is the delegation's remainder, not a fresh short lifetime")
	assert.Greater(t, refreshed.RefreshExpiresIn, int32((55 * time.Minute).Seconds()))

	row, err := service.repo.GetSession(t.Context(), pool, mustSessionID(t, out.SessionID))
	require.NoError(t, err)
	assert.True(t, row.ExpiresAt.Before(time.Now().Add(ImpersonationTTL).Add(time.Minute)),
		"the rotated row's expiry stays inside the impersonation window")

	// The delegation survives its renewal — the actor pair is re-signed.
	assert.NotEmpty(t, refreshed.AccessToken)
}

// A delegated row whose administrator cannot be named is not a renewal: the
// claims are re-signed from the row's bookkeeping, so a row that lost its
// actor would mint a token for the target with nobody behind it — the
// delegation would read as the target's own session. The refusal comes before
// the rotation, so the refresh token survives the attempt.
func TestRefreshRefusesADelegationThatCannotNameItsActor(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	admin := seedAccount(t, pool, "robert_langdon")
	target := seedAccount(t, pool, "sophie_neveu")

	out, err := service.ImpersonateUser(t.Context(), wireOf(t, admin), "robert_langdon",
		wireOf(t, target), "reproducing the vault's missing entry")
	require.NoError(t, err)

	// The administrator is deleted while the delegation lives. The column's
	// ON DELETE SET NULL leaves the row a delegation by provider and by
	// nothing else.
	_, err = pool.Exec(t.Context(), `DELETE FROM public.users WHERE id = $1`, admin)
	require.NoError(t, err)

	row, err := service.repo.GetSession(t.Context(), pool, mustSessionID(t, out.SessionID))
	require.NoError(t, err)
	require.Equal(t, ImpersonationProvider, row.Provider)
	require.Nil(t, row.ImpersonatedBy, "the deleted administrator leaves the column NULL")

	_, err = service.Refresh(t.Context(), out.RefreshToken)
	assert.ErrorIs(t, err, ErrSessionEnded,
		"a delegation that cannot name its actor must not sign a token for the target")

	// The refusal landed before the rotation: the token was not spent, so the
	// same credential is refused again rather than silently replaced.
	_, err = service.Refresh(t.Context(), out.RefreshToken)
	assert.ErrorIs(t, err, ErrSessionEnded)

	_, err = service.repo.FindActiveByTokenHash(t.Context(), pool,
		crypto.HashRefreshToken(out.RefreshToken), time.Now())
	assert.NoError(t, err, "the refused renewal did not burn the refresh token")
}

// Presenting a refresh token a rotation already replaced is the one
// competent explanation of a duplicated credential: the renewal refuses the
// caller and revokes the session the spent token names, with the audit
// record written in the same transaction.
// Two in-flight renewals of one token serialize on the row lock: the second
// finds a `token_hash` that is no longer the one it presented, which is the
// reuse the session dies for. Only one renewal answers a pair, and the session
// the token named is revoked as compromised.
func TestConcurrentRefreshOfOneTokenSerializesAndRevokesTheReuse(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	sid, token := seedSession(t, pool, userID, "one", "credential", false)

	const callers = 4
	results := make([]Refreshed, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = service.Refresh(context.Background(), token)
		}()
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		assert.ErrorIs(t, err, ErrSessionEnded, "every loser is refused, not handed a second pair")
	}
	assert.Equal(t, 1, succeeded, "exactly one renewal wins the token")

	// The reuse is not a silent refusal: the session is revoked outright, and
	// the winning pair dies with it — the whole credential is retired, not
	// just the token that leaked.
	row, err := service.repo.GetSession(t.Context(), pool, sid)
	require.NoError(t, err)
	require.NotNil(t, row.RevokedAt)
	for _, r := range results {
		if r.RefreshToken == "" {
			continue
		}
		_, refreshErr := service.Refresh(t.Context(), r.RefreshToken)
		assert.ErrorIs(t, refreshErr, ErrSessionEnded)
	}
	assert.Greater(t, auditCount(t, pool, audit.EventSessionRevoked, sid.UUID()), 0,
		"the overlapping reuse is an audit record, not a silent refusal")
}

// Presenting a refresh token a rotation already replaced is the one
// competent explanation of a duplicated credential: the renewal refuses the
// caller and revokes the session the spent token names, with the audit
// record written in the same transaction.
func TestRefreshRefusesAReplayedTokenAndRevokesTheSession(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)
	userID := seedAccount(t, pool, "hermione")
	sid, token := seedSession(t, pool, userID, "one", "credential", false)

	first, err := service.Refresh(t.Context(), token)
	require.NoError(t, err)

	mid, err := service.repo.GetSession(t.Context(), pool, sid)
	require.NoError(t, err)
	require.Equal(t, crypto.HashRefreshToken(token), mid.RotatedTokenHash,
		"the rotation keeps the hash it replaced beside the row")

	_, err = service.Refresh(t.Context(), token)
	assert.ErrorIs(t, err, ErrSessionEnded)

	// The session the replay named is dead, not merely unrotated.
	row, err := service.repo.GetSession(t.Context(), pool, sid)
	require.NoError(t, err)
	require.NotNil(t, row.RevokedAt)

	// And the replacement the first renewal minted is dead with it: the
	// whole credential is retired, not just the half that leaked.
	_, err = service.Refresh(t.Context(), first.RefreshToken)
	assert.ErrorIs(t, err, ErrSessionEnded)
	_, err = service.repo.FindActiveByTokenHash(t.Context(), pool,
		crypto.HashRefreshToken(first.RefreshToken), time.Now())
	assert.ErrorIs(t, err, datastore.ErrNoRows)

	assert.Greater(t, auditCount(t, pool, audit.EventSessionRevoked, sid.UUID()), 0,
		"the reuse is an audit record, not a silent refusal")
}

func TestImpersonateUserOpensADelegatedSession(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)

	admin := seedAccount(t, pool, "robert_langdon")
	target := seedAccount(t, pool, "sophie_neveu")

	out, err := service.ImpersonateUser(t.Context(), wireOf(t, admin), "robert_langdon",
		wireOf(t, target), "reproducing the vault's missing entry")
	require.NoError(t, err)

	assert.Equal(t, "fake-token-for-"+wireOf(t, target), out.AccessToken,
		"the delegated token's subject is the target")
	assert.Equal(t, int32(ImpersonationTTL.Seconds()), out.RefreshExpiresIn,
		"the window is the delegation's fixed one, not the account's")

	row, err := service.repo.GetSession(t.Context(), pool, mustSessionID(t, out.SessionID))
	require.NoError(t, err)
	require.NotNil(t, row.ImpersonatedBy)
	assert.Equal(t, admin, *row.ImpersonatedBy, "the row names the administrator behind it")
	assert.Equal(t, ImpersonationProvider, row.Provider)

	assert.Equal(t, 1, auditCount(t, pool, audit.EventImpersonationStarted, mustSessionID(t, out.SessionID).UUID()))
}

func TestImpersonateUserRefusesAdminsAndItselfAndTheUnknown(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)

	admin := seedAccount(t, pool, "robert_langdon")
	fellowAdmin := seedAdmin(t, pool, "vittoria_vetra")

	_, err := service.ImpersonateUser(t.Context(), wireOf(t, admin), "robert_langdon",
		wireOf(t, fellowAdmin), "checking the ledger")
	assert.ErrorIs(t, err, ErrTargetAdmin)

	_, err = service.ImpersonateUser(t.Context(), wireOf(t, admin), "robert_langdon",
		wireOf(t, admin), "checking the ledger")
	assert.ErrorIs(t, err, ErrTargetSelf,
		"impersonating oneself is not an admin refusal: it names the mistake")

	unknown := uuid.New()
	_, err = service.ImpersonateUser(t.Context(), wireOf(t, admin), "robert_langdon",
		wireOf(t, unknown), "checking the ledger")
	assert.ErrorIs(t, err, ErrTargetNotFound)
}

func TestStopImpersonatingEndsTheDelegationAndReissuesTheActor(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)

	admin := seedAccount(t, pool, "robert_langdon")
	target := seedAccount(t, pool, "sophie_neveu")

	out, err := service.ImpersonateUser(t.Context(), wireOf(t, admin), "robert_langdon",
		wireOf(t, target), "reproducing the vault's missing entry")
	require.NoError(t, err)

	caller := &jwtutils.Caller{
		UserID: wireOf(t, target),
		AccessClaims: jwtutils.AccessClaims{
			Username:      "sophie_neveu",
			SessionID:     out.SessionID,
			ActorID:       wireOf(t, admin),
			ActorUsername: "robert_langdon",
		},
	}

	stopped, err := service.StopImpersonating(t.Context(), out.SessionID, caller)
	require.NoError(t, err)

	assert.Equal(t, "fake-token-for-"+wireOf(t, admin), stopped.AccessToken,
		"the fresh pair names the administrator again")

	// The delegated row is stamped, and the stop is on the record beside the
	// start: the pair reads together in the log.
	row, err := service.repo.GetSession(t.Context(), pool, mustSessionID(t, out.SessionID))
	require.NoError(t, err)
	require.NotNil(t, row.RevokedAt)
	assert.Equal(t, 1, auditCount(t, pool, audit.EventImpersonationStopped, mustSessionID(t, out.SessionID).UUID()))
}

func TestStopImpersonatingRefusesTheNonDelegatedAndTheForeign(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool)

	admin := seedAccount(t, pool, "robert_langdon")
	target := seedAccount(t, pool, "sophie_neveu")
	bystander := seedAccount(t, pool, "rubeus_hagrid")

	sid, token := seedSession(t, pool, target, "plain", "credential", false)

	// A live session that is nobody's delegation refuses the stop even when
	// its caller carries actor claims — the row is the proof, not the token.
	forged := &jwtutils.Caller{
		UserID: wireOf(t, target),
		AccessClaims: jwtutils.AccessClaims{
			Username:  "sophie_neveu",
			SessionID: sid.String(),
			ActorID:   wireOf(t, admin),
		},
	}
	_, err := service.StopImpersonating(t.Context(), sid.String(), forged)
	assert.ErrorIs(t, err, ErrNotImpersonating)

	// The administrator's own ordinary session cannot be ended by naming a
	// delegation that is not theirs.
	adminSid, _ := seedSession(t, pool, admin, "own", "credential", false)
	selfCaller := &jwtutils.Caller{
		UserID: wireOf(t, admin),
		AccessClaims: jwtutils.AccessClaims{
			Username:  "robert_langdon",
			SessionID: adminSid.String(),
		},
	}
	_, err = service.StopImpersonating(t.Context(), adminSid.String(), selfCaller)
	assert.ErrorIs(t, err, ErrNotImpersonating)

	// A delegated caller whose actor names someone else than the row's
	// recorded administrator is refused too: the actor pair must match the
	// row, not merely exist.
	out, err := service.ImpersonateUser(t.Context(), wireOf(t, admin), "robert_langdon",
		wireOf(t, target), "reproducing the vault's missing entry")
	require.NoError(t, err)

	stranger := &jwtutils.Caller{
		UserID: wireOf(t, target),
		AccessClaims: jwtutils.AccessClaims{
			Username:  "sophie_neveu",
			SessionID: out.SessionID,
			ActorID:   wireOf(t, bystander),
		},
	}
	_, err = service.StopImpersonating(t.Context(), out.SessionID, stranger)
	assert.ErrorIs(t, err, ErrNotImpersonating)

	_ = token
}

// mustSessionID parses the wire form a response carries into the typed id the
// rows hold.
func mustSessionID(t *testing.T, wire string) SessionID {
	t.Helper()
	sid, err := typeid.Parse[SessionID](wire)
	require.NoError(t, err)
	return sid
}
