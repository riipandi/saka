package verification

import (
	"context"
	"errors"
	"testing"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/jobs"
	"github.com/riipandi/saka/modules/identity/blocklist"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/testutils"
)

func emailChangeService(t *testing.T, pool *datastore.Postgres, noticesEnabled bool) *Service {
	t.Helper()
	service := testService(t, pool, true)
	return service.
		WithEmailChangeNotifier(jobs.NewEmailChangeNotifier(service.queue, nil, noticesEnabled)).
		// The tests exercise the flow, not the gate: the toggle's on state
		// is what an operator serving the feature runs.
		WithEmailChangeGate(stubGate{on: true})
}

// stubGate is the change toggle's test double: the on state answers for the
// catalog the gate reads.
type stubGate struct {
	on bool
}

func (g stubGate) GetBool(context.Context, string) (bool, error) { return g.on, nil }

// GetString answers nothing: an unreadable mode key keeps the bulk default.
func (g stubGate) GetString(context.Context, string) (string, error) {
	return "", errors.New("unreadable setting")
}

func pendingCount(t *testing.T, service *Service, name string) int64 {
	t.Helper()
	count, err := service.queue.Pending(t.Context(), name)
	require.NoError(t, err)
	return count
}

func pendingChangeToken(t *testing.T, pool *datastore.Postgres, userID string) EmailChangeToken {
	t.Helper()
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "payload", "expires_at", "last_sent_at")
	sb.From(entity.TableAuthTokens)
	sb.Where(sb.Equal("user_id", userID), sb.Equal("purpose", PurposeEmailChange))

	query, args := sb.Build()
	var row EmailChangeToken
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&row.ID, &row.UserID, &row.Payload, &row.ExpiresAt, &row.LastSentAt))
	return row
}

func TestRequestEmailChangeBindsTheTokenToTheNewAddress(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	userID := uuid.MustParse(seedUser(t, pool, "rlangdon", "langdon@example.com", false))

	require.NoError(t, service.RequestEmailChange(t.Context(), userID, "langdon@new.example.com"))

	// The confirm-code message and the pending notice both travel, and the
	// row binds the pending address: the confirmation will move the account
	// to the address the request named.
	assert.Equal(t, int64(1), pendingCount(t, service, jobs.EmailChangeRequestEmailName), "the confirm code is queued to the new address")
	assert.Equal(t, int64(1), pendingCount(t, service, jobs.EmailChangeNoticeName), "the old address hears about the request")

	row := pendingChangeToken(t, pool, userID.String())
	assert.Equal(t, "langdon@new.example.com", row.Payload)

	// A second request inside the cooldown refuses, whatever address it
	// names.
	err := service.RequestEmailChange(t.Context(), userID, "langdon@other.example.com")
	assert.ErrorIs(t, err, ErrResendTooSoon)
}

func TestRequestEmailChangeRefusesTakenAndSameAddresses(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	userID := uuid.MustParse(seedUser(t, pool, "rlangdon", "langdon@example.com", false))
	seedUser(t, pool, "sneveu", "neveu@example.com", false)

	err := service.RequestEmailChange(t.Context(), userID, "langdon@example.com")
	assert.ErrorIs(t, err, ErrSameEmail, "the current address is nothing to change")

	err = service.RequestEmailChange(t.Context(), userID, "neveu@example.com")
	assert.ErrorIs(t, err, ErrEmailTaken, "another account's address is refused at the request")

	// Neither refusal left a pending row behind.
	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.auth_tokens WHERE purpose = $1", PurposeEmailChange).Scan(&count))
	assert.Zero(t, count)
}

