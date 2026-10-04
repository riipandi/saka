package oauthsso

import (
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// linkedAccountFixture is the binding two of the tests share: one
// account, one connection, one binding row, and the flow token the
// unlink answers by.
type linkedAccountFixture struct {
	flow    Flow
	userID  uuid.UUID
	binding LinkedAccountView
}

func seededBinding(t *testing.T, service *Service, provider string) linkedAccountFixture {
	t.Helper()
	pool := service.pool
	flow, _ := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-" + provider,
		Email:             "grint@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Weasley",
		FamilyName:        "Grint",
	})
	userID := seedAccount(t, pool, "grint@hogwarts.example", true)
	mustExec(t, pool,
		`INSERT INTO public.oauth_linked_accounts (user_id, connection_id, provider_account_id, email, email_verified)
		 VALUES ($1, $2, $3, 'grint@hogwarts.example', true)`,
		userID, flow.ConnectionID, "prov-"+provider)

	binding, err := service.repo.LinkedAccountByID(t.Context(), pool, func() uuid.UUID {
		var id uuid.UUID
		require.NoError(t, pool.QueryRow(t.Context(),
			`SELECT id FROM public.oauth_linked_accounts WHERE user_id = $1`, userID).Scan(&id))
		return id
	}())
	require.NoError(t, err)
	return linkedAccountFixture{flow: flow, userID: userID, binding: binding}
}

func TestListLinkedAnswersOnlyTheCallerRowsOldestFirst(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	fixture := seededBinding(t, service, "hogwarts")

	// A second binding, newer: the listing must order by the bind time.
	mustExec(t, pool,
		`INSERT INTO public.oauth_linked_accounts (user_id, connection_id, provider_account_id, email, email_verified, created_at)
		 VALUES ($1, $2, 'prov-hogwarts-2', 'grint@hogwarts.example', true, now() + interval '1 hour')`,
		fixture.userID, fixture.flow.ConnectionID)

	// A foreign binding on a foreign account: never the caller's rows.
	foreign := seedAccount(t, pool, "flitwick@hogwarts.example", false)
	mustExec(t, pool,
		`INSERT INTO public.oauth_linked_accounts (user_id, connection_id, provider_account_id, email, email_verified)
		 VALUES ($1, $2, 'prov-foreign', 'flitwick@hogwarts.example', true)`,
		foreign, fixture.flow.ConnectionID)

	linked, err := service.ListLinkedAccounts(t.Context(), fixture.userID)
	require.NoError(t, err)
	require.Len(t, linked, 2)
	assert.Equal(t, "grint@hogwarts.example", linked[0].LinkedAccount.Email)
	assert.Equal(t, "hogwarts-sso", linked[0].Provider)
	assert.Equal(t, "prov-hogwarts-2", linked[1].LinkedAccount.ProviderAccountID)
}

func TestUnlinkRemovesTheBindingAndRecordsTheChange(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	fixture := seededBinding(t, service, "hogwarts")

	// The account keeps another way in: the password row the fixture
	// seeded.
	require.NoError(t, service.UnlinkLinkedAccount(t.Context(), fixture.userID, fixture.binding.LinkedAccount.ID))

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth_linked_accounts WHERE id = $1`,
		fixture.binding.LinkedAccount.ID).Scan(&count))
	assert.Zero(t, count, "the binding left the table")

	var records int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = 'oauthsso_account_unlinked'`).Scan(&records))
	assert.Equal(t, 1, records)

	// A second unlink of the same binding answers not-found — the row
	// is gone, and the audit trail never counts it twice.
	err := service.UnlinkLinkedAccount(t.Context(), fixture.userID, fixture.binding.LinkedAccount.ID)
	assert.ErrorIs(t, err, ErrLinkedAccountNotFound)
}

func TestUnlinkRefusesTheLastCredentialOfAPasswordlessAccount(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	// A passwordless account: the account seeded without a password
	// row, holding exactly one binding and no passkeys.
	fixture := seededBinding(t, service, "hogwarts")
	mustExec(t, pool, `DELETE FROM public.user_passwords WHERE user_id = $1`, fixture.userID)

	err := service.UnlinkLinkedAccount(t.Context(), fixture.userID, fixture.binding.LinkedAccount.ID)
	assert.ErrorIs(t, err, ErrLastCredential)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth_linked_accounts WHERE id = $1`,
		fixture.binding.LinkedAccount.ID).Scan(&count))
	assert.Equal(t, 1, count, "the refused unlink leaves the binding")

	var records int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = 'oauthsso_account_unlinked'`).Scan(&records))
	assert.Zero(t, records, "a refused unlink records nothing")
}

