package oauthsso

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
)

// recordingBans is the restrictions write the offboard tests drive: it
// records the ban the pass asked for rather than writing the row — the
// row's shape is the restrictions feature's own contract, judged there.
type recordingBans struct {
	calls []string
}

func (b *recordingBans) ApplyBanAndLift(_ context.Context, _ datastore.Querier, userID uuid.UUID,
	reason string, expiresAt *time.Time, _ *uuid.UUID, _ time.Time) error {
	b.calls = append(b.calls, fmt.Sprintf("%s|%s|%v", userID, reason, expiresAt == nil))
	return nil
}

// recordingEnder is the session lifecycle's offboard half.
type recordingEnder struct {
	users []uuid.UUID
}

func (e *recordingEnder) RevokeAllForUser(_ context.Context, _ datastore.Querier, userID uuid.UUID) (int, error) {
	e.users = append(e.users, userID)
	return 3, nil
}

// offboardingFixture builds one custom connection and one refresh-tokened
// binding for an account the fixture seeds, answering both.
type offboardingFixture struct {
	service   *Service
	userID    uuid.UUID
	bindingID uuid.UUID
	provider  string
}

func offboardingFixtureFor(t *testing.T, service *Service, email, providerAccountID string) offboardingFixture {
	t.Helper()
	pool := service.pool
	// One connection per provider slug — the fixtures of one test share
	// the connection the first built.
	created, err := service.ByProvider(t.Context(), "hogwarts-sso")
	if errors.Is(err, ErrConnectionNotFound) {
		params := customParams()
		params.Enabled = true
		created, err = service.Create(t.Context(), params)
	}
	require.NoError(t, err)
	userID := seedAccount(t, pool, email, true)
	mustExec(t, pool,
		`INSERT INTO public.oauth_accounts (user_id, connection_id, provider_account_id, email, email_verified, access_token, refresh_token, access_expires_at)
		 VALUES ($1, $2, $3, $4, true, $5, $6, now() - interval '1 hour')`,
		userID, created.ID, providerAccountID, email,
		mustSeal(t, service, "stale-access"), mustSeal(t, service, "live-refresh"))
	return offboardingFixture{service: service, userID: userID, bindingID: mustBindingID(t, pool, userID), provider: created.Provider}
}

func mustSeal(t *testing.T, service *Service, secret string) string {
	t.Helper()
	sealed, err := service.seal(secret)
	require.NoError(t, err)
	return sealed
}

func mustBindingID(t *testing.T, pool *datastore.Postgres, userID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT id FROM public.oauth_accounts WHERE user_id = $1`, userID).Scan(&id))
	return id
}

func offboardingService(t *testing.T, pool *datastore.Postgres, poster *recordingPoster,
	bans *recordingBans, ender *recordingEnder) *Service {
	t.Helper()
	service := resolutionService(t, pool, func(s *Service) { s.WithTokenPoster(poster) })
	service.WithBanEnforcer(bans)
	service.WithSessionEnder(ender)
	service.providers.Custom = &fakeProvider{endpoint: "https://sso.hogwarts.example/token"}
	return service
}

func TestTheDeadIdentityOffboardsItsAccount(t *testing.T) {
	pool := migratedPool(t)
	bans, ender := &recordingBans{}, &recordingEnder{}
	poster := &recordingPoster{status: 400, payload: grantJSON("", "", 0, "invalid_grant")}
	service := offboardingService(t, pool, poster, bans, ender)
	fixture := offboardingFixtureFor(t, service, "grint@hogwarts.example", "prov-dead")

	outcome, err := service.OffboardPass(t.Context(), 100)
	require.NoError(t, err)
	assert.Equal(t, 1, outcome.Probed)
	assert.Equal(t, 1, outcome.Offboarded)
	assert.Equal(t, 0, outcome.Rotated)

	// The ban is permanent — no expiry — and the reason names the
	// provider whose grant refused the holder.
	require.Len(t, bans.calls, 1)
	assert.Contains(t, bans.calls[0], fixture.userID.String())
	assert.Contains(t, bans.calls[0], fixture.provider)
	assert.Contains(t, bans.calls[0], "true", "the ban carries no expiry")
	require.Len(t, ender.users, 1)
	assert.Equal(t, fixture.userID, ender.users[0])

	// The audit record is the existing user_banned event with the
	// offboarding reason — no new vocabulary.
	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = 'user_banned' AND user_id = $1`,
		fixture.userID).Scan(&count))
	assert.Equal(t, 1, count)

	// The binding row stays: the ban, not the unlink, is the offboard.
	var bindings int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth_accounts WHERE user_id = $1`, fixture.userID).Scan(&bindings))
	assert.Equal(t, 1, bindings)
}

func TestTheLiveIdentityRotatesAndSurvives(t *testing.T) {
	pool := migratedPool(t)
	bans, ender := &recordingBans{}, &recordingEnder{}
	poster := &recordingPoster{status: 200, payload: grantJSON("fresh-access", "rotated-refresh", 3600, "")}
	service := offboardingService(t, pool, poster, bans, ender)
	fixture := offboardingFixtureFor(t, service, "grint@hogwarts.example", "prov-live")

	outcome, err := service.OffboardPass(t.Context(), 100)
	require.NoError(t, err)
	assert.Equal(t, 1, outcome.Probed)
	assert.Equal(t, 1, outcome.Rotated)
	assert.Equal(t, 0, outcome.Offboarded)
	assert.Empty(t, bans.calls, "a survivor is never banned")

	var storedAccess string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT access_token FROM public.oauth_accounts WHERE id = $1`,
		fixture.bindingID).Scan(&storedAccess))
	assert.Equal(t, "fresh-access", mustOpen(t, service, storedAccess))
}