func TestConfirmEmailChangeMovesTheAccountOnce(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	userID := seedUser(t, pool, "vvetra", "vetra@example.com", false)

	raw := "vetra-change-token-value"
	parsed, parseErr := uuid.Parse(userID)
	require.NoError(t, parseErr)
	require.NoError(t, service.repo.UpsertEmailChangeToken(t.Context(), pool, parsed, crypto.HashHexToken(raw), "vetra@new.example.com",
		time.Now().Add(time.Hour), time.Now()))

	require.NoError(t, service.ConfirmEmailChange(t.Context(), raw))

	// The account moved and the move reads as verified: the token proved
	// control of the new address, so the proof applies to it.
	var email string
	var verifiedAt *time.Time
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT email, email_verified_at FROM public.users WHERE id = $1", userID).Scan(&email, &verifiedAt))
	assert.Equal(t, "vetra@new.example.com", email)
	require.NotNil(t, verifiedAt, "a proven address is a verified one")

	// The token is consumed: a replay answers the same failure an unknown
	// one does.
	assert.Equal(t, int64(1), pendingCount(t, service, jobs.EmailChangeNoticeName), "the new address hears about the completed change")
	err := service.ConfirmEmailChange(t.Context(), raw)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestConfirmEmailChangeRefusesAnAddressClaimedInTheMeantime(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	userID := seedUser(t, pool, "rkohl", "kohl@example.com", false)

	raw := "kohl-change-token-value"
	parsed, parseErr := uuid.Parse(userID)
	require.NoError(t, parseErr)
	require.NoError(t, service.repo.UpsertEmailChangeToken(t.Context(), pool, parsed, crypto.HashHexToken(raw), "granger@example.com",
		time.Now().Add(time.Hour), time.Now()))

	// Another account claims the address between the request and the click.
	seedUser(t, pool, "hgranger", "granger@example.com", false)

	err := service.ConfirmEmailChange(t.Context(), raw)
	assert.ErrorIs(t, err, ErrEmailTaken)

	// The token is not consumed by the refusal: the requester may pick a
	// different address with the same link still standing.
	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.auth_tokens WHERE purpose = $1", PurposeEmailChange).Scan(&count))
	assert.Equal(t, 1, count)

	var email string
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT email FROM public.users WHERE id = $1", userID).Scan(&email))
	assert.Equal(t, "kohl@example.com", email)
}

// A re-request replaces the pending row under the same id. The confirm that
// read the row before the replacement must not apply the stale payload: the
// delete carries the hash it resolved, so a superseded token removes nothing
// and the transaction aborts before the address moves.
func TestConfirmEmailChangeRefusesASupersededToken(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	userID := seedUser(t, pool, "hermione", "hermione@example.com", false)

	stale := "hermione-stale-change-token"
	parsed, parseErr := uuid.Parse(userID)
	require.NoError(t, parseErr)
	require.NoError(t, service.repo.UpsertEmailChangeToken(t.Context(), pool, parsed,
		crypto.HashHexToken(stale), "hermione@first.example.com", time.Now().Add(time.Hour), time.Now()))

	// A second request replaces the row in place: the id survives, the hash
	// and the payload move to the newest address.
	fresh := "hermione-fresh-change-token"
	require.NoError(t, service.repo.UpsertEmailChangeToken(t.Context(), pool, parsed,
		crypto.HashHexToken(fresh), "hermione@second.example.com", time.Now().Add(time.Hour), time.Now()))

	err := service.ConfirmEmailChange(t.Context(), stale)
	assert.ErrorIs(t, err, ErrInvalidToken, "the superseded token confirms nothing")

	var email string
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT email FROM public.users WHERE id = $1", userID).Scan(&email))
	assert.Equal(t, "hermione@example.com", email, "the stale payload never reached the account")

	// The live token still works: the replacement is the one that confirms.
	require.NoError(t, service.ConfirmEmailChange(t.Context(), fresh))
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT email FROM public.users WHERE id = $1", userID).Scan(&email))
	assert.Equal(t, "hermione@second.example.com", email)
}

// The delete's guard is the hash the caller resolved, so a row a re-request
// replaced is removed by nothing: the stale delete reports no rows and leaves
// the live token standing.
func TestDeleteTokenRefusesAStaleHash(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	userID := seedUser(t, pool, "gsilas", "silas@example.com", false)

	stale := "silas-stale-token"
	parsed, parseErr := uuid.Parse(userID)
	require.NoError(t, parseErr)
	require.NoError(t, service.repo.UpsertEmailChangeToken(t.Context(), pool, parsed,
		crypto.HashHexToken(stale), "silas@first.example.com", time.Now().Add(time.Hour), time.Now()))

	token := pendingChangeToken(t, pool, userID)
	require.NoError(t, service.repo.UpsertEmailChangeToken(t.Context(), pool, parsed,
		crypto.HashHexToken("silas-live-token"), "silas@second.example.com", time.Now().Add(time.Hour), time.Now()))

	consumed, err := service.repo.DeleteToken(t.Context(), pool, token.ID, crypto.HashHexToken(stale), PurposeEmailChange)
	require.NoError(t, err)
	assert.False(t, consumed, "a stale hash removes nothing")

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.auth_tokens WHERE purpose = $1", PurposeEmailChange).Scan(&count))
	assert.Equal(t, 1, count, "the live token survives the stale delete")
}

func TestConfirmEmailChangeRefusesAnExpiredToken(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	userID := seedUser(t, pool, "gsilas", "silas@example.com", false)

	raw := "silas-expired-token-value"
	parsed, parseErr := uuid.Parse(userID)
	require.NoError(t, parseErr)
	// The table refuses a backdated row, so the test moves the service's
	// clock past the window a fresh token closes with.
	require.NoError(t, service.repo.UpsertEmailChangeToken(t.Context(), pool, parsed, crypto.HashHexToken(raw), "silas@new.example.com",
		time.Now().Add(2*time.Second), time.Now()))
	service.now = func() time.Time { return time.Now().Add(time.Hour) }

	err := service.ConfirmEmailChange(t.Context(), raw)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

// fakeSubaddresses is the collision guard's test double: it answers from a
// base list the way the real service does, and its read can fail on demand.
type fakeSubaddresses struct {
	bases   []string
	failing bool
}

func (f *fakeSubaddresses) CollisionTaken(_ context.Context, address string) (bool, error) {
	if f.failing {
		return false, errors.New("the collision scan failed")
	}
	base := blocklist.CollisionBase(address)
	for _, held := range f.bases {
		if held == base {
			return true, nil
		}
	}
	return false, nil
}

// TestRequestEmailChangeRefusesACollidingBase pins the subaddress guard:
// with the toggle on, a change toward an address whose base another account
// holds is refused with the generic failure — the answer names nothing
// about the account whose base collided — and no token row is left behind.
func TestRequestEmailChangeRefusesACollidingBase(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	service.WithEmailChangeGate(stubGate{on: true})
	service.WithSubaddressGuard(&fakeSubaddresses{bases: []string{"neveu@example.com"}})
	userID := uuid.MustParse(seedUser(t, pool, "rlangdon", "langdon@example.com", false))

	err := service.RequestEmailChange(t.Context(), userID, "neveu+tag@example.com")
	assert.ErrorIs(t, err, ErrSubaddressBlocked)

	// No pending row: the refusal spent nothing.
	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.auth_tokens WHERE purpose = $1", PurposeEmailChange).Scan(&count))
	assert.Zero(t, count)
}

// TestRequestEmailChangeSubaddressToggleAndFailure pins the guard's off
// state: with the toggle off, a colliding base passes — the confirm's own
// taken-check still answers.
func TestRequestEmailChangeSubaddressToggleAndFailure(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	inner := testService(t, pool, true)
	service := inner.
		WithEmailChangeNotifier(jobs.NewEmailChangeNotifier(inner.queue, nil, true)).
		WithEmailChangeGate(toggleGate{changeEmail: true, blockSubaddresses: false}).
		WithSubaddressGuard(&fakeSubaddresses{bases: []string{"neveu@example.com"}})
	userID := uuid.MustParse(seedUser(t, pool, "rlangdon", "langdon@example.com", false))
	seedUser(t, pool, "sneveu", "neveu@example.com", false)

	require.NoError(t, service.RequestEmailChange(t.Context(), userID, "sneveu+tag@example.com"),
		"the toggle off, the wired guard is not asked")
}

// TestRequestEmailChangeToleratesAFailingScan pins the fail-open stance on
// the change flow: a scan that cannot answer refuses nothing.
func TestRequestEmailChangeToleratesAFailingScan(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	service.WithEmailChangeGate(stubGate{on: true})
	service.WithSubaddressGuard(&fakeSubaddresses{failing: true})
	userID := uuid.MustParse(seedUser(t, pool, "rlangdon", "langdon@example.com", false))

	require.NoError(t, service.RequestEmailChange(t.Context(), userID, "vetra@new.example.com"))
}

// toggleGate is the keyed gate double: each catalog key answers its own
// state, so a test can arm the change flow and leave the subaddress guard's
// toggle off.
type toggleGate struct {
	changeEmail       bool
	blockSubaddresses bool
	strict            bool
}

func (g toggleGate) GetBool(_ context.Context, key string) (bool, error) {
	switch key {
	case SettingChangeEmailEnabled:
		return g.changeEmail, nil
	case SettingAccessBlockSubaddresses:
		return g.blockSubaddresses, nil
	}
	return false, errors.New("unreadable setting")
}

func (g toggleGate) GetString(_ context.Context, key string) (string, error) {
	if key == SettingUserEnumerationProtection {
		if g.strict {
			return "strict", nil
		}
		return "bulk", nil
	}
	return "", errors.New("unreadable setting")
}

// TestRequestEmailChangeUnderStrictHidesTheTakenAddress pins the strict
// enumeration answer: a change toward an address another account holds
// answers as if the verification had started — no error, no token row, no
// message on either queue — so the response no longer tells the caller the
// address is taken. The bulk default keeps the honest refusal.
func TestRequestEmailChangeUnderStrictHidesTheTakenAddress(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	inner := testService(t, pool, true)
	service := inner.
		WithEmailChangeNotifier(jobs.NewEmailChangeNotifier(inner.queue, nil, true)).
		WithEmailChangeGate(toggleGate{changeEmail: true, blockSubaddresses: false, strict: true})
	userID := uuid.MustParse(seedUser(t, pool, "rlangdon", "langdon@example.com", false))
	seedUser(t, pool, "sneveu", "neveu@example.com", false)

	require.NoError(t, service.RequestEmailChange(t.Context(), userID, "neveu@example.com"),
		"strict answers the started shape, not the collision")

	// Nothing was spent: no token row, no message on either queue the
	// honest request sends.
	assert.Zero(t, pendingChangeCount(t, pool, userID.String()))
	assert.Zero(t, pendingCount(t, inner, jobs.EmailChangeRequestEmailName))
	assert.Zero(t, pendingCount(t, inner, jobs.EmailChangeNoticeName))
}

// TestRequestEmailChangeUnderBulkKeepsTheRefusal pins the mode's default: a
// taken address answers the collision it always answered.
func TestRequestEmailChangeUnderBulkKeepsTheRefusal(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	userID := uuid.MustParse(seedUser(t, pool, "rlangdon", "langdon@example.com", false))
	seedUser(t, pool, "sneveu", "neveu@example.com", false)

	err := service.RequestEmailChange(t.Context(), userID, "neveu@example.com")
	assert.ErrorIs(t, err, ErrEmailTaken)
}

// pendingChangeCount counts the email-change token rows one account holds.
func pendingChangeCount(t *testing.T, pool *datastore.Postgres, userID string) int64 {
	t.Helper()
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TableAuthTokens)
	sb.Where(sb.Equal("user_id", userID), sb.Equal("purpose", PurposeEmailChange))

	query, args := sb.Build()
	var count int64
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}
