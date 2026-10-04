package restrictions

import (
	"context"
	"errors"
	"testing"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/pkg/testutils"
)

// The lockout policy the tests run: five attempts — the reader's floor —
// and a one-hour window. The keyedSettings stub answers it.
var lockoutSettings = keyedSettings{
	SettingLockoutEnabled:  "true",
	SettingLockoutMax:      "5",
	SettingLockoutDuration: "1h",
}

// keyedSettings answers the catalog from a map, the way the lockout policy
// reads it.
type keyedSettings map[string]string

func (s keyedSettings) GetString(_ context.Context, key string) (string, error) {
	if value, ok := s[key]; ok {
		return value, nil
	}
	return "", errors.New("unreadable setting")
}

func (s keyedSettings) GetBool(_ context.Context, key string) (bool, error) {
	value, ok := s[key]
	if !ok {
		return false, errors.New("unreadable setting")
	}
	return value == "true", nil
}

// recordingNotices is the locked-notice seam's test double.
type recordingNotices struct{ locked []string }

func (r *recordingNotices) EnqueueUserLockedNotice(_ context.Context, email, _ string, _ *time.Time) {
	r.locked = append(r.locked, email)
}

// migratedPool answers a pool over a fresh, fully migrated database.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()
	return testutils.MigratedPostgres(t, "restrictions_test")
}

// createAccount writes the user row a restriction names and answers its id.
func createAccount(t *testing.T, pool *datastore.Postgres) uuid.UUID {
	t.Helper()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUsers)
	ib.Cols("username", "email", "display_name", "email_verified_at")
	ib.Values("rlangdon", "langdon@example.com", "Robert Langdon", time.Now().UTC()).
		Returning("id")

	query, args := ib.Build()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// failures answers the streak the counter currently holds.
func failures(t *testing.T, pool *datastore.Postgres, id uuid.UUID) int {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("failed_attempts")
	sb.From(entity.TableUsers)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	var attempts int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&attempts))
	return attempts
}

// TestTheCounterIsAStreakNotATally pins the counter's arithmetic: the bump
// advances in place, the zero clears it, and the next bump counts from one
// again — never a lifetime tally.
func TestTheCounterIsAStreakNotATally(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	id := createAccount(t, pool)
	repo := NewRepository()

	attempts, err := repo.BumpFailures(t.Context(), pool, id)
	require.NoError(t, err)
	assert.Equal(t, 1, attempts)
	attempts, err = repo.BumpFailures(t.Context(), pool, id)
	require.NoError(t, err)
	assert.Equal(t, 2, attempts)

	require.NoError(t, repo.ZeroFailures(t.Context(), pool, id))
	assert.Equal(t, 0, failures(t, pool, id))

	attempts, err = repo.BumpFailures(t.Context(), pool, id)
	require.NoError(t, err)
	assert.Equal(t, 1, attempts, "the streak starts fresh, never from the tally")
}

