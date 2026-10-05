package customclaim

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/modules/identity/usergroup"

	"uuid"
)

// migratedPool opens a database the migrations have built, so the claim
// table exists.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "federation_customclaim_test")
}

// testService builds the service with a recorder that writes for real — a
// record is part of the transaction it describes, so the assertions read
// the table the writer fills.
func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	return NewService(pool, fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
}

// seedSubject inserts an account and a group and answers their wire ids —
// the forms the claim requests carry.
func seedSubject(t *testing.T, pool *datastore.Postgres) (string, string) {
	t.Helper()

	var userID uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), `
		INSERT INTO public.users (username, email, first_name, last_name, display_name)
		VALUES ('hermione', 'hermione@example.com', 'Hermione', 'Granger', 'Hermione Granger')
		RETURNING id`).Scan(&userID))

	var groupID uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), `
		INSERT INTO public.user_groups (name, display_name)
		VALUES ('gryffindor', 'Gryffindor') RETURNING id`).Scan(&groupID))

	return user.FormatID(userID), usergroup.FormatID(groupID)
}

// TestCreateListAndSuggestCoverTheLifecycle covers the core: a claim hangs
// on its subject, the list answers it, and the suggestions count its key's
// use.
func TestCreateListAndSuggestCoverTheLifecycle(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userWire, groupWire := seedSubject(t, pool)

	created, err := service.CreateByUser(t.Context(), userWire, "house", "gryffindor")
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "house", created.Key)
	assert.Equal(t, "gryffindor", created.Value)

	// A duplicate key on the same subject is the unique index's answer.
	_, err = service.CreateByUser(t.Context(), userWire, "house", "ravenclaw")
	assert.ErrorIs(t, err, ErrClaimExists)

	// The same key on another subject is fine.
	grouped, err := service.CreateByGroup(t.Context(), groupWire, "house", "gryffindor")
	require.NoError(t, err)
	assert.Equal(t, "house", grouped.Key)

	listed, err := service.ListByUser(t.Context(), userWire)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	keys, err := service.Suggest(t.Context())
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, int64(2), keys[0].UsageCount, "the usage count is the count across subjects")

	// An unknown subject refuses the creation.
	_, err = service.CreateByUser(t.Context(), "usr_00000000000000000000000000", "house", "x")
	assert.ErrorIs(t, err, ErrSubjectNotFound)
	_, err = service.CreateByGroup(t.Context(), "ugrp_00000000000000000000000000", "house", "x")
	assert.ErrorIs(t, err, ErrSubjectNotFound)
}

// TestUpdateAndDeleteRefuseTheOtherSubjectKind covers the boundary: the
// user surface refuses a group claim and the group surface refuses a user
// claim, so a client cannot silently move a claim between subjects by
// pointing the other surface at it.
func TestUpdateAndDeleteRefuseTheOtherSubjectKind(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userWire, groupWire := seedSubject(t, pool)

	userClaim, err := service.CreateByUser(t.Context(), userWire, "house", "gryffindor")
	require.NoError(t, err)
	groupClaim, err := service.CreateByGroup(t.Context(), groupWire, "year", "1991")
	require.NoError(t, err)

	_, err = service.UpdateByGroup(t.Context(), userClaim.ID, "house", "ravenclaw")
	assert.ErrorIs(t, err, ErrClaimWrongSubject)
	_, err = service.UpdateByUser(t.Context(), groupClaim.ID, "year", "1992")
	assert.ErrorIs(t, err, ErrClaimWrongSubject)

	err = service.DeleteByGroup(t.Context(), userClaim.ID)
	assert.ErrorIs(t, err, ErrClaimWrongSubject)
	err = service.DeleteByUser(t.Context(), groupClaim.ID)
	assert.ErrorIs(t, err, ErrClaimWrongSubject)

	// The right surface rewrites and removes.
	updated, err := service.UpdateByUser(t.Context(), userClaim.ID, "house", "ravenclaw")
	require.NoError(t, err)
	assert.Equal(t, "ravenclaw", updated.Value)

	require.NoError(t, service.DeleteByUser(t.Context(), userClaim.ID))
	listed, err := service.ListByUser(t.Context(), userWire)
	require.NoError(t, err)
	assert.Empty(t, listed)

	err = service.DeleteByUser(t.Context(), userClaim.ID)
	assert.ErrorIs(t, err, ErrClaimNotFound)
}

// TestTheAuditRecordsRideTheChangeTransactions reads the table the recorder
// fills, so the record's presence is what is asserted.
func TestTheAuditRecordsRideTheChangeTransactions(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userWire, _ := seedSubject(t, pool)

	created, err := service.CreateByUser(t.Context(), userWire, "house", "gryffindor")
	require.NoError(t, err)
	assertEvents(t, pool, audit.EventCustomClaimCreated, 1)

	_, err = service.UpdateByUser(t.Context(), created.ID, "house", "ravenclaw")
	require.NoError(t, err)
	assertEvents(t, pool, audit.EventCustomClaimUpdated, 1)

	require.NoError(t, service.DeleteByUser(t.Context(), created.ID))
	assertEvents(t, pool, audit.EventCustomClaimDeleted, 1)
}

// assertEvents counts the records one event has in the table.
func assertEvents(t *testing.T, pool *datastore.Postgres, event string, want int) {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = $1`, event).Scan(&count))
	assert.Equal(t, want, count, "the %s record rides the change that caused it", event)
}
