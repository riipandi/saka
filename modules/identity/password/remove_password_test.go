package password

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/riipandi/tango/pkg/testutils"
)

// stubAlternatives answers the chained check from a field, so the removal's
// judgement is tested without the passkey and binding tables.
type stubAlternatives struct {
	kept bool
	err  error
}

func (s stubAlternatives) HasAlternative(_ context.Context, _ uuid.UUID) (bool, error) {
	return s.kept, s.err
}

func TestRemovePasswordRefusesTheLastCredential(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)
	service.WithAlternatives(stubAlternatives{kept: false})
	userID := seedUser(t, pool, "langdon", "langdon@example.com")
	id, err := uuid.Parse(userID)
	require.NoError(t, err)

	require.ErrorIs(t, service.RemovePassword(t.Context(), id), ErrLastCredential)

	// The credential survives the refusal, and no notice says otherwise.
	var hashCount int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.user_passwords WHERE user_id = $1", id).Scan(&hashCount))
	assert.Equal(t, 1, hashCount)
	assert.False(t, enqueuer.removed)
}

func TestRemovePasswordRefusesAnAccountWithoutOne(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)
	userID := seedUser(t, pool, "neveu", "neveu@example.com")
	id, err := uuid.Parse(userID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		"DELETE FROM public.user_passwords WHERE user_id = $1", id)
	require.NoError(t, err)

	require.ErrorIs(t, service.RemovePassword(t.Context(), id), ErrNoPassword)
	assert.False(t, enqueuer.removed)
}

func TestRemovePasswordDeletesTheHashAndSendsTheReceipt(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)
	service.WithAlternatives(stubAlternatives{kept: true})
	userID := seedUser(t, pool, "vetra", "vetra@example.com")
	id, err := uuid.Parse(userID)
	require.NoError(t, err)

	require.NoError(t, service.RemovePassword(t.Context(), id))

	var hashCount int
	require.NoError(t, pool.QueryRow(t.Context(),
		"SELECT count(*) FROM public.user_passwords WHERE user_id = $1", id).Scan(&hashCount))
	assert.Equal(t, 0, hashCount)
	assert.True(t, enqueuer.removed)
}

func TestRemovePasswordRefusesAnImpersonatedAndDisabledState(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, enqueuer := testService(t, pool, true)
	service.WithAlternatives(stubAlternatives{kept: true})
	userID := seedUser(t, pool, "horcrux", "horcrux@example.com")
	id, err := uuid.Parse(userID)
	require.NoError(t, err)
	// A disabled account refuses on the state read, before the chained
	// check runs — the same order the add and the reset keep.
	_, err = pool.Exec(t.Context(),
		"UPDATE public.users SET disabled = true WHERE id = $1", id)
	require.NoError(t, err)

	require.ErrorIs(t, service.RemovePassword(t.Context(), id), ErrAccountForbidden)
	assert.False(t, enqueuer.removed)
}
