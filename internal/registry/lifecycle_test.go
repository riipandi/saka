package registry_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/database"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/registry"
	"github.com/riipandi/tango/pkg/testutils"
)

// servedInjector builds the container over a fresh migrated database, the
// state a serve run reaches before the listener opens: the prewarm walk seeds
// the recurring jobs, which reads the database, so a real one backs the walk.
func servedInjector(t *testing.T) *do.RootScope {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := database.NewMigrator(t.Context(), migrationDB, database.MigratorOptions{})
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	cfg := config.Default()
	cfg.Database.URL = dsn
	cfg.Storage.Watch.Enable = true
	cfg.App.BaseURL = "https://idp.example.com"
	// The protocol feature signs through the jwks service; the HMAC key
	// is the signing key a bare run carries.
	cfg.Auth.SecretKey = strings.Repeat("ab", 32)

	injector := registry.New(t.Context(), cfg, nil, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { injector.Shutdown() })
	return injector
}

// TestPrewarmResolvesEveryBlockingService is the warm-up contract: a run with
// a database that answers prewarms clean, and the router behind the server is
// built — the areas' Mount ran — by the time it returns.
//
// The second half is the anti-drift check: every service the container holds
// must have been invoked by the walk, because a lazy service left cold after
// Prewarm is one whose failure would surface as a 500 on the first request —
// exactly what the warm-up exists to prevent. Registering a service without
// wiring it into the walk fails here, at test time.
func TestPrewarmResolvesEveryBlockingService(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	injector := servedInjector(t)

	require.NoError(t, registry.Prewarm(t.Context(), injector))

	// A service is cold by exception only, each with the reason it must be:
	// the Valkey client is opt-in and this run did not opt in, the cache
	// faces modules and stays cold until a feature resolves it, and the
	// watcher belongs to Runners — resolved there when storage.watch.enable
	// is on, and its construction cannot fail on a dependency.
	cold := map[string]string{
		"*github.com/riipandi/tango/internal/datastore.Valkey": "opt-in backend, disabled in this run",
		"github.com/riipandi/tango/internal/cache.Cache":       "module-facing, cold until a feature resolves it",
		"*github.com/riipandi/tango/internal/storage.Watcher":  "runner-owned, not on the prewarm walk",
	}

	invoked := make(map[string]bool)
	for _, d := range injector.ListInvokedServices() {
		invoked[d.ScopeID+"/"+d.Service] = true
	}
	for _, d := range injector.ListProvidedServices() {
		if _, ok := invoked[d.ScopeID+"/"+d.Service]; ok {
			continue
		}
		if _, ok := cold[d.Service]; ok {
			continue
		}
		t.Errorf("service %q in scope %q was not invoked by Prewarm: a service left cold fails on the first request, not at startup — wire it into the prewarm walk or list it among the cold exceptions with a reason",
			d.Service, d.ScopeName)
	}
}

// TestRunnersAreTheLongRunningComponentsInStartOrder pins the list the serve
// run starts and drains: the queue first, the scheduler second, the staging
// watcher last and only when it is enabled.
func TestRunnersAreTheLongRunningComponentsInStartOrder(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	injector := servedInjector(t)

	runners, err := registry.Runners(injector)
	require.NoError(t, err)

	names := make([]string, 0, len(runners))
	for _, runner := range runners {
		names = append(names, runner.Name)
	}
	assert.Equal(t, []string{"queue", "scheduler", "staging watch"}, names)

	// The drain ownership travels with the list: the queue outlives the
	// listener, so the container's shutdown walk drains it; the scheduler
	// stops in the run's own drain window.
	assert.Nil(t, runners[0].Stop, "the queue's drain belongs to the injector")
	assert.NotNil(t, runners[1].Stop, "the scheduler drains inside the shutdown window")
	assert.Nil(t, runners[2].Stop, "the watcher ends with the run's context")
}
