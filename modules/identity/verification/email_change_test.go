package verification

import (
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/jobs"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"
)

func emailChangeService(t *testing.T, pool *datastore.Postgres, noticesEnabled bool) *Service {
	t.Helper()
	service := testService(t, pool, true)
	return service.WithEmailChangeNotifier(jobs.NewEmailChangeNotifier(service.queue, nil, noticesEnabled))
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
	sb.From(AuthTokenTable)
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
	userID := seedUser(t, pool, "rlangdon", "langdon@example.com", false)

	require.NoError(t, service.RequestEmailChange(t.Context(), "rlangdon", "langdon@new.example.com"))

	// The confirm-link message and the pending notice both travel, and the
	// row binds the pending address: the confirmation will move the account
	// to the address the request named, never one a replayed link picks.
	assert.Equal(t, int64(1), pendingCount(t, service, jobs.EmailChangeRequestEmailName), "the confirm link is queued to the new address")
	assert.Equal(t, int64(1), pendingCount(t, service, jobs.EmailChangeNoticeName), "the old address hears about the request")

	row := pendingChangeToken(t, pool, userID)
	assert.Equal(t, "langdon@new.example.com", row.Payload)

	// A second request inside the cooldown refuses, whatever address it
	// names.
	err := service.RequestEmailChange(t.Context(), "rlangdon", "langdon@other.example.com")
	assert.ErrorIs(t, err, ErrResendTooSoon)
}

func TestRequestEmailChangeRefusesTakenAndSameAddresses(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := emailChangeService(t, pool, true)
	seedUser(t, pool, "rlangdon", "langdon@example.com", false)
	seedUser(t, pool, "sneveu", "neveu@example.com", false)

	err := service.RequestEmailChange(t.Context(), "rlangdon", "langdon@example.com")
	assert.ErrorIs(t, err, ErrSameEmail, "the current address is nothing to change")

	err = service.RequestEmailChange(t.Context(), "rlangdon", "neveu@example.com")
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
