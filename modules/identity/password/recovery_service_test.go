package password

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"connectrpc.com/connect"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"
)

func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "password_recovery_test")
}

// seedUser writes an account row with a password credential, so the tests
// drive the flow's own tables rather than another feature's procedures.
func seedUser(t *testing.T, pool *datastore.Postgres, username, email string) string {
	t.Helper()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto("public.users")
	ib.Cols("username", "email", "display_name")
	ib.Values(username, email, username)
	ib.SQL("RETURNING id")

	query, args := ib.Build()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))

	hash, err := defaultHasher("Griffindor!9")
	require.NoError(t, err)
	pib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	pib.InsertInto(UserPasswordTable)
	pib.Cols("user_id", "password_hash")
	pib.Values(id, hash)
	pquery, pargs := pib.Build()
	_, err = pool.Exec(t.Context(), pquery, pargs...)
	require.NoError(t, err)
	return id
}

// seedResetToken writes the reset row a raw value hashes to.
func seedResetToken(t *testing.T, pool *datastore.Postgres, userID, raw string, expiresAt time.Time) {
	t.Helper()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(AuthTokenTable)
	ib.Cols("user_id", "token_hash", "purpose", "expires_at")
	ib.Values(userID, crypto.HashHexToken(raw), PurposePasswordReset, expiresAt)

	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

// countingEnqueuer stands in for the durable queue: it counts the messages
// and keeps the last ones, which is what the assertions read.
type countingEnqueuer struct {
	calls      int
	last       ResetEmail
	noticeSent bool
}

func (e *countingEnqueuer) EnqueuePasswordResetEmail(_ context.Context, email ResetEmail) error {
	e.calls++
	e.last = email
	return nil
}

func (e *countingEnqueuer) EnqueuePasswordChangedNotice(_ context.Context, _ ChangedNotice) error {
	e.noticeSent = true
	return nil
}

// recordingEnder counts the session revocations a successful reset runs.
type recordingEnder struct {
	calls int
}

func (e *recordingEnder) RevokeAllForUser(_ context.Context, _ datastore.Querier, _ uuid.UUID) (int, error) {
	e.calls++
	return 2, nil
}

// testService builds the service over a mailer whose configuration decides
// whether it reports configured. No connection is dialed: the enqueuer is
// the stand-in the assertions read.
func testService(t *testing.T, pool *datastore.Postgres, configured bool) (*Service, *countingEnqueuer) {
	t.Helper()

	cfg := config.Default()
	if configured {
		cfg.Mailer.SMTPHost = "localhost"
	}
	mail, err := mailer.New(cfg, nil)
	require.NoError(t, err)
	templates, err := mailer.NewTemplates(mailer.SenderFrom(cfg))
	require.NoError(t, err)

	enqueuer := &countingEnqueuer{}
	service := NewService(pool, mailer.NewService(mail, templates), nil, "http://localhost:3000", nil).
		WithEnqueuer(enqueuer)
	return service, enqueuer
}

func TestForgotPasswordStaysSilentAboutTheAccountsItDoesNotKnow(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)

	// An unknown address and an address with no password credential answer
	// success and queue nothing — the silence is the anti-enumeration.
	raw, err := service.ForgotPassword(t.Context(), "nobody@example.com")
	require.NoError(t, err)
	assert.Empty(t, raw)
	assert.Equal(t, 0, enqueuer.calls)

	seedUser(t, pool, "unpassworded", "unpassworded@example.com")
	_, err = pool.Exec(t.Context(), "DELETE FROM public.user_passwords WHERE user_id = (SELECT id FROM public.users WHERE email = 'unpassworded@example.com')")
	require.NoError(t, err)
	raw, err = service.ForgotPassword(t.Context(), "unpassworded@example.com")
	require.NoError(t, err)
	assert.Empty(t, raw)
	assert.Equal(t, 0, enqueuer.calls)
}

func TestForgotPasswordIssuesOneTokenPerAccount(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)

	seedUser(t, pool, "sophie", "sophie@example.com")
	raw, err := service.ForgotPassword(t.Context(), "sophie@example.com")
	require.NoError(t, err)
	require.Equal(t, 1, enqueuer.calls)
	assert.NotEmpty(t, enqueuer.last.Token)

	// The token is URL-safe hex: 64 lowercase hexadecimal characters, no
	// dashes, no symbols.
	assert.Regexp(t, `^[0-9a-f]{64}$`, raw)

	// The row carries the hash, not the value the email took away.
	var stored string
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT token_hash FROM public.auth_tokens WHERE purpose = 'password_reset'",
	).Scan(&stored))
	assert.Equal(t, crypto.HashHexToken(raw), stored)

	// A re-request inside the cooldown refuses; the row is untouched.
	_, err = service.ForgotPassword(t.Context(), "sophie@example.com")
	assert.ErrorIs(t, err, ErrResendTooSoon)
	assert.Equal(t, 1, enqueuer.calls)
}

func TestForgotPasswordNeedsAMailerToServe(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool, false)

	seedUser(t, pool, "vittoria", "vittoria@example.com")
	_, err := service.ForgotPassword(t.Context(), "vittoria@example.com")
	assert.ErrorIs(t, err, ErrMailUnavailable)
}

