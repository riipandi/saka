//go:build debug

package main

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	appauthz "github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/pkg/envfile"
)

// seededDatabase returns the envFile of a database the migrations and the
// seeders have both run against — the shape a deployment that later lags
// the catalog starts from.
func seededDatabase(t *testing.T) string {
	t.Helper()

	envFile := migratedDatabase(t)
	_, err := runMigrateSeedCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	return envFile
}

// grantsCheck runs the serve check against the pool and answers the warning
// output it produced, so a test reads what an operator would see.
func grantsCheck(t *testing.T, pool *datastore.Postgres) (string, error) {
	t.Helper()

	var out bytes.Buffer
	log := slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelWarn}))
	err := checkAdministratorGrants(t.Context(), pool, log)
	return out.String(), err
}

// poolOfEnvFile opens the pool the env file points at.
func poolOfEnvFile(t *testing.T, envFile string) *datastore.Postgres {
	t.Helper()

	file, err := envfile.Load(envFile)
	require.NoError(t, err)
	dsn, ok := file.Get(envfile.DatabaseURL)
	require.True(t, ok)
	return testPool(t, dsn)
}

// revokeGrant deletes the administrator's junction row for one catalog slug
// — the drift a deployment seeded before a catalog addition carries.
func revokeGrant(t *testing.T, pool *datastore.Postgres, slug string) {
	t.Helper()

	tag, err := pool.Exec(t.Context(), `DELETE FROM public.role_permissions rp
		USING public.roles r, public.permissions p
		WHERE rp.role_id = r.id AND rp.permission_id = p.id
			AND r.slug = $1 AND p.slug = $2`, appauthz.AdministratorRole, slug)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected())
}

// A database the seed covers answers no warning: the common case must not
// nag.
func TestServeGrantsAreQuietWhenTheCatalogIsCurrent(t *testing.T) {
	pool := poolOfEnvFile(t, seededDatabase(t))

	out, err := grantsCheck(t, pool)
	require.NoError(t, err)
	assert.Empty(t, out)
}

// A database seeded before a catalog addition must learn the gap and the
// repair command at boot, not through a missing menu weeks later.
func TestServeWarnsWhenTheAdministratorGrantLagsTheCatalog(t *testing.T) {
	pool := poolOfEnvFile(t, seededDatabase(t))
	slugs := appauthz.AllSlugs()
	lagging := slugs[len(slugs)-1]
	revokeGrant(t, pool, lagging)

	out, err := grantsCheck(t, pool)
	require.NoError(t, err)
	assert.Contains(t, out, "the administrator grant lags the permission catalog")
	assert.Contains(t, out, lagging)
	assert.Contains(t, out, "migrate:seed")
}

// A database whose migrations have run but whose seed has not answers the
// loudest version of the warning — the administrator role exists with an
// empty junction, so every catalog slug is missing — and the repair command
// is what turns it current.
func TestServeWarnsWhenTheJunctionIsEmpty(t *testing.T) {
	pool := poolOfEnvFile(t, migratedDatabase(t))

	out, err := grantsCheck(t, pool)
	require.NoError(t, err)
	assert.Contains(t, out, "the administrator grant lags the permission catalog")
	assert.Contains(t, out, "user_group:*:read")
	assert.Contains(t, out, "migrate:seed")
}

// The check never writes: a lagging database must leave the junction as it
// found it, because serve repairs nothing.
func TestServeGrantCheckWritesNothing(t *testing.T) {
	pool := poolOfEnvFile(t, seededDatabase(t))
	slugs := appauthz.AllSlugs()
	lagging := slugs[len(slugs)-1]
	revokeGrant(t, pool, lagging)

	_, err := grantsCheck(t, pool)
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.role_permissions rp
			JOIN public.roles r ON r.id = rp.role_id
			JOIN public.permissions p ON p.id = rp.permission_id
			WHERE r.slug = $1 AND p.slug = $2`,
		appauthz.AdministratorRole, lagging).Scan(&count))
	assert.Zero(t, count)
}
