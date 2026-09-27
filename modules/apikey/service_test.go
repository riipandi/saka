package apikey

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"

	"uuid"
)

// migratedPool opens a database the migrations have built, so the key table
// exists.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "apikey_test")
}

// testService builds the service with a recorder that writes for real — a
// record is part of the transaction it describes, so the assertions read the
// table the writer fills.
func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	return NewService(pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
}

// clock returns the service and the knob that moves its sense of now, so a
// test can put a key past its expiry without writing an expiry the
// database's own check refuses.
func clock(t *testing.T, pool *datastore.Postgres) (*Service, *time.Time) {
	t.Helper()
	service := testService(t, pool)
	now := time.Now()
	service.now = func() time.Time { return now }
	return service, &now
}

// jump moves the service's clock and answers the instant it now reads.
func jump(t *testing.T, now *time.Time, d time.Duration) time.Time {
	t.Helper()
	*now = now.Add(d)
	return *now
}

// seedAccount inserts an account directly and answers its identifier. A key
// belongs to an account, so one must exist before the key does.
func seedAccount(t *testing.T, pool *datastore.Postgres, username string) uuid.UUID {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (username, email, first_name, last_name, display_name, is_admin)
		VALUES ($1::citext, $1::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger', $2)`,
		username, username == "admin")
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From("public.users")
	sb.Where(sb.Equal("username", username))

	query, args := sb.Build()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// created is a fixture key with a month-long window, for the tests that need
// more than one.
func created(t *testing.T, service *Service, owner uuid.UUID, name string) Issued {
	t.Helper()
	return createdExpiring(t, service, owner, name, 30*24*time.Hour)
}

// createdExpiring is a fixture key with an explicit window, for the tests
// whose clock outlives the month the default carries.
func createdExpiring(t *testing.T, service *Service, owner uuid.UUID, name string, ttl time.Duration) Issued {
	t.Helper()

	issued, err := service.Create(t.Context(), owner, CreateParams{
		Name:      name,
		ExpiresAt: time.Now().Add(ttl),
	})
	require.NoError(t, err)
	return issued
}

func TestCreateShowsTheKeyOnceAndRefusesADuplicateName(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	owner := seedAccount(t, pool, "hermione")

	issued, err := service.Create(t.Context(), owner, CreateParams{
		Name:        "hogwarts-library",
		Description: "for the restricted section",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	})
	require.NoError(t, err)

	// The raw credential is one prefix, one separator, one secret — and its
	// hash is the row's whole presence, so the raw value survives nowhere.
	require.Len(t, issued.Raw, prefixLength+1+secretLength)
	assert.True(t, strings.HasPrefix(issued.Raw, issued.Key.Prefix+keySeparator))
	assert.Equal(t, crypto.HashTokenBytes(issued.Raw), issued.Key.KeyHash)
	assert.NotEqual(t, issued.Raw, string(issued.Key.KeyHash))
	assert.Equal(t, "hogwarts-library", issued.Key.Name)
	require.NotNil(t, issued.Key.Descr)
	assert.Equal(t, "for the restricted section", *issued.Key.Descr)
	assert.Equal(t, owner, issued.Key.UserID)

	// The credential works the moment it exists.
	validated, err := service.Validate(t.Context(), issued.Raw)
	require.NoError(t, err)
	assert.Equal(t, owner, validated.ID)

	// The name is unique per owner, and the duplicate is the caller's
	// failure, not an internal error.
	_, err = service.Create(t.Context(), owner, CreateParams{
		Name:      "hogwarts-library",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	assert.ErrorIs(t, err, ErrKeyExists)

	// Another owner may hold the same name: the index is (name, owner).
	second := seedAccount(t, pool, "ron")
	_, err = service.Create(t.Context(), second, CreateParams{
		Name:      "hogwarts-library",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	assert.NoError(t, err)

	// The record of the creation names the key and its owner.
	assert.Equal(t, 1, auditCount(t, pool, audit.EventAPIKeyCreated, issued.Key.ID.String()))
}

func TestValidateRefusesAnUnknownExpiredRevokedOrDisabledKey(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, now := clock(t, pool)
	owner := seedAccount(t, pool, "hermione")

	live := createdExpiring(t, service, owner, "live-key", 90*24*time.Hour)

	// An unknown credential is the same answer an expired, revoked, or
	// disabled one gives: the refusal does not disclose which half failed.
	_, err := service.Validate(t.Context(), "abcd1234."+strings.Repeat("x", secretLength))
	assert.ErrorIs(t, err, ErrKeyNotFound)
	_, err = service.Validate(t.Context(), "")
	assert.ErrorIs(t, err, ErrKeyNotFound)

	// An expired key opens nothing.
	expired := created(t, service, owner, "expired-key")
	jump(t, now, 31*24*time.Hour)
	_, err = service.Validate(t.Context(), expired.Raw)
	assert.ErrorIs(t, err, ErrKeyNotFound)

	// A revoked key opens nothing, even inside its window.
	revoked := createdExpiring(t, service, owner, "revoked-key", 90*24*time.Hour)
	require.NoError(t, service.Revoke(t.Context(), owner, revoked.Key.ID))
	_, err = service.Validate(t.Context(), revoked.Raw)
	assert.ErrorIs(t, err, ErrKeyNotFound)

	// A disabled owner's keys open nothing: the credential is fine, the
	// account it acts as is not.
	disabled := createdExpiring(t, service, owner, "disabled-key", 90*24*time.Hour)
	_, err = pool.Exec(t.Context(), "UPDATE public.users SET disabled = true WHERE id = $1", owner)
	require.NoError(t, err)
	_, err = service.Validate(t.Context(), disabled.Raw)
	assert.ErrorIs(t, err, ErrKeyNotFound)
	_, err = pool.Exec(t.Context(), "UPDATE public.users SET disabled = false WHERE id = $1", owner)
	require.NoError(t, err)

	// The live key still works, and the validation it just passed left its
	// mark: the last-used instant is the touch the row carries.
	validated, err := service.Validate(t.Context(), live.Raw)
	require.NoError(t, err)
	assert.Equal(t, owner, validated.ID)
	row, err := service.repo.GetKey(t.Context(), pool, live.Key.ID)
	require.NoError(t, err)
	require.NotNil(t, row.LastUsed)
}

func TestRenewReplacesAnExpiredKeyAndRefusesALiveOne(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, now := clock(t, pool)
	owner := seedAccount(t, pool, "hermione")

	expired := created(t, service, owner, "expiring-key")
	live := created(t, service, owner, "live-key")
	_, err := pool.Exec(t.Context(), "UPDATE public.api_keys SET expires_at = $1 WHERE id = $2",
		time.Now().Add(365*24*time.Hour), live.Key.ID)
	require.NoError(t, err)

	// A window still open is not a renewal: the key works, and renewal is
	// how a key lives past its expiry, not how it escapes one.
	_, err = service.Renew(t.Context(), owner, live.Key.ID, time.Now().Add(24*time.Hour))
	assert.ErrorIs(t, err, ErrKeyNotExpired)

	// An expired key earns a new secret and a new window, and the reminder
	// stamp the old window earned dies with it.
	jump(t, now, 31*24*time.Hour)
	newExpiry := now.Add(30 * 24 * time.Hour)
	renewed, err := service.Renew(t.Context(), owner, expired.Key.ID, newExpiry)
	require.NoError(t, err)
	assert.NotEqual(t, expired.Raw, renewed.Raw, "the old secret is dead the moment the row is written")
	assert.Equal(t, crypto.HashTokenBytes(renewed.Raw), renewed.Key.KeyHash)
	assert.WithinDuration(t, newExpiry, renewed.Key.ExpiresAt, time.Second)
	assert.Nil(t, renewed.Key.EmailSent, "the new window earns a fresh reminder, not the old one's stamp")
	assert.NotNil(t, renewed.Key.UpdatedAt)

	// The new credential works; the old one opens nothing.
	_, err = service.Validate(t.Context(), renewed.Raw)
	assert.NoError(t, err)
	_, err = service.Validate(t.Context(), expired.Raw)
	assert.ErrorIs(t, err, ErrKeyNotFound)

	// A key another account owns is the not-found failure, the same as an
	// unknown identifier: the owner's list is the only way to learn which
	// keys exist.
	second := seedAccount(t, pool, "ron")
	_, err = service.Renew(t.Context(), second, expired.Key.ID, newExpiry)
	assert.ErrorIs(t, err, ErrKeyNotFound)
	_, err = service.Renew(t.Context(), owner, uuid.Nil(), newExpiry)
	assert.ErrorIs(t, err, ErrKeyNotFound)

	assert.Equal(t, 1, auditCount(t, pool, audit.EventAPIKeyRenewed, expired.Key.ID.String()))
}

func TestRevokeIsSoftAndIdempotent(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	owner := seedAccount(t, pool, "hermione")

	key := created(t, service, owner, "short-lived")

	require.NoError(t, service.Revoke(t.Context(), owner, key.Key.ID))

	// The row survives the stamp: the view carries the instant, and the
	// credential opens nothing from now on.
	row, err := service.repo.GetKey(t.Context(), pool, key.Key.ID)
	require.NoError(t, err)
	require.NotNil(t, row.RevokedAt)
	_, err = service.Validate(t.Context(), key.Raw)
	assert.ErrorIs(t, err, ErrKeyNotFound)

	// Revoking a revoked key is the success it is: the caller's intent is
	// the state the key is in.
	assert.NoError(t, service.Revoke(t.Context(), owner, key.Key.ID))
	assert.Equal(t, 1, auditCount(t, pool, audit.EventAPIKeyRevoked, key.Key.ID.String()),
		"the second revocation records nothing, because nothing happened")

	// A key another account owns is not revocable by them.
	second := seedAccount(t, pool, "ron")
	other := created(t, service, second, "theirs")
	assert.ErrorIs(t, service.Revoke(t.Context(), owner, other.Key.ID), ErrKeyNotFound)
}

func TestListOwnScopesToTheOwnerAndListAllSeesEverything(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	first := seedAccount(t, pool, "hermione")
	second := seedAccount(t, pool, "ron")

	created(t, service, first, "first-a")
	created(t, service, first, "first-b")
	created(t, service, second, "second-a")

	// The owner's list is the owner's: another account's keys are not in
	// it, and the count agrees.
	own, pagination, err := service.ListOwn(t.Context(), first, "", false, 1, 10)
	require.NoError(t, err)
	require.Len(t, own, 2)
	assert.Equal(t, 2, *pagination.TotalItems)
	for _, row := range own {
		assert.Equal(t, first, row.UserID)
	}

	// The administrative view is every key, and it is a page like any
	// other.
	all, pagination, err := service.ListAll(t.Context(), "", false, 1, 10)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, 3, *pagination.TotalItems)
}

func TestExpiryWindowAnswersTheUnreminded(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	owner := seedAccount(t, pool, "hermione")

	// The pass reads from a moment forty days out: one key expires inside
	// the seven days that follow it, one beyond the window, one already
	// expired and therefore beyond reminding. The keys are written with
	// future windows — the database's own check refuses an expiry in the
	// past — and the pass's clock is what puts them in and out of the
	// window.
	soon, err := service.Create(t.Context(), owner, CreateParams{
		Name:      "soon",
		ExpiresAt: time.Now().Add(45 * 24 * time.Hour),
	})
	require.NoError(t, err)
	far := created(t, service, owner, "far")
	expired := created(t, service, owner, "expired")
	_ = expired
	_, err = pool.Exec(t.Context(), "UPDATE public.api_keys SET expires_at = $1 WHERE id = $2",
		time.Now().Add(30*24*time.Hour), far.Key.ID)
	require.NoError(t, err)

	pass := time.Now().Add(40 * 24 * time.Hour)
	keys, err := service.repo.ListExpiring(t.Context(), pool, pass, pass.Add(7*24*time.Hour))
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, soon.Key.ID, keys[0].Key.ID)
	assert.Equal(t, "hermione@example.com", keys[0].OwnerEmail)
	assert.Equal(t, "Hermione Granger", keys[0].OwnerName)

	// The mark is what keeps the next pass from repeating the reminder,
	// and it is written once: a second mark answers false, the way the
	// reminder itself is answered once.
	marked, err := service.repo.MarkEmailSent(t.Context(), pool, soon.Key.ID, pass)
	require.NoError(t, err)
	assert.True(t, marked)
	marked, err = service.repo.MarkEmailSent(t.Context(), pool, soon.Key.ID, pass)
	require.NoError(t, err)
	assert.False(t, marked)

	keys, err = service.repo.ListExpiring(t.Context(), pool, pass, pass.Add(7*24*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, keys)

	// The far key stays beyond the window, and the record of the creation
	// is the only audit row the fixture keys carry.
	assert.Equal(t, 1, auditCount(t, pool, audit.EventAPIKeyCreated, far.Key.ID.String()))
}

// auditCount reads how many records the log holds of one event about one key.
func auditCount(t *testing.T, pool *datastore.Postgres, event, keyID string) int {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From("public.audit_logs")
	sb.Where(sb.Equal("event", event), sb.Equal("resource_id", keyID))

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}
