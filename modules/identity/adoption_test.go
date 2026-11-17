package identity

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"uuid"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/modules/identity/multifactor"
	"github.com/riipandi/saka/modules/identity/password"
	"github.com/riipandi/saka/modules/identity/session"
	"github.com/riipandi/saka/modules/identity/verification"
)

func adoptionUnderTest(pool *datastore.Postgres) accountAdoption {
	log := slog.New(slog.DiscardHandler)
	recorder := fwaudit.NewRecorder(log)
	return accountAdoption{
		verification: verification.NewService(pool, nil, nil, recorder, "https://app.example", log),
		passwords:    password.NewService(pool, nil, recorder, "https://app.example", log),
		factors:      multifactor.NewService(pool, nil, nil, recorder, "Saka", log),
		sessions:     session.NewService(pool, nil, nil, recorder, log),
	}
}

func seedSquattedAccount(t *testing.T, pool *datastore.Postgres, email string, verified bool) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	verifiedAt := "NULL"
	if verified {
		verifiedAt = "now()"
	}
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO public.users (username, email, display_name, email_verified_at)
		 VALUES ('squat' || substr(md5($1), 1, 6), $1, 'Squatter', `+verifiedAt+`) RETURNING id`, email).Scan(&id))
	for _, statement := range []string{
		`INSERT INTO public.user_passwords (user_id, password_hash) VALUES ($1, 'attacker-hash')`,
		`INSERT INTO public.auth_tokens (user_id, token_hash, purpose, expires_at) VALUES ($1, 'reset-hash', 'password_reset', now() + interval '1 hour')`,
		`INSERT INTO public.mfa_totp (user_id, name, secret, confirmed_at) VALUES ($1, 'phone', 'enc:x', now())`,
		`INSERT INTO public.mfa_recovery_codes (user_id, code_hash) VALUES ($1, 'recovery-hash')`,
		`INSERT INTO public.mfa_pending (user_id, token_hash, expires_at) VALUES ($1, 'bridge-hash', now() + interval '1 hour')`,
	} {
		_, err := pool.Exec(ctx, statement, id)
		require.NoError(t, err, statement)
	}
	return id
}

func held(t *testing.T, pool *datastore.Postgres, id uuid.UUID) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for table, column := range map[string]string{
		"user_passwords": "user_id", "auth_tokens": "user_id", "mfa_totp": "user_id",
		"mfa_recovery_codes": "user_id", "mfa_pending": "user_id",
	} {
		var n int
		require.NoError(t, pool.QueryRow(t.Context(),
			`SELECT count(*) FROM public.`+table+` WHERE `+column+` = $1`, id).Scan(&n))
		counts[table] = n
	}
	return counts
}

func TestAdoptingAnUnverifiedAccountStripsWhatTheSquatterSet(t *testing.T) {
	pool := testutils.MigratedPostgres(t, "identity_adoption_test")
	adoption := adoptionUnderTest(pool)
	id := seedSquattedAccount(t, pool, "victim@example.com", false)

	err := pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		adopted, adoptErr := adoption.AdoptUnverified(ctx, tx, id)
		require.True(t, adopted)
		return adoptErr
	})
	require.NoError(t, err)

	for table, n := range held(t, pool, id) {
		assert.Zero(t, n, "%s is cleared", table)
	}
	var verified bool
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT email_verified_at IS NOT NULL FROM public.users WHERE id = $1`, id).Scan(&verified))
	assert.True(t, verified, "the provider's claim is the proof")
}

func TestAdoptingAVerifiedAccountChangesNothing(t *testing.T) {
	pool := testutils.MigratedPostgres(t, "identity_adoption_test")
	adoption := adoptionUnderTest(pool)
	id := seedSquattedAccount(t, pool, "owner@example.com", true)

	err := pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		adopted, adoptErr := adoption.AdoptUnverified(ctx, tx, id)
		assert.False(t, adopted, "an account whose holder already proved the address is not adopted")
		return adoptErr
	})
	require.NoError(t, err)

	for table, n := range held(t, pool, id) {
		assert.Equal(t, 1, n, "%s is untouched", table)
	}
}
