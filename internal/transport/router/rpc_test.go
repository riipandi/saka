package router_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authnv1 "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1"
	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	systemv1 "github.com/riipandi/saka/codegen/proto/go/saka/system/v1"
	"github.com/riipandi/saka/codegen/proto/go/saka/system/v1/systemv1connect"
	"github.com/riipandi/saka/framework/health"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/config"
	rt "github.com/riipandi/saka/internal/transport/router"
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
		rt.RPCPath+procedure, strings.NewReader(body))
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
	return rt.NewRouter(rt.Options{
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
	router := rt.NewRouter(rt.Options{
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
		rt.RPCPath+"/saka.system.v1.HealthService/Check", nil))

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

	client := systemv1connect.NewHealthServiceClient(
		connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL+rt.RPCPath)))
	ctx, info := connect.NewClientContext(t.Context())
	resp, err := client.Check(ctx, &systemv1.CheckRequest{})
	require.NoError(t, err)

	assert.Equal(t, "healthy", resp.GetStatus())
	assert.True(t, strings.HasPrefix(info.ResponseHeader().Get("X-Request-Id"), "req_"),
		"the generated client reads the correlation id from the response header")
	assert.Len(t, info.ResponseHeader().Values("X-Request-Id"), 1,
		"the generated client must read one id, not a duplicated pair")
}

// rpcFeature is a module that serves a procedure, the way an application
// module does once it has a contract. It registers the generated sign-in
// handler on whatever server it is handed — a service the transport's own
// registration does not claim — so the tests prove the transport's shared
// codec and interceptors reach a module's procedure.
type rpcFeature struct{}

func (rpcFeature) Name() string { return "rpc-feature" }

func (rpcFeature) Mount(chi.Router) {}

func (rpcFeature) MountRPC(server *connect.Server) {
	authnv1connect.RegisterAuthServiceHandler(server, stubAuthService{})
}

// rpcFailingFeature is the same feature with a service that fails on a plain
// error — the non-coded failure a database driver leaves behind, not one the
// handler authored. The masking test needs exactly this shape on the wire.
type rpcFailingFeature struct{}

func (rpcFailingFeature) Name() string { return "rpc-failing-feature" }

func (rpcFailingFeature) Mount(chi.Router) {}

func (rpcFailingFeature) MountRPC(server *connect.Server) {
	err := errors.New("sql: duplicate key value violates unique constraint \"accounts_username_key\"")
	authnv1connect.RegisterAuthServiceHandler(server, stubAuthService{fail: err})
}

// TestAModuleProcedureMountsBelowThePrefix pins the seam a feature uses to add
// a procedure: it registers on the same server as the transport's own
// services, and the mount serves it below the prefix, with the prefix already
// stripped.
func TestAModuleProcedureMountsBelowThePrefix(t *testing.T) {
	router := rt.NewRouter(rt.Options{
		Config:  config.Default(),
		Checker: health.NewChecker(),
		Modules: []kernel.Module{rpcFeature{}},
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.AuthServiceSignInProcedure, "{}"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"access_expires_in"`,
		"a module's procedure must serialize under the proto field names")
}

// TestModuleProcedureGetsTheSnakeCaseCodec is the reason the shared codec
// exists: a module that registers on the transport's server answers in the
// same field names the transport's own services do. The negative control
// mounts the same handler on a server with the default codecs — without it
// the assertion above would also pass on a codec that never ran, which is
// what makes it a real check.
func TestModuleProcedureGetsTheSnakeCaseCodec(t *testing.T) {
	router := rt.NewRouter(rt.Options{
		Config:  config.Default(),
		Checker: health.NewChecker(),
		Modules: []kernel.Module{rpcFeature{}},
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.AuthServiceSignInProcedure, "{}"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"access_expires_in":900`)
	assert.NotContains(t, rec.Body.String(), `"access_expires_in":"900"`)

	plain := connect.NewServer()
	authnv1connect.RegisterAuthServiceHandler(plain, stubAuthService{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, plain)

	controlReq := httptest.NewRequest(http.MethodPost,
		authnv1connect.AuthServiceSignInProcedure, strings.NewReader("{}"))
	controlReq.Header.Set("Content-Type", "application/json")
	controlReq.Header.Set("Connect-Protocol-Version", "1")

	control := httptest.NewRecorder()
	mux.ServeHTTP(control, controlReq)
	assert.Contains(t, control.Body.String(), `"accessExpiresIn"`,
		"the default mapping is camelCase, so the shared codec is what changes it")
}

// TestTheCodecWritesALifetimeAsANumber pins the int32 answer: protobuf's JSON
// mapping writes a 64-bit integer as a string, and a token lifetime arriving
// as `"access_expires_in":"900"` is what a client reading an OpenAPI-shaped
// response cannot parse. The shared codec serializes the 32-bit field as a
// number.
func TestTheCodecWritesALifetimeAsANumber(t *testing.T) {
	router := rt.NewRouter(rt.Options{
		Config:  config.Default(),
		Checker: health.NewChecker(),
		Modules: []kernel.Module{rpcFeature{}},
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.AuthServiceSignInProcedure, "{}"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"access_expires_in":900`)
	assert.NotContains(t, rec.Body.String(), `"access_expires_in":"900"`)
}

// TestAPlainErrorIsMasked pins the wire verdict v2 hands a service's plain
// error: the code is `unknown` and the message carries nothing — a driver
// error's SQL fragments and constraint names are not a caller's to read. The
// cause stays server-side, which is the improvement over v1's habit of
// publishing err.Error(). A handler that wants a message authors a
// *connect.Error, and those keep their text (every refusal test in this
// package is the proof).
func TestAPlainErrorIsMasked(t *testing.T) {
	router := rt.NewRouter(rt.Options{
		Config:  config.Default(),
		Checker: health.NewChecker(),
		Modules: []kernel.Module{rpcFailingFeature{}},
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t, authnv1connect.AuthServiceSignInProcedure, "{}"))

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "unknown", body.Code)
	assert.Empty(t, body.Message, "a plain error's text must not reach the wire")
	assert.NotContains(t, rec.Body.String(), "constraint")
}

// stubAuthService answers the smallest SignIn response that carries the field.
// A stub may carry a plain (non-connect) failure to answer with instead: the
// masking test needs a service error that is not a coded one.
type stubAuthService struct{ fail error }

func (s stubAuthService) SignIn(context.Context, *authnv1.SignInRequest) (*authnv1.SignInResponse, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	return &authnv1.SignInResponse{
		AccessToken:     "token",
		TokenType:       "Bearer",
		AccessExpiresIn: 900,
	}, nil
}
