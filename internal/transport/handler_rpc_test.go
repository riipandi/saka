package transport_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authnv1 "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1"
	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	systemv1 "github.com/riipandi/saka/codegen/proto/go/saka/system/v1"
	"github.com/riipandi/saka/codegen/proto/go/saka/system/v1/systemv1connect"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/health"
	"github.com/riipandi/saka/internal/kernel"
	"github.com/riipandi/saka/internal/transport"
)

// rpcRequest builds the POST a Connect client sends: the procedure path below
// /rpc, the unary JSON content type, and the protocol version header the
// contract requires.
func rpcRequest(t *testing.T, procedure, body string) *http.Request {
	req := rpcRequestWithHeaders(t, procedure, body, nil)
	return req
}

// rpcRequestWithHeaders builds a procedure request carrying extra headers —
// the step-up proof's header is the one a Reauthenticated procedure reads.
func rpcRequestWithHeaders(t *testing.T, procedure, body string, headers map[string]string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost,
		transport.RPCPath+procedure, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return req
}

// newRPCRouter builds the router with a checker over one passing check, so the
// readiness procedure has a result to publish.
func newRPCRouter(t *testing.T) http.Handler {
	t.Helper()

	checker := health.NewChecker(health.WithCheck(health.Check{
		Name: "database",
		Check: func(context.Context) error {
			return nil
		},
	}))
	return transport.NewRouter(transport.Options{
		Config:  config.Default(),
		Checker: checker,
	})
}

// TestRPCCheckAnswersTheReadinessDocument is the endpoint this contract was
// introduced for: a client that speaks ConnectRPC reads the same readiness the
// REST endpoint publishes.
func TestRPCCheckAnswersTheReadinessDocument(t *testing.T) {
	router := newRPCRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.system.v1.HealthService/Check", "{}"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body struct {
		Status  string `json:"status"`
		Details []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, "healthy", body.Status)
	assert.True(t, strings.HasPrefix(rec.Header().Get("X-Request-Id"), "req_"),
		"the response header names the request the middleware tagged")
	assert.Len(t, rec.Header().Values("X-Request-Id"), 1,
		"the middleware is the header's only writer; connect appends handler-set headers, so a second writer duplicates it")
	require.Len(t, body.Details, 1)
	assert.Equal(t, "database", body.Details[0].Name)
	assert.Equal(t, "up", body.Details[0].Status)
}

// TestRPCFieldsAreSnakeCase pins the one deviation protobuf's JSON mapping
// would otherwise introduce: the declared proto field names are serialized, so
// an RPC field and its REST twin are spelled the same way.
func TestRPCFieldsAreSnakeCase(t *testing.T) {
	router := newRPCRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.system.v1.HealthService/Check", "{}"))

	body := rec.Body.String()
	assert.Contains(t, body, `"took_ms"`, "the body must use the proto field name")
	assert.NotContains(t, body, `"tookMs"`, "the default lowerCamelCase mapping must not be used")
}

// TestRPCUnhealthyAnswersUnavailable pins the failure contract: a probe reads
// the status code and the message names what is down.
func TestRPCUnhealthyAnswersUnavailable(t *testing.T) {
	checker := health.NewChecker(health.WithCheck(health.Check{
		Name: "database",
		Check: func(context.Context) error {
			return assert.AnError
		},
	}))
	router := transport.NewRouter(transport.Options{
		Config:  config.Default(),
		Checker: checker,
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.system.v1.HealthService/Check", "{}"))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, "unavailable", body.Code)
	assert.Contains(t, body.Message, "database", "the message must name the failing check")
	assert.True(t, strings.HasPrefix(rec.Header().Get("X-Request-Id"), "req_"),
		"a failed call still carries the correlation id")
	assert.Len(t, rec.Header().Values("X-Request-Id"), 1,
		"the middleware's header survives an error answer; no handler may write it again")
}

// TestRPCUnknownProcedureAnswersConnectError covers the not-found boundary:
// an unknown procedure must be answered in the caller's own protocol, never
// with the SPA document or the REST envelope.
func TestRPCUnknownProcedureAnswersConnectError(t *testing.T) {
	router := newRPCRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.system.v1.HealthService/Absent", "{}"))

	require.Equal(t, http.StatusNotImplemented, rec.Code, rec.Body.String())

	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "unimplemented", body.Code)
}

// TestRPCRejectsGet pins the transport rule the contract states: no procedure
// declares `idempotency_level = NO_SIDE_EFFECTS`, so every procedure is
// POST-only and a GET is refused with the Allow header a client reads.
func TestRPCRejectsGet(t *testing.T) {
	router := newRPCRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		transport.RPCPath+"/saka.system.v1.HealthService/Check", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"a GET must not reach a unary procedure")
	assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"),
		"the refusal must name the method the procedure accepts")
}

