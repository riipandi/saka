package kernel

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mountingModule is a module that registers the given method/pattern pairs on
// whatever router it is handed.
type mountingModule struct {
	name    string
	methods map[string]string
}

func (m mountingModule) Name() string { return m.name }

func (m mountingModule) Mount(r chi.Router) {
	for method, pattern := range m.methods {
		r.Method(method, pattern, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	}
}

// rpcMountingModule is a module that also registers procedure paths.
type rpcMountingModule struct {
	mountingModule
	procedures []string
}

func (m rpcMountingModule) MountRPC(r chi.Router, _ ...connect.HandlerOption) {
	for _, procedure := range m.procedures {
		r.Handle(procedure, http.NotFoundHandler())
	}
}

// TestMountRegistersEveryModuleRoutes is the base contract: disjoint modules
// all answer.
func TestMountRegistersEveryModuleRoutes(t *testing.T) {
	r := chi.NewRouter()
	Mount(r,
		mountingModule{name: "a", methods: map[string]string{http.MethodGet: "/a"}},
		mountingModule{name: "b", methods: map[string]string{http.MethodGet: "/b"}},
	)

	assertRouteServed(t, r, http.MethodGet, "/a")
	assertRouteServed(t, r, http.MethodGet, "/b")
}

// TestMountFailsOnAClaimedTwiceRoute is the detection: two modules claiming
// one pattern fail the mount naming both, instead of the second silently
// replacing the first.
func TestMountFailsOnAClaimedTwiceRoute(t *testing.T) {
	r := chi.NewRouter()

	assert.PanicsWithValue(t,
		`kernel: module "b" claims route GET /api/users already claimed by module "a"`,
		func() {
			Mount(r,
				mountingModule{name: "a", methods: map[string]string{http.MethodGet: "/api/users"}},
				mountingModule{name: "b", methods: map[string]string{http.MethodGet: "/api/users"}},
			)
		})
}

// TestMountServesTheSamePathUnderDifferentMethods: a claim is method and
// pattern, so one module owning a path's GET and another its POST is no
// conflict.
func TestMountServesTheSamePathUnderDifferentMethods(t *testing.T) {
	r := chi.NewRouter()
	Mount(r,
		mountingModule{name: "a", methods: map[string]string{http.MethodGet: "/api/items"}},
		mountingModule{name: "b", methods: map[string]string{http.MethodPost: "/api/items"}},
	)

	assertRouteServed(t, r, http.MethodGet, "/api/items")
	assertRouteServed(t, r, http.MethodPost, "/api/items")
}

// TestMountRPCSkipsModulesWithoutProcedures keeps the type-assertion seam:
// a module that serves no procedure says so by not implementing RPCModule.
func TestMountRPCSkipsModulesWithoutProcedures(t *testing.T) {
	r := chi.NewRouter()
	assert.NotPanics(t, func() {
		MountRPC(r, nil,
			mountingModule{name: "plain", methods: map[string]string{http.MethodGet: "/plain"}},
			rpcMountingModule{
				name:       "rpc",
				procedures: []string{"/hogwarts.test.v1.FeatureService/Ping"},
			},
		)
	})
}

// TestMountRPCFailsOnAClaimedTwiceProcedure: two modules registering one
// procedure path fail the mount naming both — the generated handler would
// otherwise answer whichever registered last.
func TestMountRPCFailsOnAClaimedTwiceProcedure(t *testing.T) {
	r := chi.NewRouter()

	assert.PanicsWithValue(t,
		`kernel: module "b" claims procedure /hogwarts.test.v1.FeatureService/Ping already claimed by module "a"`,
		func() {
			MountRPC(r, nil,
				rpcMountingModule{
					name:       "a",
					procedures: []string{"/hogwarts.test.v1.FeatureService/Ping"},
				},
				rpcMountingModule{
					name:       "b",
					procedures: []string{"/hogwarts.test.v1.FeatureService/Ping"},
				},
			)
		})
}

func assertRouteServed(t *testing.T, r chi.Router, method, path string) {
	t.Helper()

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	require.Equal(t, http.StatusOK, rec.Code, "route %s %s must answer", method, path)
}