func TestTheTokenlessBindingIsUntouched(t *testing.T) {
	pool := migratedPool(t)
	bans, ender := &recordingBans{}, &recordingEnder{}
	poster := &recordingPoster{status: 200, payload: grantJSON("fresh", "", 3600, "")}
	service := offboardingService(t, pool, poster, bans, ender)
	// The fixture's default shape: the provider answered no tokens, the
	// binding carries none, and the pass cannot judge it.
	seededBinding(t, service, "hogwarts")

	outcome, err := service.OffboardPass(t.Context(), 100)
	require.NoError(t, err)
	assert.Equal(t, 0, outcome.Probed)
	assert.Empty(t, bans.calls)
	assert.Empty(t, ender.users)
}

func TestThePassIsBounded(t *testing.T) {
	pool := migratedPool(t)
	bans, ender := &recordingBans{}, &recordingEnder{}
	poster := &recordingPoster{status: 200, payload: grantJSON("fresh", "", 3600, "")}
	service := offboardingService(t, pool, poster, bans, ender)
	for i := range 3 {
		offboardingFixtureFor(t, service,
			fmt.Sprintf("student%d@hogwarts.example", i), fmt.Sprintf("prov-%d", i))
	}

	outcome, err := service.OffboardPass(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, 2, outcome.Probed, "the batch bounds the pass; the rest waits for the next run")
}

func TestTheBannedHolderIsNotProbedAgain(t *testing.T) {
	pool := migratedPool(t)
	bans, ender := &recordingBans{}, &recordingEnder{}
	poster := &recordingPoster{status: 400, payload: grantJSON("", "", 0, "invalid_grant")}
	service := offboardingService(t, pool, poster, bans, ender)
	fixture := offboardingFixtureFor(t, service, "grint@hogwarts.example", "prov-dead")

	// An open ban row — the pass's own earlier hour, or an operator's —
	// takes the holder out of the pass's candidates: a re-probe would
	// earn a fresh ban row and a fresh audit record every hour.
	mustExec(t, pool,
		`INSERT INTO public.account_restrictions (user_id, kind, reason, started_at)
		 VALUES ($1, 'ban', 'offboarded already', now())`, fixture.userID)

	outcome, err := service.OffboardPass(t.Context(), 100)
	require.NoError(t, err)
	assert.Equal(t, 0, outcome.Probed)
	assert.Empty(t, bans.calls)
}

func TestTheGrantTheProbePresentsCarriesTheBinding(t *testing.T) {
	pool := migratedPool(t)
	bans, ender := &recordingBans{}, &recordingEnder{}
	poster := &recordingPoster{status: 200, payload: grantJSON("fresh", "", 3600, "")}
	service := offboardingService(t, pool, poster, bans, ender)
	offboardingFixtureFor(t, service, "grint@hogwarts.example", "prov-live")

	_, err := service.OffboardPass(t.Context(), 100)
	require.NoError(t, err)
	require.Len(t, poster.forms, 1)
	form := poster.forms[0]
	assert.Equal(t, "refresh_token", form.Get("grant_type"))
	assert.Equal(t, "live-refresh", form.Get("refresh_token"))
	assert.NotEmpty(t, form.Get("client_id"))
	assert.NotEmpty(t, form.Get("client_secret"))
}