// TestRPCCheckIsCallableByTheGeneratedClient proves the contract from the
// client side, which is what the SPA and internal tools do.
func TestRPCCheckIsCallableByTheGeneratedClient(t *testing.T) {
	server := httptest.NewServer(newRPCRouter(t))
	defer server.Close()

	client := systemv1connect.NewHealthServiceClient(server.Client(), server.URL+transport.RPCPath)
	resp, err := client.Check(t.Context(), connect.NewRequest(&systemv1.CheckRequest{}))
	require.NoError(t, err)

	assert.Equal(t, "healthy", resp.Msg.GetStatus())
	assert.True(t, strings.HasPrefix(resp.Header().Get("X-Request-Id"), "req_"),
		"the generated client reads the correlation id from the response header")
	assert.Len(t, resp.Header().Values("X-Request-Id"), 1,
		"the generated client must read one id, not a duplicated pair")
}

// rpcFeature is a module that serves a procedure, the way an application
// module does once it has a contract. It records the handler options the
// transport handed it, so a test can prove the shared codec reaches a module's
// own handler.
type rpcFeature struct {
	options []connect.HandlerOption
}

func (rpcFeature) Name() string { return "rpc-feature" }

func (rpcFeature) Mount(chi.Router) {}

func (f *rpcFeature) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	f.options = opts
	r.Post("/saka.test.v1.FeatureService/Ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pong":true}`))
	})
}

// TestAModuleProcedureMountsBelowThePrefix pins the seam a feature uses to add
// a procedure: it mounts on the same router as the transport's own services,
// below the prefix, with the prefix already stripped.
func TestAModuleProcedureMountsBelowThePrefix(t *testing.T) {
	feature := &rpcFeature{}
	router := transport.NewRouter(transport.Options{
		Config:  config.Default(),
		Checker: health.NewChecker(),
		Modules: []kernel.Module{feature},
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, "/saka.test.v1.FeatureService/Ping", "{}"))

	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"pong":true}`, rec.Body.String())
	assert.NotEmpty(t, feature.options,
		"the transport must hand a module the options that carry the shared codec")
}

// TestModuleProcedureGetsTheSnakeCaseCodec is the reason the options travel:
// a module that registers a generated handler with them answers in the same
// field names the transport's own services do. Registering without them would
// silently fall back to protobuf's camelCase mapping, which is the drift the
// shared codec exists to prevent.
func TestModuleProcedureGetsTheSnakeCaseCodec(t *testing.T) {
	feature := &rpcFeature{}
	transport.NewRouter(transport.Options{
		Config:  config.Default(),
		Checker: health.NewChecker(),
		Modules: []kernel.Module{feature},
	})

	_, handler := systemv1connect.NewHealthServiceHandler(
		stubHealthService{}, feature.options...)
	req := httptest.NewRequest(http.MethodPost,
		systemv1connect.HealthServiceCheckProcedure, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"took_ms"`,
		"a module's handler must serialize under the proto field names")
	assert.NotContains(t, rec.Body.String(), `"tookMs"`)

	// Negative control: the same handler without the options falls back to
	// protobuf's own mapping. Without this the assertion above would also pass
	// on a codec that never ran, which is what makes it a real check.
	controlReq := httptest.NewRequest(http.MethodPost,
		systemv1connect.HealthServiceCheckProcedure, strings.NewReader("{}"))
	controlReq.Header.Set("Content-Type", "application/json")
	controlReq.Header.Set("Connect-Protocol-Version", "1")

	_, plain := systemv1connect.NewHealthServiceHandler(stubHealthService{})
	control := httptest.NewRecorder()
	plain.ServeHTTP(control, controlReq)
	assert.Contains(t, control.Body.String(), `"tookMs"`,
		"the default mapping is camelCase, so the shared codec is what changes it")
}

// stubHealthService is a minimal HealthServiceHandler for the codec assertion
// above; the real one is exercised through the router.
type stubHealthService struct{}

func (stubHealthService) Check(context.Context, *connect.Request[systemv1.CheckRequest]) (*connect.Response[systemv1.CheckResponse], error) {
	return connect.NewResponse(&systemv1.CheckResponse{
		Status: "healthy",
		TookMs: 1.5,
	}), nil
}

// TestTheCodecWritesALifetimeAsANumber pins the int32 answer: protobuf's JSON
// mapping writes a 64-bit integer as a string, and a token lifetime arriving
// as `"access_expires_in":"900"` is what a client reading an OpenAPI-shaped
// response cannot parse. The shared codec serializes the 32-bit field as a
// number.
func TestTheCodecWritesALifetimeAsANumber(t *testing.T) {
	feature := &rpcFeature{}
	transport.NewRouter(transport.Options{
		Config:  config.Default(),
		Checker: health.NewChecker(),
		Modules: []kernel.Module{feature},
	})

	_, handler := authnv1connect.NewAuthServiceHandler(
		stubAuthService{}, feature.options...)
	req := httptest.NewRequest(http.MethodPost,
		authnv1connect.AuthServiceSignInProcedure, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"access_expires_in":900`)
	assert.NotContains(t, rec.Body.String(), `"access_expires_in":"900"`)
}

// stubAuthService answers the smallest SignIn response that carries the field.
type stubAuthService struct{}

func (stubAuthService) SignIn(context.Context, *connect.Request[authnv1.SignInRequest]) (*connect.Response[authnv1.SignInResponse], error) {
	return connect.NewResponse(&authnv1.SignInResponse{
		AccessToken:     "token",
		TokenType:       "Bearer",
		AccessExpiresIn: 900,
	}), nil
}
