package registry_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/kernel"
	"github.com/riipandi/tango/internal/registry"
	"github.com/riipandi/tango/internal/transport/middleware"
	"github.com/riipandi/tango/pkg/jwtutils"
)

// reportsArea is an area a consumer wrote outside this repository. It owns one
// service and one route, and it needs nothing from identity.
type reportsArea struct{}

func (reportsArea) Name() string { return "reports" }

func (reportsArea) Mount(r chi.Router) {
	r.Get("/reports/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})
}

// TestAConsumerServesItsOwnArea is the composability contract: an area that
// lives outside this repository reaches the running server by being passed to
// New, with no edit to internal/registry.
//
// It is the test that failed before the seam existed — the area mounted
// nowhere and its route answered 404.
func TestAConsumerServesItsOwnArea(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.PrivateKey = ""
	cfg.Auth.PublicKey = ""
	cfg.Auth.SecretKey = ""
	cfg.Database.URL = "postgres://u:p@127.0.0.1:1/none?connect_timeout=1"

	extra := registry.Area{
		Name:    "reports",
		Package: do.Package(do.Eager(reportsArea{})),
		Mount: func(i do.Injector) (kernel.Module, error) {
			return do.MustInvoke[reportsArea](i), nil
		},
	}

	injector := registry.New(t.Context(), cfg, nil, slog.New(slog.DiscardHandler), extra)
	t.Cleanup(func() { injector.Shutdown() })

	// The pool is replaced with a stub: this test is about the area seam, not
	// about reaching a database, and the health check that holds the pool runs
	// on request rather than at construction.
	do.OverrideValue(injector, &datastore.Postgres{})
	// The limiter is stubbed for the same reason. It reads the pool by
	// default, and the stub above has no connections: the throttling itself is
	// covered by internal/transport, and what this test asserts is that an
	// area passed to New reaches the served router.
	do.Override[middleware.Limiter](injector, func(do.Injector) (middleware.Limiter, error) {
		return allowAll{}, nil
	})
	// The authenticator is stubbed for the same reason: the consumer area's
	// route is protected and administrative by default — a route the guard
	// table does not name is administrative — so the stub answers the
	// administrator the default rule requires.
	do.Override[middleware.Authenticator](injector, func(do.Injector) (middleware.Authenticator, error) {
		return func(context.Context, *http.Request) (any, error) {
			return &jwtutils.Caller{
				UserID:       "01a0da1c-cb41-779d-bd02-99b3eb5da32a",
				AccessClaims: jwtutils.AccessClaims{Username: "admin", IsAdmin: true},
			}, nil
		}, nil
	})

	router, err := do.Invoke[chi.Router](injector)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/reports/ping", nil))

	require.Equal(t, http.StatusOK, rec.Code,
		"an area passed to New must be mounted on the served router")
	assert.Equal(t, "pong", rec.Body.String())
}

// allowAll is a limiter that never throttles, so a test can exercise the
// routing above it without a backend.
type allowAll struct{}

func (allowAll) Allow(context.Context, string) (middleware.Result, error) {
	return middleware.Result{Limit: 60, Remaining: 60}, nil
}

// TestTheApplicationAreasAreListedOnce keeps the built-in list honest: every
// area is reachable, and the list carries each by name. The length is pinned
// on purpose — a second area is a decision, and this is where it is noticed.
func TestTheApplicationAreasAreListedOnce(t *testing.T) {
	areas := registry.Areas()
	require.Len(t, areas, 4)

	assert.Equal(t, "identity", areas[0].Name)
	assert.Equal(t, "apikey", areas[1].Name)
	assert.Equal(t, "auditlog", areas[2].Name)
	assert.Equal(t, "notification", areas[3].Name)
	for _, area := range areas {
		assert.NotNil(t, area.Package, "area %q must register its own services", area.Name)
		assert.NotNil(t, area.Mount, "area %q must build its own module", area.Name)
	}
}
