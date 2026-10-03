package oauthsso

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHoldsAlternativeCredentialAnswersThePasskeyRoll(t *testing.T) {
	pool := migratedPool(t)
	repo := NewRepository(pool)
	userID := seedAccount(t, pool, "langdon@hogwarts.example", true)

	// No binding and no passkey: the password is the only way in.
	kept, err := repo.HoldsAlternativeCredential(t.Context(), pool, userID)
	require.NoError(t, err)
	assert.False(t, kept)

	mustExec(t, pool,
		`INSERT INTO public.webauthn_credentials (user_id, name, credential_id, public_key, attestation_type)
		 VALUES ($1, 'Sophie''s Laptop', 'sophies-laptop', 'key', 'none')`, userID)

	kept, err = repo.HoldsAlternativeCredential(t.Context(), pool, userID)
	require.NoError(t, err)
	assert.True(t, kept)
}

func TestHoldsAlternativeCredentialAnswersTheBindings(t *testing.T) {
	pool := migratedPool(t)
	repo := NewRepository(pool)
	userID := seedAccount(t, pool, "neveu@hogwarts.example", true)
	mustExec(t, pool,
		`INSERT INTO public.oauth_connections (kind, provider, display_name, client_id, client_secret, enabled)
		 VALUES ('builtin', 'github', 'GitHub', 'client', 'enc:sealed', true)`)
	mustExec(t, pool,
		`INSERT INTO public.oauth_linked_accounts (user_id, connection_id, provider_account_id, email)
		 VALUES ($1, (SELECT id FROM public.oauth_connections WHERE provider = 'github'), 'provider-1', 'neveu@hogwarts.example')`, userID)

	kept, err := repo.HoldsAlternativeCredential(t.Context(), pool, userID)
	require.NoError(t, err)
	assert.True(t, kept)
}
