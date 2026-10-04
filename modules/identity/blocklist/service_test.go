package blocklist

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect"
	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/testutils"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// migratedPool is the container the CRUD round trips run over: a database
// migrated to the head, one per test.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()
	return testutils.MigratedPostgres(t, "blocklist_test")
}

// testService is the service over the container, its clock still.
func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	service := NewService(pool, nil, nil)
	service.now = func() time.Time { return time.Unix(0, 0).UTC() }
	return service
}

// insertAdmin creates the account an entry's created_by names — the foreign
// key wants a live account, the way a real call's caller is one.
func insertAdmin(t *testing.T, pool *datastore.Postgres, username string) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(entity.TableUsers)
	sb.Cols("id", "username", "email", "display_name")
	sb.Values(id, username, username+"@example.com", username)

	query, args := sb.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
	return id
}

// TestAddStoresAndAnswersTheStoredRow pins the write: a valid entry is
// stored lowercased with its maker's name, a duplicate add is not an error
// and answers the row that was already there, and a malformed pattern is
// refused before the database is touched.
func TestAddStoresAndAnswersTheStoredRow(t *testing.T) {
	conttest.SkipWithoutDocker(t)
	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := context.Background()
	admin := insertAdmin(t, pool, "arthur")

	entry, err := service.Add(ctx, admin, "  John.Doe@Example.com ")
	require.NoError(t, err)
	assert.Equal(t, "john.doe@example.com", entry.Pattern)
	assert.NotNil(t, entry.CreatedBy)
	assert.Equal(t, admin, *entry.CreatedBy)

	duplicate, err := service.Add(ctx, admin, "john.doe@example.com")
	require.NoError(t, err, "a duplicate add answers the stored row")
	assert.Equal(t, entry.ID, duplicate.ID)
	assert.Equal(t, entry.CreatedAt, duplicate.CreatedAt)

	total, _, err := service.List(ctx, "", false, 1, 50)
	require.NoError(t, err)
	assert.Len(t, total, 1, "the duplicate stored no second row")

	_, err = service.Add(ctx, admin, "*@example.com")
	assert.ErrorIs(t, err, ErrPatternInvalid)
}

// TestRemoveDeletesAndRefusesAnUnknownIdentifier pins the delete: a live
// identifier leaves, an unknown one is the not-found failure, and a removed
// identifier can be added again.
func TestRemoveDeletesAndRefusesAnUnknownIdentifier(t *testing.T) {
	conttest.SkipWithoutDocker(t)
	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := context.Background()
	admin := insertAdmin(t, pool, "arthur")

	entry, err := service.Add(ctx, admin, "@spam.example")
	require.NoError(t, err)

	require.NoError(t, service.Remove(ctx, entry.ID))
	err = service.Remove(ctx, entry.ID)
	assert.ErrorIs(t, err, ErrEntryNotFound)

	again, err := service.Add(ctx, admin, "@spam.example")
	require.NoError(t, err, "a removed identifier can be stored again")
	assert.Equal(t, entry.Pattern, again.Pattern)
}

// TestListPagesTheEntries pins the page: newest first by default, the
// pattern sort honored, the total counted across the pages.
func TestListPagesTheEntries(t *testing.T) {
	conttest.SkipWithoutDocker(t)
	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := context.Background()
	admin := insertAdmin(t, pool, "arthur")

	first, err := service.Add(ctx, admin, "@a.example")
	require.NoError(t, err)
	second, err := service.Add(ctx, admin, "@b.example")
	require.NoError(t, err)

	page, meta, err := service.List(ctx, "", false, 1, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, second.ID, page[0].ID, "newest first")
	require.NotNil(t, meta.TotalItems)
	assert.Equal(t, 2, *meta.TotalItems)

	page, _, err = service.List(ctx, "pattern", true, 1, 2)
	require.NoError(t, err)
	require.Len(t, page, 2)
	assert.Equal(t, first.ID, page[0].ID, "the pattern sort ordered the page")
}

// TestBlockedReadsTheStoredEntries pins the gate over the real rows: the
// stored entries decide, the removal lifts the block. The fail-open stance
// is the sign-up gate's test — the caller that decides what a failed read
// means.
func TestBlockedReadsTheStoredEntries(t *testing.T) {
	conttest.SkipWithoutDocker(t)
	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := context.Background()
	admin := insertAdmin(t, pool, "arthur")

	blocked, err := service.Blocked(ctx, "john.doe@example.com")
	require.NoError(t, err)
	assert.False(t, blocked)

	_, err = service.Add(ctx, admin, "john.doe@example.com")
	require.NoError(t, err)
	blocked, err = service.Blocked(ctx, "john.doe+tag@example.com")
	require.NoError(t, err)
	assert.True(t, blocked, "the carry-over blocks the subaddressed variant")

	require.NoError(t, service.Remove(ctx, mustEntryID(t, service, "john.doe@example.com")))
	blocked, err = service.Blocked(ctx, "john.doe@example.com")
	require.NoError(t, err)
	assert.False(t, blocked)
}

// mustEntryID reads the identifier the stored pattern carries.
func mustEntryID(t *testing.T, service *Service, pattern string) uuid.UUID {
	t.Helper()
	entry, err := service.repo.GetEntryByPattern(context.Background(), service.pool, pattern)
	if err != nil {
		t.Fatalf("read the entry back: %v", err)
	}
	return entry.ID
}

// TestMapErrorCarriesTheConnectCodes pins the transport mapping.
func TestMapErrorCarriesTheConnectCodes(t *testing.T) {
	cases := []struct {
		err  error
		code connect.Code
	}{
		{ErrEntryNotFound, connect.CodeNotFound},
		{ErrPatternInvalid, connect.CodeInvalidArgument},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.code, connect.CodeOf(mapError(tc.err)), "%v", tc.err)
	}
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(mapError(errors.New("boom"))))
}

// TestCollisionTakenPinsTheScan pins the collision scan over the real rows:
// the exact duplicate is not a collision (the caller's taken-answer owns
// it), a base another account holds is, and the fold compares Gmail's dots.
func TestCollisionTakenPinsTheScan(t *testing.T) {
	conttest.SkipWithoutDocker(t)
	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := context.Background()

	insertAccount(t, pool, "jsmith", "jsmith@gmail.com")

	// The same mailbox, folded differently: a collision.
	taken, err := service.CollisionTaken(ctx, "j.smith+tag@gmail.com")
	require.NoError(t, err)
	assert.True(t, taken)

	// The exact candidate is not a collision — the taken-answer owns it.
	taken, err = service.CollisionTaken(ctx, "jsmith@gmail.com")
	require.NoError(t, err)
	assert.False(t, taken)

	// A different mailbox at the same domain is not one.
	taken, err = service.CollisionTaken(ctx, "sneveu@gmail.com")
	require.NoError(t, err)
	assert.False(t, taken)
}

// insertAccount creates the account row the collision scan reads.
func insertAccount(t *testing.T, pool *datastore.Postgres, username, email string) {
	t.Helper()
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(entity.TableUsers)
	sb.Cols("id", "username", "email", "display_name")
	sb.Values(uuid.NewV7(), username, email, username)

	query, args := sb.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}