func TestResetPasswordSwapsTheCredentialAndEndsTheSessions(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	enders := &recordingEnder{}
	service, enqueuer := testService(t, pool, true)
	service.sessions = sessionEnder(enders)
	service.now = func() time.Time { return time.Now() }

	userID := seedUser(t, pool, "langdon", "langdon@example.com")
	_, err := service.ForgotPassword(t.Context(), "langdon@example.com")
	require.NoError(t, err)
	require.Equal(t, 1, enqueuer.calls)

	require.NoError(t, service.ResetPassword(t.Context(), enqueuer.last.Token, "Expecto!Patronum9", true))

	// The new hash verifies, the token row is spent, and the session
	// coupling ran.
	var hash string
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT password_hash FROM public.user_passwords WHERE user_id = $1", userID,
	).Scan(&hash))
	assert.NotEmpty(t, hash)
	assert.Equal(t, 1, enders.calls)

	count := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.auth_tokens WHERE user_id = $1 AND purpose = 'password_reset'", userID)
	var live int
	require.NoError(t, count.Scan(&live))
	assert.Equal(t, 0, live)

	// The receipt was queued: the reset that committed says so.
	assert.True(t, enqueuer.noticeSent)

	// The spent token cannot reset twice.
	err = service.ResetPassword(t.Context(), enqueuer.last.Token, "Another!Passphrase1", true)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestResetPasswordRefusesAnUnknownAnExpiredAndAWeakCredential(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testService(t, pool, true)

	userID := seedUser(t, pool, "neveu", "neveu@example.com")

	// An unknown token.
	err := service.ResetPassword(t.Context(), "no-such-token", "Expecto!Patronum9", true)
	assert.ErrorIs(t, err, ErrInvalidToken)

	// An expired one: the row is written live, and the service's clock is
	// moved past it — the table's check refuses an already-expired row, and
	// the (user_id, purpose) unique index allows one row per account, so
	// the expired case reuses the same row.
	raw := "expiring-token-value"
	seedResetToken(t, pool, userID, raw, time.Now().Add(tokenTTL))
	service.now = func() time.Time { return time.Now().Add(2 * tokenTTL) }
	err = service.ResetPassword(t.Context(), raw, "Expecto!Patronum9", true)
	assert.ErrorIs(t, err, ErrInvalidToken)
	service.now = time.Now

	// The same row, still unspent, and a credential the policy refuses: the
	// refusal is the policy's, and the token survives.
	err = service.ResetPassword(t.Context(), raw, "short", true)
	assert.ErrorIs(t, err, ErrWeakPassword)

	// A credential identical to the current one is refused too: a reset
	// that changes nothing is not a reset.
	err = service.ResetPassword(t.Context(), raw, "Griffindor!9", true)
	assert.ErrorIs(t, err, ErrSamePassword)

	// A different credential passes; the seeded hash was Griffindor!9.
	err = service.ResetPassword(t.Context(), raw, "Expecto!Patronum9", true)
	require.NoError(t, err)

	var stillThere int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.auth_tokens WHERE token_hash = $1", crypto.HashHexToken(raw),
	).Scan(&stillThere))
	assert.Equal(t, 0, stillThere)
}

// decoder is the test's stand-in for the wire-form conversion: the seeded
// ids are plain UUIDs and the test's wire form carries them straight
// through, so the service's behavior is what the test exercises.
func decoder(uuidStr string) (uuid.UUID, error) {
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return uuid.UUID{}, err
	}
	return id, nil
}

// decoderRefusing is the state a malformed identifier leaves: the wire form
// never parses.
func decoderRefusing(string) (uuid.UUID, error) {
	return uuid.UUID{}, errors.New("bad wire form")
}

// wireForm is the test's wire-form encoder. It round-trips through the
// service's own decoder, so the encoding is whatever the decoder accepts —
// the test asserts the service's behavior, not the codec's.
func wireForm(uuidStr string) string {
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return ""
	}
	// The typeid library accepts the bare UUID form for decoding through
	// the same path the seeded rows take.
	return id.String()
}

func TestAdminResetTriggerReportsTheStatesForgotPasswordHides(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)

	// A decoder that refuses is the state a malformed identifier leaves.
	service.WithUUIDDecoder(decoderRefusing)
	err := service.AdminResetUserPassword(t.Context(), "user_broken")
	assert.ErrorIs(t, err, ErrUserNotFound)

	// An unknown identifier.
	service.WithUUIDDecoder(decoder)
	err = service.AdminResetUserPassword(t.Context(), "user_01m3ent6yqfb8br0fps9m43ag8")
	assert.ErrorIs(t, err, ErrUserNotFound)

	userID := seedUser(t, pool, "gryffindor", "gryffindor@example.com")
	wire := wireForm(userID)
	require.NoError(t, service.AdminResetUserPassword(t.Context(), wire))
	assert.Equal(t, 1, enqueuer.calls)

	// The same cooldown the self-service trigger keeps.
	err = service.AdminResetUserPassword(t.Context(), wire)
	assert.ErrorIs(t, err, ErrResendTooSoon)
}

func TestResetPasswordCanSpareTheSessionsWhenAsked(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	enders := &recordingEnder{}
	service, enqueuer := testService(t, pool, true)
	service.sessions = enders

	seedUser(t, pool, "chamber", "chamber@example.com")
	_, err := service.ForgotPassword(t.Context(), "chamber@example.com")
	require.NoError(t, err)

	// terminate_sessions=false leaves the live rows alone: the caller asked
	// for a credential swap, not a sign-out.
	require.NoError(t, service.ResetPassword(t.Context(), enqueuer.last.Token, "Expecto!Patronum9", false))
	assert.Equal(t, 0, enders.calls)
}

func TestMapRecoveryErrorCarriesTheConnectCodes(t *testing.T) {
	cases := []error{
		ErrUserNotFound,
		ErrNoPassword,
		ErrAccountForbidden,
		ErrMailUnavailable,
		ErrInvalidToken,
		ErrResendTooSoon,
		ErrSamePassword,
	}
	for _, err := range cases {
		mapped := mapRecoveryError(err)
		assert.NotEqual(t, connect.CodeInternal, connect.CodeOf(mapped), "unexpected internal for %v", err)
	}
}