func TestUnlinkCountsAPasskeyAsAWayBackIn(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	fixture := seededBinding(t, service, "hogwarts")
	mustExec(t, pool, `DELETE FROM public.user_passwords WHERE user_id = $1`, fixture.userID)

	// A passkey is a credential the stranding rule keeps: the last
	// binding may go, the account still signs in.
	mustExec(t, pool,
		`INSERT INTO public.webauthn_credentials (id, user_id, name, credential_id, public_key, attestation_type, transport, sign_count)
		 VALUES (uuidv7(), $1, 'Gringotts key', '\x77616e64', '\x77616e64', 'none', '["usb"]'::jsonb, 0)`,
		fixture.userID)

	require.NoError(t, service.UnlinkLinkedAccount(t.Context(), fixture.userID, fixture.binding.LinkedAccount.ID))
}

func TestUnlinkCountsASecondBindingAsAWayBackIn(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	fixture := seededBinding(t, service, "hogwarts")
	mustExec(t, pool, `DELETE FROM public.user_passwords WHERE user_id = $1`, fixture.userID)

	// The second binding, not the password, is the way back in.
	mustExec(t, pool,
		`INSERT INTO public.oauth_linked_accounts (user_id, connection_id, provider_account_id, email, email_verified)
		 VALUES ($1, $2, 'prov-hogwarts-2', 'grint@hogwarts.example', true)`,
		fixture.userID, fixture.flow.ConnectionID)

	require.NoError(t, service.UnlinkLinkedAccount(t.Context(), fixture.userID, fixture.binding.LinkedAccount.ID))
}

func TestUnlinkRefusesAForeignBindingWithANotFound(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	fixture := seededBinding(t, service, "hogwarts")
	foreign := seedAccount(t, pool, "flitwick@hogwarts.example", false)

	err := service.UnlinkLinkedAccount(t.Context(), foreign, fixture.binding.LinkedAccount.ID)
	assert.ErrorIs(t, err, ErrLinkedAccountNotFound)
}

func TestTheHoldersBindingAnswersItsOpenedTokens(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	fixture := seededBinding(t, service, "hogwarts")

	// The tokens rest sealed on the row, exactly the shape the bind
	// wrote: an expiry the provider named rides beside them.
	sealedAccess, err := service.seal("provider-access-token")
	require.NoError(t, err)
	sealedRefresh, err := service.seal("provider-refresh-token")
	require.NoError(t, err)
	expires := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	mustExec(t, pool,
		`UPDATE public.oauth_linked_accounts
		 SET access_token = $1, refresh_token = $2, access_expires_at = $3
		 WHERE id = $4`,
		sealedAccess, sealedRefresh, expires, fixture.binding.LinkedAccount.ID)

	tokens, err := service.GetLinkedAccountTokens(t.Context(), fixture.userID, fixture.binding.LinkedAccount.ID)
	require.NoError(t, err)
	assert.Equal(t, "provider-access-token", tokens.AccessToken)
	assert.Equal(t, "provider-refresh-token", tokens.RefreshToken)
	require.NotNil(t, tokens.ExpiresAt)
	assert.True(t, expires.Equal(*tokens.ExpiresAt))
	assert.Equal(t, fixture.binding.LinkedAccount.ConnectionID, tokens.ConnectionID)
	assert.Equal(t, fixture.binding.Provider, tokens.Provider)
}

func TestAForeignBindingAnswersNotFoundOnTheTokenRead(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	fixture := seededBinding(t, service, "hogwarts")
	foreign := seedAccount(t, pool, "flitwick@hogwarts.example", false)

	_, err := service.GetLinkedAccountTokens(t.Context(), foreign, fixture.binding.LinkedAccount.ID)
	assert.ErrorIs(t, err, ErrLinkedAccountNotFound)

	_, err = service.GetLinkedAccountTokens(t.Context(), fixture.userID, uuid.New())
	assert.ErrorIs(t, err, ErrLinkedAccountNotFound)
}

func TestTheTokenlessBindingAnswersEmptyFields(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	fixture := seededBinding(t, service, "hogwarts")

	// The provider answered no tokens for the scopes asked: the empty
	// columns are the fact the row stores, and the read answers them as
	// they lie rather than refusing.
	tokens, err := service.GetLinkedAccountTokens(t.Context(), fixture.userID, fixture.binding.LinkedAccount.ID)
	require.NoError(t, err)
	assert.Empty(t, tokens.AccessToken)
	assert.Empty(t, tokens.RefreshToken)
	assert.Nil(t, tokens.ExpiresAt)
}
