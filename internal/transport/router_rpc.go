package transport

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"connectrpc.com/validate"

	"github.com/go-chi/chi/v5"
	"github.com/riipandi/tango/internal/guard"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	notificationv1connect "github.com/riipandi/tango/codegen/proto/go/tango/notification/v1/notificationv1connect"
	systemv1connect "github.com/riipandi/tango/codegen/proto/go/tango/system/v1/systemv1connect"
	"github.com/riipandi/tango/internal/health"
	"github.com/riipandi/tango/internal/kernel"
	"github.com/riipandi/tango/internal/transport/middleware"
)

// RPCPath is the route prefix the ConnectRPC surface is mounted on. The SPA,
// the admin console, and internal tools call the generated clients below it.
const RPCPath = "/rpc"

// healthCheckPath is the full path the readiness procedure answers below the
// RPC prefix — the same readiness the REST probe answers at /api/healthz.
const healthCheckPath = RPCPath + systemv1connect.HealthServiceCheckProcedure

// The names the Connect protocol resolves a codec from, taken from the
// `Content-Type` a client sends. Both are registered, because a client may
// spell the JSON content type either way.
const (
	rpcCodecJSON            = "json"
	rpcCodecJSONCharsetUTF8 = "json; charset=utf-8"
)

// rpcStreamingProcedures lists the procedures whose response stays open
// past every request deadline: a server-streaming procedure is the one
// surface the write timeout is wrong for, so the middleware above the
// handlers lifts its deadline off the paths named here. A stream whose
// procedure is missing from the list is cut at the deadline instead of
// being answered unbounded, which is the safe side of a forgotten entry.
var rpcStreamingProcedures = []string{
	notificationv1connect.NotificationServiceWatchNotificationsProcedure,
}

// rpcJSONCodec adapts protojson to the contract the two transports share.
//
// protobuf's JSON mapping defaults to lowerCamelCase, which would make an RPC
// field (`statusCode`) and its REST twin (`status_code`) two spellings of one
// contract. `UseProtoNames` serializes under the declared proto field names
// instead, so both transports write snake_case and a client reads one
// vocabulary. Requests stay tolerant: protojson accepts either spelling on the
// way in, and unknown fields are discarded so a client is not pinned to the
// server's schema version.
//
// It is registered under the names connect's built-in JSON codecs use, which
// replaces them rather than adding a second JSON encoding beside them.
type rpcJSONCodec struct {
	name string
}

var _ connect.Codec = rpcJSONCodec{}

func (c rpcJSONCodec) Name() string { return c.name }

func (c rpcJSONCodec) Marshal(message any) ([]byte, error) {
	msg, err := protoMessage(message)
	if err != nil {
		return nil, err
	}
	return protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
}

func (c rpcJSONCodec) MarshalAppend(dst []byte, message any) ([]byte, error) {
	msg, err := protoMessage(message)
	if err != nil {
		return nil, err
	}
	return protojson.MarshalOptions{UseProtoNames: true}.MarshalAppend(dst, msg)
}

func (c rpcJSONCodec) Unmarshal(data []byte, message any) error {
	msg, err := protoMessage(message)
	if err != nil {
		return err
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data, msg)
}

// protoMessage checks the codec was handed a message it can serialize. A
// generated handler always passes one, so this reports a hand-registered
// service rather than a runtime condition.
func protoMessage(message any) (proto.Message, error) {
	msg, ok := message.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("rpc: %T is not a protobuf message", message)
	}
	return msg, nil
}

// otelInterceptor is the OpenTelemetry interceptor every procedure is served
// behind: it creates the server span a trace follows and records the rpc
// duration and size histograms. It reads the global providers the observer
// installs, so a signal that is switched off costs a no-op.
func otelInterceptor() connect.Interceptor {
	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		// New with no options cannot fail — the error guards an SDK the
		// repository pins — but the signature demands the check, and a broken
		// SDK is a build defect, not a configuration a run can survive.
		panic(fmt.Sprintf("transport: otelconnect: %s", err))
	}
	return interceptor
}

// rpcHandlerOptions are the options every service on this surface is
// registered with: the shared codec pair, the panic boundary, the validate
// interceptor, and the OpenTelemetry interceptor. A module that serves
// procedures receives them and passes them to every generated handler it
// registers, so a module's procedure answers exactly like the transport's
// own — the codec, the panic boundary, and the telemetry included.
//
// The read bound rides the same options, so a request body larger than
// server.max_request_bytes is refused before any procedure — or the
// authentication wrap in front of them — decodes it.
func rpcHandlerOptions(maxRequestBytes int, reauth guard.ReauthConsumer) []connect.HandlerOption {
	return []connect.HandlerOption{
		connect.WithCodec(rpcJSONCodec{name: rpcCodecJSON}),
		connect.WithCodec(rpcJSONCodec{name: rpcCodecJSONCharsetUTF8}),
		connect.WithReadMaxBytes(maxRequestBytes),
		// Authorization runs before the contract is enforced, so a caller who
		// may not run a procedure is refused without the request body being
		// judged: the answer is the same whether the body was well-formed or
		// not, which keeps a non-administrator from telling an administrative
		// procedure apart from an absent one. The rule is per-procedure and
		// the request carries the procedure it calls, so this is an
		// interceptor rather than a middleware.
		connect.WithInterceptors(middleware.Guard(reauth)),
		// The declarative constraints in the contracts are enforced here,
		// once: a message that fails its protovalidate options never reaches
		// a handler, and the violations travel as typed error details.
		connect.WithInterceptors(validate.NewInterceptor()),
		connect.WithRecover(func(_ context.Context, _ connect.Spec, _ http.Header, recovered any) error {
			// The recovery middleware above this surface answers a panic with
			// the REST envelope, which a Connect client cannot parse. A panic
			// inside a procedure is reported in the protocol the caller used,
			// and the panic value is not published: it is an internal detail,
			// and the middleware logs it with the stack and the request id.
			return connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}),
		connect.WithInterceptors(otelInterceptor()),
	}
}

