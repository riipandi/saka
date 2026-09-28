package authorization

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/cache"
)

// TestAListingIsServedFromTheCache pins the read the administrative
// surfaces depend on: two identical list calls, one query — and the cached
// answer carries the rows the seed wrote, wire-form identifiers included.
func TestAListingIsServedFromTheCache(t *testing.T) {
	pool := migratedPool(t)
	kvCache := cache.NewMemory(0, 0)
	service := NewService(pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), kvCache)
	seedCatalog(t, pool)

	first, pagination, err := service.ListRoles(t.Context(), false, "", "", "", true, 1, 100)
	require.NoError(t, err)
	require.NotEmpty(t, first, "the seed's roles rest in the table")

	second, cachedPagination, err := service.ListRoles(t.Context(), false, "", "", "", true, 1, 100)
	require.NoError(t, err)
	assert.Equal(t, first, second, "the cached page answers the same rows")
	assert.Equal(t, pagination, cachedPagination)
}

// TestAChangeDropsEveryCachedListing pins the invalidation a write owes the
// family: a role created after a listing is in the next listing, and the
// detail of a role renamed answers the new name — the write drops the
// prefix, not one fingerprint it cannot know.
func TestAChangeDropsEveryCachedListing(t *testing.T) {
	pool := migratedPool(t)
	kvCache := cache.NewMemory(0, 0)
	service := NewService(pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), kvCache)
	seedCatalog(t, pool)

	_, _, err := service.ListRoles(t.Context(), false, "", "", "", true, 1, 100)
	require.NoError(t, err)

	created, err := service.CreateRole(t.Context(), CreateParams{Name: "Archivist", Slug: "archivist"})
	require.NoError(t, err)

	roles, _, err := service.ListRoles(t.Context(), false, "", "", "", true, 1, 100)
	require.NoError(t, err)
	var found bool
	for _, role := range roles {
		if role.Slug == "archivist" {
			found = true
		}
	}
	assert.True(t, found, "the listing the cache answers again carries the created role")

	renamed, err := service.UpdateRole(t.Context(), created.ID.String(), CreateParams{Name: "Keeper of Records", Slug: "archivist"})
	require.NoError(t, err)
	assert.Equal(t, "Keeper of Records", renamed.Name,
		"the detail answers the rename, not a cached answer from before it")
}

// TestABypassedReadLeavesTheCacheAlone pins the caller-facing escape hatch.
// The source is made to drift from the cache by a row an outside hand
// wrote — a write through the service would have dropped the entry — and
// the bypassed read answers the source while the cached answer the other
// callers share is neither consulted nor replaced.
func TestABypassedReadLeavesTheCacheAlone(t *testing.T) {
	pool := migratedPool(t)
	kvCache := cache.NewMemory(0, 0)
	service := NewService(pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), kvCache)
	seedCatalog(t, pool)

	_, _, err := service.ListRoles(t.Context(), false, "", "", "", true, 1, 100)
	require.NoError(t, err)

	// The outside hand: a row the service never wrote, so no invalidation
	// ran and the cache has aged away from the table.
	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.roles (name, slug, description, type)
		VALUES ('Curator', 'curator', '', 'custom')`)
	require.NoError(t, err)

	bypassed, _, err := service.ListRoles(t.Context(), true, "", "", "", true, 1, 100)
	require.NoError(t, err)
	var carried bool
	for _, role := range bypassed {
		if role.Slug == "curator" {
			carried = true
		}
	}
	assert.True(t, carried, "the bypassed read answers the source")

	cached, _, err := service.ListRoles(t.Context(), false, "", "", "", true, 1, 100)
	require.NoError(t, err)
	carried = false
	for _, role := range cached {
		if role.Slug == "curator" {
			carried = true
		}
	}
	assert.False(t, carried, "the cached answer is the one the bypass left alone")
}
