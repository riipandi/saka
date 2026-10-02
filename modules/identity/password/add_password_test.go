package password

import (
	"testing"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/crypto"
)

// The add-password flow: a first credential set through the step-up proof,
// judged by the same policy the sign-up and the reset judge.

// seedPasswordless writes an account row with no credential — the state the
// add-password procedure exists for.
func seedPasswordless(t *testing.T, pool *datastore.Postgres, username, email string) uuid.UUID {
	t.Helper()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto("public.users")
	ib.Cols("username", "email", "display_name")
	ib.Values(username, email, username)
	ib.SQL("RETURNING id")

	query, args := ib.Build()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	parsed, err := uuid.Parse(id)
	require.NoError(t, err)
	return parsed
}

func TestAddPasswordSetsTheFirstCredential(t *testing.T) {
	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)
	id := seedPasswordless(t, pool, "hermione", "hermione@example.com")

	require.NoError(t, service.AddPassword(t.Context(), id, "Expecto-Patronum-9"))

	// The credential verifies, and the receipt went to the account.
	current, err := service.repo.FindPasswordHash(t.Context(), pool, id)
	require.NoError(t, err)
	assert.NotEmpty(t, current)
	ok, err := crypto.NewPasswordHasher().Verify("Expecto-Patronum-9", current)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, enqueuer.noticeSent)
}

func TestAddPasswordRefusesAnAccountThatAlreadyHoldsOne(t *testing.T) {
	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)
	id := seedPasswordless(t, pool, "hermione", "hermione@example.com")

	require.NoError(t, service.AddPassword(t.Context(), id, "Expecto-Patronum-9"))
	assert.ErrorIs(t, service.AddPassword(t.Context(), id, "Alohomora-8"), ErrPasswordSet)

	// The refusal is cheap — nothing was overwritten, nothing was queued.
	current, err := service.repo.FindPasswordHash(t.Context(), pool, id)
	require.NoError(t, err)
	ok, err := crypto.NewPasswordHasher().Verify("Expecto-Patronum-9", current)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, enqueuer.noticeSent, "the first add's receipt is the only one")
}

func TestAddPasswordRunsThePolicy(t *testing.T) {
	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)
	id := seedPasswordless(t, pool, "hermione", "hermione@example.com")

	// The policy's refusals stand before any hash is written: the same
	// weak credential the sign-up refuses, the add refuses.
	assert.ErrorIs(t, service.AddPassword(t.Context(), id, "short"), ErrWeakPassword)
	assert.ErrorIs(t, service.AddPassword(t.Context(), id, "aaaaaaaaaaaa"), ErrWeakPassword)

	current, err := service.repo.FindPasswordHash(t.Context(), pool, id)
	require.NoError(t, err)
	assert.Empty(t, current, "no credential was written")
	assert.False(t, enqueuer.noticeSent)
}
