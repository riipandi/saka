package customclaim

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reserved-key rule: a create or update whose key collides with a
// claim the protocol mints itself is refused, an update onto a reserved
// key leaves the row unchanged, and a differently cased variant is not
// blocked — JSON keys are case-sensitive, so it cannot collide.

func TestReservedKeysAreRefusedOnCreateAndUpdate(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userWire, groupWire := seedSubject(t, pool)

	for _, key := range []string{"sub", "email", "groups", "saka:token_type"} {
		_, err := service.CreateByUser(t.Context(), userWire, key, "evil")
		assert.ErrorIs(t, err, ErrReservedClaim, "user create with %q", key)
		_, err = service.CreateByGroup(t.Context(), groupWire, key, "evil")
		assert.ErrorIs(t, err, ErrReservedClaim, "group create with %q", key)
	}

	// Nothing was written.
	views, err := service.ListByUser(t.Context(), userWire)
	require.NoError(t, err)
	assert.Empty(t, views)

	// An ordinary key still lands.
	created, err := service.CreateByUser(t.Context(), userWire, "house", "gryffindor")
	require.NoError(t, err)

	// Updating onto a reserved key is refused, and the row is unchanged.
	_, err = service.UpdateByUser(t.Context(), created.ID, "iss", "evil")
	assert.ErrorIs(t, err, ErrReservedClaim)

	views, err = service.ListByUser(t.Context(), userWire)
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, "house", views[0].Key)
}

func TestCasedVariantsOfReservedKeysAreNotBlocked(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userWire, _ := seedSubject(t, pool)

	_, err := service.CreateByUser(t.Context(), userWire, "Sub", "innocent")
	require.NoError(t, err, "a differently cased key cannot collide with a protected claim")
}