// mountRPC registers the ConnectRPC surface on the router. It receives
// exactly what the surface serves — the checker, the authenticator, and the
// modules — so the RPC registration never reads how the router got its
// dependencies.
func mountRPC(r chi.Router, checker *health.Checker, auth Authenticator, modules []kernel.Module, maxRequestBytes int, reauth guard.ReauthConsumer) {
	// The prefix is stripped because chi only shifts its own route context: a
	// generated Connect handler matches its procedure path exactly.
	r.Mount(RPCPath, http.StripPrefix(RPCPath, rpcRouter(checker, auth, modules, maxRequestBytes, reauth)))
}

// rpcRouter builds the Connect handler tree served below RPCPath.
//
// Every procedure is registered at its own path rather than the service's
// shared subtree prefix: the generated handler answers a path under its prefix
// it does not know with a plain-text 404, which a Connect client cannot read.
// Registering the procedures keeps the not-found boundary in one place, where
// the answer is written in the protocol the caller used.
//
// The path is registered for every method on purpose. A procedure is POST-only
// (no procedure in this contract declares `idempotency_level =
// NO_SIDE_EFFECTS`, which is what would make a GET legal), and the generated
// handler is what refuses another method with `405` and `Allow: POST`.
func rpcRouter(checker *health.Checker, auth Authenticator, modules []kernel.Module, maxRequestBytes int, reauth guard.ReauthConsumer) http.Handler {
	r := chi.NewRouter()

	// The streaming procedures are lifted out of the request deadlines the
	// chain above them sets: a stream's whole job is to outlive the timeouts
	// a unary call is bounded by. The paths are the contract's own, so a
	// renamed procedure breaks the build rather than losing its exemption.
	r.Use(middleware.UnboundedFor(rpcStreamingProcedures...))

	options := rpcHandlerOptions(maxRequestBytes, reauth)
	_, healthHandler := systemv1connect.NewHealthServiceHandler(
		newRPCHealthService(checker),
		options...,
	)
	r.Handle(systemv1connect.HealthServiceCheckProcedure, healthHandler)

	// A module's procedures mount beside the transport's own, on the same
	// router, so they share the codec and the not-found boundary.
	kernel.MountRPC(r, options, modules...)

	// The error writer owns the wire form of a miss, so the answer carries the
	// code and the HTTP status the Connect specification assigns it rather
	// than a shape invented here or the SPA document.
	writer := connect.NewErrorWriter(options...)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeRPCError(writer, w, r, connect.CodeUnimplemented,
			fmt.Sprintf("unknown procedure %q", r.URL.Path))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeRPCError(writer, w, r, connect.CodeUnimplemented,
			fmt.Sprintf("procedure %q does not accept %s", r.URL.Path, r.Method))
	})

	// Authentication wraps the finished tree, so a refusal happens before the
	// request is decoded and an unknown procedure is not disclosed to a
	// caller without a token.
	return middleware.BearerAuth(auth, rpcPublicProcedures, options, r)
}

// rpcRefuseWith answers a limited RPC request in its own protocol. The bound
// is threaded so the error writer spells refusals with the same options the
// procedures were registered with.
func rpcRefuseWith(maxRequestBytes int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		rpcRefuse(w, r, maxRequestBytes)
	}
}

// writeRPCError answers a request that reached no procedure, in the protocol the caller used.
func writeRPCError(writer *connect.ErrorWriter, w http.ResponseWriter, r *http.Request, code connect.Code, message string) {
	_ = writer.Write(w, r, connect.NewError(code, errors.New(message)))
}

// rpcRefuse answers a limited procedure call in the protocol the caller used:
// `resource_exhausted`, the code the Connect specification maps to 429. The
// X-RateLimit-* and Retry-After headers are already on the response — the
// middleware wrote them before refusing. The error writer is built from the
// same handler options the procedures are registered with, so the refusal is
// serialized under the shared codec, exactly like a refusal from a procedure
// itself.
func rpcRefuse(w http.ResponseWriter, r *http.Request, maxRequestBytes int) {
	options := rpcHandlerOptions(maxRequestBytes, nil)
	writer := connect.NewErrorWriter(options...)
	writeRPCError(writer, w, r, connect.CodeResourceExhausted, "rate limit exceeded")
}
