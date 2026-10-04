package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCORSPreflightNamesTheAllowedOrigin(t *testing.T) {
	handle := CORS(CORSOptions{
		AllowedOrigins: []string{"http://localhost:3080"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Authorization"},
		MaxAge:         time.Hour,
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/healthz", nil)
	req.Header.Set("Origin", "http://localhost:3080")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	handle(http.NotFoundHandler()).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code,
		"a preflight is answered with 204, the status rs/cors replies with")
	assert.Equal(t, "http://localhost:3080", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
	assert.NotEmpty(t, rec.Header().Get("Access-Control-Max-Age"))
}

func TestCORSLeavesAForeignOriginUnanswered(t *testing.T) {
	handle := CORS(CORSOptions{
		AllowedOrigins: []string{"http://localhost:3080"},
		AllowedMethods: []string{"GET"},
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/healthz", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()

	handle(http.NotFoundHandler()).ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"an origin the policy does not name gets no cross-origin answer")
}

func TestCORSWithNoOriginsIsAPassthrough(t *testing.T) {
	handle := CORS(CORSOptions{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:3080")
	rec := httptest.NewRecorder()

	handle(inner).ServeHTTP(rec, req)

	require.Equal(t, http.StatusTeapot, rec.Code,
		"an empty policy must not stand between a request and its handler")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSPreflightFallsBackToTheDualSurfaceDefaults(t *testing.T) {
	handle := CORS(CORSOptions{
		AllowedOrigins: []string{"http://localhost:3080"},
		MaxAge:         time.Hour,
	})

	// rs/cors answers a preflight by echoing the method and headers the
	// browser asked about, so the defaults are proven by accepting every
	// one of them and refusing what the list does not name.
	for _, method := range DefaultCORSMethods {
		req := httptest.NewRequest(http.MethodOptions, "/rpc/hogwarts.identity.v1.UserService/ListUsers", nil)
		req.Header.Set("Origin", "http://localhost:3080")
		req.Header.Set("Access-Control-Request-Method", method)
		rec := httptest.NewRecorder()
		handle(http.NotFoundHandler()).ServeHTTP(rec, req)

		require.Equal(t, http.StatusNoContent, rec.Code, method)
		assert.Equal(t, method, rec.Header().Get("Access-Control-Allow-Methods"), method)
		assert.Equal(t, "http://localhost:3080", rec.Header().Get("Access-Control-Allow-Origin"), method)
	}

	// A method neither the Connect nor the REST surface answers is refused.
	req := httptest.NewRequest(http.MethodOptions, "/rpc/hogwarts.identity.v1.UserService/ListUsers", nil)
	req.Header.Set("Origin", "http://localhost:3080")
	req.Header.Set("Access-Control-Request-Method", http.MethodTrace)
	rec := httptest.NewRecorder()
	handle(http.NotFoundHandler()).ServeHTTP(rec, req)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"a method outside the defaults gets no cross-origin answer")

	// The Connect protocol's headers and the machine credential's header are
	// preflightable without the configuration naming them. rs/cors answers a
	// preflight by echoing the names back, and its matcher demands the list
	// sorted, which is the order a browser sends.
	req = httptest.NewRequest(http.MethodOptions, "/rpc/hogwarts.identity.v1.UserService/ListUsers", nil)
	req.Header.Set("Origin", "http://localhost:3080")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "connect-protocol-version, content-type, x-api-key")
	rec = httptest.NewRecorder()
	handle(http.NotFoundHandler()).ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "connect-protocol-version, content-type, x-api-key",
		rec.Header().Get("Access-Control-Allow-Headers"),
		"the requested protocol headers are within the defaults")

	// A header outside the defaults is refused.
	req = httptest.NewRequest(http.MethodOptions, "/rpc/hogwarts.identity.v1.UserService/ListUsers", nil)
	req.Header.Set("Origin", "http://localhost:3080")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "x-unknown")
	rec = httptest.NewRecorder()
	handle(http.NotFoundHandler()).ServeHTTP(rec, req)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"a header outside the defaults gets no cross-origin answer")
}

func TestCORSNamesTheExposedResponseHeaders(t *testing.T) {
	handle := CORS(CORSOptions{
		AllowedOrigins: []string{"http://localhost:3080"},
		MaxAge:         time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/rpc/saka.system.v1.HealthService/Check", nil)
	req.Header.Set("Origin", "http://localhost:3080")
	rec := httptest.NewRecorder()

	handle(http.NotFoundHandler()).ServeHTTP(rec, req)

	exposed := rec.Header().Get("Access-Control-Expose-Headers")
	for _, header := range DefaultCORSExposedHeaders {
		assert.Contains(t, exposed, header,
			"a cross-origin script must be able to read the RPC status fields")
	}
}

func TestCORSConfigOverridesTheDefaults(t *testing.T) {
	handle := CORS(CORSOptions{
		AllowedOrigins: []string{"http://localhost:3080"},
		AllowedMethods: []string{"GET"},
		AllowedHeaders: []string{"X-Custom"},
		ExposedHeaders: []string{"X-Custom-Response"},
		MaxAge:         time.Hour,
	})

	// A named method list is the operator's policy: POST, which the defaults
	// answer, is now refused.
	req := httptest.NewRequest(http.MethodOptions, "/rpc/hogwarts.identity.v1.UserService/ListUsers", nil)
	req.Header.Set("Origin", "http://localhost:3080")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	handle(http.NotFoundHandler()).ServeHTTP(rec, req)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"a named list replaces the defaults wholesale")

	// A named header list answers only its own names.
	req = httptest.NewRequest(http.MethodOptions, "/rpc/hogwarts.identity.v1.UserService/ListUsers", nil)
	req.Header.Set("Origin", "http://localhost:3080")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "x-custom")
	rec = httptest.NewRecorder()
	handle(http.NotFoundHandler()).ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "x-custom")

	// The named expose list reaches an actual cross-origin response, and no
	// default leaks into it.
	req = httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	req.Header.Set("Origin", "http://localhost:3080")
	rec = httptest.NewRecorder()
	handle(http.NotFoundHandler()).ServeHTTP(rec, req)
	assert.Equal(t, "X-Custom-Response", rec.Header().Get("Access-Control-Expose-Headers"))
}
