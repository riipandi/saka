package signin

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/riipandi/saka/modules/identity/user"
)

// recordingNotices is the notice channel a test inspects: every notice the
// service hands over lands here, in the order it was handed over.
type recordingNotices struct {
	notices []NewDeviceNotice
}

func (r *recordingNotices) EnqueueNewDeviceNotice(_ context.Context, notice NewDeviceNotice) {
	r.notices = append(r.notices, notice)
}

const (
	langdonFingerprint = "fp-robert-langdon"
	vetraFingerprint   = "fp-vittoria-vetra"
)

func TestSignInNoticesTheFirstSignInFromADevice(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	recorder := &recordingNotices{}
	service.WithDeviceNotifier(recorder)

	id := createAccount(t, pool, "rlangdon", "langdon@example.com", "@dmin123", nil)

	result, err := service.SignIn(t.Context(), Params{
		Identity:    "langdon@example.com",
		Password:    "@dmin123",
		Fingerprint: langdonFingerprint,
	})
	require.NoError(t, err)
	require.Equal(t, id, mustParseID(t, result.User.ID))

	require.Len(t, recorder.notices, 1, "the first sighting of a fingerprint is noticed")
	notice := recorder.notices[0]
	assert.Equal(t, "langdon@example.com", notice.Email)
	assert.Equal(t, langdonFingerprint, notice.Fingerprint)
	assert.False(t, notice.SignedInAt.IsZero(), "the notice carries when the session opened")
}

func TestSignInNoticesNothingWhenTheDeviceIsAlreadyKnown(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	recorder := &recordingNotices{}
	service.WithDeviceNotifier(recorder)
	createAccount(t, pool, "sneveu", "neveu@example.com", "@dmin123", nil)

	_, err := service.SignIn(t.Context(), Params{
		Identity:    "neveu@example.com",
		Password:    "@dmin123",
		Fingerprint: langdonFingerprint,
	})
	require.NoError(t, err)

	_, err = service.SignIn(t.Context(), Params{
		Identity:    "neveu@example.com",
		Password:    "@dmin123",
		Fingerprint: langdonFingerprint,
	})
	require.NoError(t, err)

	assert.Len(t, recorder.notices, 1, "the second sighting of a known device notices nothing")
}

func TestSignInNoticesNothingWithoutAFingerprint(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	recorder := &recordingNotices{}
	service.WithDeviceNotifier(recorder)
	createAccount(t, pool, "hgranger", "granger@example.com", "@dmin123", nil)

	_, err := service.SignIn(t.Context(), Params{
		Identity: "granger@example.com",
		Password: "@dmin123",
	})
	require.NoError(t, err)

	assert.Empty(t, recorder.notices, "a client that sends no fingerprint cannot be a device")

	// And it left no row behind: the table holds only fingerprints that
	// were judged.
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM public.known_devices").Scan(&count))
	assert.Zero(t, count)
}

func TestSignInJudgesTheFingerprintPerAccount(t *testing.T) {
	// The unique pair is (user_id, fingerprint): one browser shared by two
	// accounts is a new device for each of them, because the notice speaks
	// for the account, not for the machine.
	pool := migratedPool(t)
	service := testService(t, pool)
	recorder := &recordingNotices{}
	service.WithDeviceNotifier(recorder)
	createAccount(t, pool, "vvetra", "vetra@example.com", "@dmin123", nil)
	createAccount(t, pool, "rkohl", "kohl@example.com", "@dmin123", nil)

	_, err := service.SignIn(t.Context(), Params{
		Identity:    "vetra@example.com",
		Password:    "@dmin123",
		Fingerprint: vetraFingerprint,
	})
	require.NoError(t, err)
	_, err = service.SignIn(t.Context(), Params{
		Identity:    "kohl@example.com",
		Password:    "@dmin123",
		Fingerprint: vetraFingerprint,
	})
	require.NoError(t, err)

	require.Len(t, recorder.notices, 2, "each account's first sighting is its own")
	assert.Equal(t, "vetra@example.com", recorder.notices[0].Email)
	assert.Equal(t, "kohl@example.com", recorder.notices[1].Email)
}

func TestSignInNoticesNothingWithoutANotifier(t *testing.T) {
	// A service wired without a channel signs in as before — the notice is
	// an add-on, never a requirement. The fingerprint is still judged, so a
	// notifier wired later does not re-notice an old device.
	pool := migratedPool(t)
	service := testService(t, pool)
	createAccount(t, pool, "gsilas", "silas@example.com", "@dmin123", nil)

	_, err := service.SignIn(t.Context(), Params{
		Identity:    "silas@example.com",
		Password:    "@dmin123",
		Fingerprint: langdonFingerprint,
	})
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM public.known_devices").Scan(&count))
	assert.Equal(t, 1, count)
}

func TestMarkDeviceSeenAnswersTheJudgementItself(t *testing.T) {
	pool := migratedPool(t)
	repo := NewRepository(pool)
	id := createAccount(t, pool, "gsilas", "silas@example.com", "@dmin123", nil)

	first, err := repo.MarkDeviceSeen(t.Context(), pool, id, langdonFingerprint, time.Now())
	require.NoError(t, err)
	assert.True(t, first, "a fingerprint the table never saw is a first sighting")

	second, err := repo.MarkDeviceSeen(t.Context(), pool, id, langdonFingerprint, time.Now())
	require.NoError(t, err)
	assert.False(t, second, "the same fingerprint again is a known device")

	other, err := repo.MarkDeviceSeen(t.Context(), pool, id, vetraFingerprint, time.Now())
	require.NoError(t, err)
	assert.True(t, other, "a different fingerprint is its own first sighting")

	// A later sighting moves last_seen_at and leaves first_seen_at alone.
	var firstSeen, lastSeen time.Time
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT first_seen_at, last_seen_at FROM public.known_devices WHERE user_id = $1 AND device_fingerprint = $2",
		id, langdonFingerprint).Scan(&firstSeen, &lastSeen))

	later := firstSeen.Add(time.Hour)
	_, err = repo.MarkDeviceSeen(t.Context(), pool, id, langdonFingerprint, later)
	require.NoError(t, err)

	var firstAfter, lastAfter time.Time
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT first_seen_at, last_seen_at FROM public.known_devices WHERE user_id = $1 AND device_fingerprint = $2",
		id, langdonFingerprint).Scan(&firstAfter, &lastAfter))
	assert.WithinDuration(t, firstSeen, firstAfter, time.Microsecond, "the first sighting never moves")
	assert.WithinDuration(t, later, lastAfter, time.Microsecond, "the last sighting tracks the newest sign-in")
}

// mustParseID reads the TypeID form a result answers with back into the UUID
// the fixture wrote.
func mustParseID(t *testing.T, id string) uuid.UUID {
	t.Helper()
	decoded, err := user.UUIDFromWire(id)
	require.NoError(t, err)
	return decoded
}