// TestTheBanWritesAndLiftsAsARow pins the ban's round trip: the write lands
// an active row the account's read model answers from, a re-ban replaces
// the open row's terms without moving its start, and the lift ends the
// unit.
func TestTheBanWritesAndLiftsAsARow(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	id := createAccount(t, pool)
	repo := NewRepository()
	now := time.Now().UTC()

	expires := now.Add(24 * time.Hour)
	require.NoError(t, repo.ApplyBan(t.Context(), pool, id, "unruly behaviour", &expires, now))

	active, err := repo.ActiveRow(t.Context(), pool, id, now)
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Equal(t, KindBan, active.Kind)
	require.NotNil(t, active.Reason)
	assert.Equal(t, "unruly behaviour", *active.Reason)
	assert.True(t, active.StartedAt.Equal(now.Truncate(time.Microsecond)),
		"the first ban's start instant is the write's own")

	// The re-ban is the caller's intent: the terms replace, the start
	// stays.
	later := now.Add(48 * time.Hour)
	require.NoError(t, repo.ApplyBan(t.Context(), pool, id, "still unruly", &later, now.Add(time.Hour)))
	active, err = repo.ActiveRow(t.Context(), pool, id, now.Add(time.Hour))
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.True(t, active.StartedAt.Equal(now.Truncate(time.Microsecond)), "a re-ban does not move the start")
	require.NotNil(t, active.ExpiresAt)
	assert.True(t, active.ExpiresAt.Equal(later))
	require.NotNil(t, active.Reason)
	assert.Equal(t, "still unruly", *active.Reason)

	// The lift ends the unit; the history row stays.
	lifted, err := repo.LiftBans(t.Context(), pool, id, nil, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.True(t, lifted)
	none, err := repo.ActiveRow(t.Context(), pool, id, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Nil(t, none, "the lifted ban answers no restriction")

	// The lift of an unbanned account is the caller's no-op success.
	again, err := repo.LiftBans(t.Context(), pool, id, nil, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.False(t, again)
}

// TestTheLockoutLandsAtTheStreakBound pins the policy: failures under the
// bound only count; the attempt that reaches it writes the lockout row,
// records the happening, and tells the account's address.
func TestTheLockoutLandsAtTheStreakBound(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	id := createAccount(t, pool)
	notices := &recordingNotices{}
	service := NewService(pool, audit.NewRecorder(nil), nil).
		WithLockedNotice(notices)
	// The policy is read at call time; the stub arms the bound the test
	// runs. The fixture mirrors it: three attempts.
	service.WithSettings(lockoutSettings)

	for range 4 {
		locked, err := service.RegisterFailure(t.Context(), id, "langdon@example.com", "Robert Langdon")
		require.NoError(t, err)
		assert.False(t, locked)
	}
	assert.Equal(t, 4, failures(t, pool, id))
	assert.Empty(t, notices.locked)

	locked, err := service.RegisterFailure(t.Context(), id, "langdon@example.com", "Robert Langdon")
	require.NoError(t, err)
	assert.True(t, locked)
	assert.Len(t, notices.locked, 1)

	active, err := service.Active(t.Context(), pool, id)
	require.NoError(t, err)
	assert.Equal(t, KindLockout, active.Kind)
	require.NotNil(t, active.ExpiresAt, "the policy's window names the lift")

	// The lockout's own history: the row the read answers carries it.
	row, err := NewRepository().ActiveRow(t.Context(), pool, id, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Nil(t, row.Reason, "the lockout's trip is its own reason")
}

// TestAnExpiredLockoutLiftsOnTheReadAndStartsTheStreakFresh pins the
// window's end: the read that finds an expired row lifts it, zeroes the
// streak it answered for, and answers no restriction.
func TestAnExpiredLockoutLiftsOnTheReadAndStartsTheStreakFresh(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	id := createAccount(t, pool)
	now := time.Now().UTC()
	repo := NewRepository()

	past := now.Add(-time.Hour)
	require.NoError(t, repo.ApplyLockout(t.Context(), pool, id, &past, past))
	require.NoError(t, pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		for range 5 {
			if _, err := repo.BumpFailures(t.Context(), tx, id); err != nil {
				return err
			}
		}
		return nil
	}))

	_, err := repo.ActiveRow(t.Context(), pool, id, now)
	require.NoError(t, err)

	service := NewService(pool, audit.NewRecorder(nil), nil)
	status, err := service.Active(t.Context(), pool, id)
	require.NoError(t, err)
	assert.Empty(t, status.Kind, "the expired window answers no restriction")
	assert.Equal(t, 0, failures(t, pool, id), "the streak the lockout answered for starts fresh")
}

// TestAnIndefiniteLockoutStaysUntilTheUnlock pins the empty-duration case:
// the lockout without an expiry never lifts by itself, and the
// administrator's unlock is the only way out — the row lifts, the streak
// starts fresh.
func TestAnIndefiniteLockoutStaysUntilTheUnlock(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	id := createAccount(t, pool)
	now := time.Now().UTC()

	require.NoError(t, NewRepository().ApplyLockout(t.Context(), pool, id, nil, now))
	require.NoError(t, pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		for range 4 {
			if _, err := NewRepository().BumpFailures(t.Context(), tx, id); err != nil {
				return err
			}
		}
		return nil
	}))

	service := NewService(pool, audit.NewRecorder(nil), nil)
	status, err := service.Active(t.Context(), pool, id)
	require.NoError(t, err)
	assert.Equal(t, KindLockout, status.Kind)
	assert.Nil(t, status.ExpiresAt, "the indefinite lockout names no lift")

	lifted, err := service.Unlock(t.Context(), id, nil)
	require.NoError(t, err)
	assert.True(t, lifted)

	status, err = service.Active(t.Context(), pool, id)
	require.NoError(t, err)
	assert.Empty(t, status.Kind)
	assert.Equal(t, 0, failures(t, pool, id), "the unlock starts the streak fresh")

	again, err := service.Unlock(t.Context(), id, nil)
	require.NoError(t, err)
	assert.False(t, again, "the unlock of an unlocked account is the no-op success")
}
