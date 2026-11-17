package oidc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Refresh rotation: the retired token's pointer stays as a tombstone, a
// replay of it ends the grant, and a request that loses the compare-and-swap
// retires nothing.

// rotationContext is one token request's context: the snapshot cache and the
// rotation record the token route's middleware plants.
func rotationContext(t *testing.T) context.Context {
	t.Helper()
	ctx := withGrantSnapshotCache(t.Context())
	return context.WithValue(ctx, refreshRotationsKey{}, &refreshRotations{})
}

func seedRotatingGrant(t *testing.T) (grantStore, *goidc.Grant) {
	t.Helper()
	pool := migratedPool(t)
	service := testService(t, pool)
	issued, err := service.Create(t.Context(), seedAccount(t, pool, "hermione"), createParams("Gryffindor"))
	require.NoError(t, err)

	store := grantStore{protocolStore: protocolStore{pool: pool}}
	grant := &goidc.Grant{
		ID:                    "grant-rot",
		CreatedAt:             int(time.Now().Unix()),
		Subject:               "sub",
		ClientID:              issued.Client.ID,
		Scopes:                "openid",
		RefreshToken:          "rt-1",
		RefreshTokenExpiresAt: 0,
	}
	require.NoError(t, store.SaveGrant(t.Context(), grant))
	return store, grant
}

// rotate performs one refresh the way the token endpoint does: load by the
// presented token, swap in the replacement, save.
func rotate(t *testing.T, store grantStore, presented, replacement string) error {
	t.Helper()
	ctx := rotationContext(t)
	grant, err := store.GrantByRefreshToken(ctx, presented)
	require.NoError(t, err)
	grant.RefreshToken = replacement
	return store.SaveGrant(ctx, grant)
}

func TestARetiredRefreshTokenIsATombstoneAndItsReplayEndsTheGrant(t *testing.T) {
	store, grant := seedRotatingGrant(t)

	require.NoError(t, rotate(t, store, "rt-1", "rt-2"))

	// The replacement redeems; the retired token's row is still there.
	_, err := store.GrantByRefreshToken(t.Context(), "rt-2")
	require.NoError(t, err)
	var retired int
	require.NoError(t, store.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth_sessions
		 WHERE kind = 'refresh' AND key = $1 AND request_data ->> 'retired' = 'true'`,
		hashToken("rt-1")).Scan(&retired))
	assert.Equal(t, 1, retired, "the rotation leaves the retired pointer as a tombstone")

	// Presenting the retired token is a replay: not found, and the whole
	// chain — the grant and the replacement token — goes with it.
	_, err = store.GrantByRefreshToken(t.Context(), "rt-1")
	require.ErrorIs(t, err, goidc.ErrNotFound)
	_, err = store.GrantByRefreshToken(t.Context(), "rt-2")
	require.ErrorIs(t, err, goidc.ErrNotFound, "the replacement died with the grant")
	_, err = store.Grant(t.Context(), grant.ID)
	require.ErrorIs(t, err, goidc.ErrNotFound)
}

func TestATombstoneLivesAtMostTheRetentionBound(t *testing.T) {
	store, _ := seedRotatingGrant(t)
	require.NoError(t, rotate(t, store, "rt-1", "rt-2"))

	// The grant's window is zero, the never-expiring token: the tombstone
	// must still be reapable.
	var withinBound bool
	require.NoError(t, store.pool.QueryRow(t.Context(),
		`SELECT expires_at IS NOT NULL AND expires_at <= now() + make_interval(secs => $2)
		 FROM public.oauth_sessions WHERE kind = 'refresh' AND key = $1`,
		hashToken("rt-1"), retiredPointerRetention.Seconds()).Scan(&withinBound))
	assert.True(t, withinBound)
}

func TestARequestThatLosesTheSwapRetiresNothing(t *testing.T) {
	store, grant := seedRotatingGrant(t)

	// Two requests present the same token and both read the grant.
	first, second := rotationContext(t), rotationContext(t)
	a, err := store.GrantByRefreshToken(first, "rt-1")
	require.NoError(t, err)
	b, err := store.GrantByRefreshToken(second, "rt-1")
	require.NoError(t, err)

	a.RefreshToken = "rt-2"
	require.NoError(t, store.SaveGrant(first, a))
	b.RefreshToken = "rt-3"
	err = store.SaveGrant(second, b)
	var oidcErr goidc.Error
	require.ErrorAs(t, err, &oidcErr)
	assert.Equal(t, goidc.ErrorCodeInvalidGrant, oidcErr.Code)

	_, err = store.GrantByRefreshToken(t.Context(), "rt-3")
	require.ErrorIs(t, err, goidc.ErrNotFound, "the loser's replacement was never issued")
	live, err := store.GrantByRefreshToken(t.Context(), "rt-2")
	require.NoError(t, err, "the winner's token still redeems")
	assert.Equal(t, grant.ID, live.ID)
}

// TestTheGrantPointerIndexServesTheChainDelete seeds a population the size a
// deployment accumulates and asks the planner how the chain delete runs:
// without the partial index it is a scan of every client's protocol state.
func TestTheGrantPointerIndexServesTheChainDelete(t *testing.T) {
	pool := migratedPool(t)
	_, err := pool.Exec(t.Context(),
		`INSERT INTO public.oauth_sessions (kind, key, request_data)
		 SELECT 'refresh', md5(g::text), jsonb_build_object('grant_id', 'g' || g)
		 FROM generate_series(1, 50000) g`)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `ANALYZE public.oauth_sessions`)
	require.NoError(t, err)

	rows, err := pool.Query(t.Context(),
		`EXPLAIN DELETE FROM public.oauth_sessions
		 WHERE kind IN ('authcode', 'refresh') AND request_data ->> 'grant_id' = 'g777'`)
	require.NoError(t, err)
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line + "\n")
	}
	assert.Contains(t, plan.String(), "idx_oauth_sessions_grant_pointer", plan.String())
}
