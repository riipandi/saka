package seeders_test

import (
	"strings"
	"testing"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/database/seeders"
	"github.com/riipandi/saka/pkg/crypto"
)

// The groups the user group seeder writes, in the order its report carries
// them.
var expectedGroups = []string{"editors", "moderators", "archivists"}

// The authorization seeder writes the catalog, the administrator, and the
// scenario roles; the user seeder grants two of them to scenario accounts.
// The junction counts pin each role's set against its definition.
func TestAuthorizationSeederSeedsTheScenarioRolesAndGrants(t *testing.T) {
	pool := newSeededPool(t)

	results := runSeeders(t, pool, false)

	require.Len(t, results[0].Created, 52)
	assert.Contains(t, results[0].Created, "editor (6 permissions)")
	assert.Contains(t, results[0].Created, "moderator (5 permissions)")
	assert.Contains(t, results[0].Created, "service (4 permissions)")

	assert.Equal(t, 4, countRows(t, pool, `SELECT count(*) FROM public.roles`))
	assert.Equal(t, 3, countRows(t, pool, `SELECT count(*) FROM public.roles WHERE type = 'custom'`))

	for role, want := range map[string]int{"editor": 6, "moderator": 5, "service": 4, "administrator": 48} {
		count := countRows(t, pool, `SELECT count(*) FROM public.role_permissions rp
			JOIN public.roles r ON r.id = rp.role_id WHERE r.slug = '`+role+`'`)
		assert.Equal(t, want, count, role)
	}

	// The grants close the loop: a token minted for the editor scenario
	// account carries the role, and the moderator account carries its own.
	for username, role := range map[string]string{
		"robert_langdon":   "editor",
		"hermione_granger": "moderator",
	} {
		count := countRows(t, pool, `SELECT count(*) FROM public.user_roles ur
			JOIN public.users u ON u.id = ur.user_id
			JOIN public.roles r ON r.id = ur.role_id
			WHERE u.username = '`+username+`' AND r.slug = '`+role+`' AND ur.revoked_at IS NULL`)
		assert.Equal(t, 1, count, username+" holds "+role)
	}
	assert.Equal(t, 0, countRows(t, pool, `SELECT count(*) FROM public.user_roles ur
		JOIN public.roles r ON r.id = ur.role_id WHERE r.slug = 'service'`))
}

// Every seeder must be reachable from All, or it never runs.
func TestAllIncludesTheUserGroupSeeder(t *testing.T) {
	names := make([]string, 0, len(seeders.All()))
	for _, seeder := range seeders.All() {
		names = append(names, seeder.Name)
	}
	assert.Contains(t, names, seeders.UserGroupSeederName)
}

func TestUserGroupSeederCreatesTheGroupsAndMemberships(t *testing.T) {
	pool := newSeededPool(t)

	results := runSeeders(t, pool, false)

	require.Len(t, results, 7)
	require.Equal(t, seeders.UserGroupSeederName, results[2].Name)

	require.Len(t, results[2].Created, 6)
	require.Equal(t, expectedGroups[0], results[2].Created[0])
	assert.Contains(t, results[2].Created, "robert_langdon in editors")
	assert.Contains(t, results[2].Created, "sophie_neveu in editors")
	assert.Contains(t, results[2].Created, "hermione_granger in moderators")
	assert.Empty(t, results[2].Skipped)

	// The empty group exists but carries no membership row: the shape a
	// feature naming an audience must survive.
	count := countRows(t, pool, `SELECT count(*) FROM public.user_groups_users ug
		JOIN public.user_groups g ON g.id = ug.user_group_id WHERE g.name = 'archivists'`)
	assert.Equal(t, 0, count)
	assert.Equal(t, 3, countRows(t, pool, "SELECT count(*) FROM public.user_groups"))
}

func TestUserGroupSeederIsIdempotent(t *testing.T) {
	pool := newSeededPool(t)
	runSeeders(t, pool, false)

	results := runSeeders(t, pool, false)

	require.Equal(t, seeders.UserGroupSeederName, results[2].Name)
	assert.Empty(t, results[2].Created)
	assert.Len(t, results[2].Skipped, 6)
	assert.Equal(t, 3, countRows(t, pool, "SELECT count(*) FROM public.user_groups"))
	assert.Equal(t, 3, countRows(t, pool, "SELECT count(*) FROM public.user_groups_users"))
}

func TestAPIKeySeederCreatesTheScenarioKeys(t *testing.T) {
	pool := newSeededPool(t)

	results := runSeeders(t, pool, false)

	require.Len(t, results, 7)
	require.Equal(t, seeders.APIKeySeederName, results[3].Name)
	require.Len(t, results[3].Created, 3)
	assert.Empty(t, results[3].Skipped)

	// The live key's report line is the one disclosure of the raw
	// credential: owner, name, prefix, separator, secret.
	live := results[3].Created[0]
	assert.True(t, strings.HasPrefix(live, seeders.DefaultUser.Email+" (Development Key "), live)
	raw := strings.TrimSuffix(strings.TrimPrefix(live, seeders.DefaultUser.Email+" (Development Key "), ")")
	parts := strings.Split(raw, ".")
	require.Len(t, parts, 2)
	assert.Len(t, parts[0], 8)
	assert.Len(t, parts[1], 32)

	// The stored hash is the hash of the reported credential, so what a
	// test copies off the seed report authenticates.
	var stored []byte
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key_hash").From(entity.TableAPIKeys).
		Where(sb.Equal("name", "Development Key"), sb.Equal("prefix", parts[0]))
	query, args := sb.Build()
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&stored))
	assert.Equal(t, crypto.HashTokenBytes(raw), stored)

	assert.Equal(t, 1, countRows(t, pool,
		"SELECT count(*) FROM public.api_keys WHERE name = 'Expiring Key' AND revoked_at IS NULL"))
	assert.Equal(t, 1, countRows(t, pool,
		"SELECT count(*) FROM public.api_keys WHERE name = 'Revoked Key' AND revoked_at IS NOT NULL"))
}

func TestAPIKeySeederIsIdempotent(t *testing.T) {
	pool := newSeededPool(t)
	runSeeders(t, pool, false)

	results := runSeeders(t, pool, false)

	require.Equal(t, seeders.APIKeySeederName, results[3].Name)
	assert.Empty(t, results[3].Created)
	assert.Len(t, results[3].Skipped, 3)
	assert.Equal(t, 3, countRows(t, pool, "SELECT count(*) FROM public.api_keys"))
}

// countRows answers what a literal count query selects — the assertions read
// the database, never what the seeder printed.
func countRows(t *testing.T, pool *datastore.Postgres, query string) int {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query).Scan(&count))
	return count
}
