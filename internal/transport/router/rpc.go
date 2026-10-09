package router

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"connectrpc.com/otelconnect"
	"connectrpc.com/validate"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/riipandi/saka/internal/guard"

	notificationv1connect "github.com/riipandi/saka/codegen/proto/go/saka/notification/v1/notificationv1connect"
	systemv1connect "github.com/riipandi/saka/codegen/proto/go/saka/system/v1/systemv1connect"
	"github.com/riipandi/saka/framework/kernel"
	fwmiddleware "github.com/riipandi/saka/framework/middleware"
	"github.com/riipandi/saka/internal/transport/handler"
	"github.com/riipandi/saka/internal/transport/middleware"
)

// Authenticator authenticates an RPC request before its procedure runs. The
// router receives it through Options rather than resolving the key service
// itself: verification material belongs to the identity area, and the
// composition root is what joins the two halves.
type Authenticator = middleware.Authenticator

var rpcRateLimitExclusions = []string{
	healthCheckPath, // the same readiness a monitor watches over ConnectRPC
}

// rpcPublicProcedures lists the procedures the RPC surface answers without a
// caller, read from the same guard table the authorization interceptor uses,
// so a procedure cannot be public for the authenticator and administrative
// for the guard. The bearer middleware refuses every other path, so a new
// procedure is protected by default and a public one is a deliberate entry in
// internal/guard.
var rpcPublicProcedures = guard.PublicProcedures()

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

func (c rpcJSONCodec) MarshalWrite(_ context.Context, dst io.Writer, message any) error {
	msg, err := protoMessage(message)
	if err != nil {
		return err
	}
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = dst.Write(data)
	return err
}

func (c rpcJSONCodec) UnmarshalRead(_ context.Context, src io.Reader, message any) error {
	msg, err := protoMessage(message)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(src)
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
func otelInterceptor() connect.ServerInterceptor {
	interceptor, err := otelconnect.NewServerInterceptor()
	if err != nil {
		// New with no options cannot fail — the error guards an SDK the
		// repository pins — but the signature demands the check, and a broken
		// SDK is a build defect, not a configuration a run can survive.
		panic(fmt.Sprintf("transport: otelconnect: %s", err))
	}
	return interceptor
}

// recoveryInterceptor is the panic boundary every procedure is served behind.
// The recovery middleware above this surface answers a panic with the REST
// envelope, which a Connect client cannot parse. A panic inside a procedure
// is reported in the protocol the caller used, and the panic value is not
// published: it is an internal detail, and the middleware logs it with the
// stack and the request id. The net/http abort sentinel passes through —
// the server owns that panic and aborting the response is its answer.
func recoveryInterceptor(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				err = connect.NewError(connect.CodeInternal, "internal error")
			}
		}()
		return next(ctx, spec, stream)
	}
}

// rpcInterceptors are the interceptors every procedure on this surface is
// served behind, in wrap order: the panic boundary outermost, then the guard
// — authorization runs before the contract is enforced, so a caller who may
// not run a procedure is refused without the request body being judged — then
// the declarative constraints, whose refusals carry typed protovalidate
// details, and the OpenTelemetry interceptor innermost.
func rpcInterceptors(reauth guard.ReauthConsumer) []connect.ServerInterceptor {
	return []connect.ServerInterceptor{
		recoveryInterceptor,
		middleware.Guard(reauth),
		validate.NewServerInterceptor(),
		otelInterceptor(),
	}
}

// rpcMountOptions are the transport options every procedure on this surface
// is mounted with: the shared codec set and the read bound. The read bound
// refuses a request body larger than server.max_request_bytes before any
// procedure — or the authentication wrap in front of them — decodes it.
func rpcMountOptions(maxRequestBytes int) []connecthttp.Option {
	return []connecthttp.Option{
		connecthttp.WithCodecs(
			rpcJSONCodec{name: rpcCodecJSON},
			rpcJSONCodec{name: rpcCodecJSONCharsetUTF8},
			connectproto.NewBinaryCodec(),
		),
		connecthttp.WithReadMaxBytes(maxRequestBytes),
	}
}

// mountRPC registers the ConnectRPC surface on the router. It receives the
// whole Options value, so the RPC registration never reads how the router
// got its dependencies.
func mountRPC(r chi.Router, opts Options) {
	// The prefix is stripped because chi only shifts its own route context: a
	// generated Connect handler matches its procedure path exactly.
	r.Mount(RPCPath, http.StripPrefix(RPCPath, rpcRouter(opts)))
}

// rpcRouter builds the Connect handler tree served below RPCPath.
//
// Every procedure is registered on one *connect.Server; the mount installs
// one route per procedure on the router. The mount also answers a request
// naming a registered service with the protocol's own refusal, so the
// not-found boundary stays in one place — the error writer below — where the
// answer is written in the protocol the caller used.
//
// The route accepts every method on purpose. A procedure is POST-only (no
// procedure in this contract declares `idempotency_level =
// NO_SIDE_EFFECTS`, which is what would make a GET legal), and the mounted
// handler is what refuses another method with `405` and `Allow: POST`.
func rpcRouter(opts Options) http.Handler {
	checker := opts.Checker
	auth := opts.Authenticator
	modules := opts.Modules
	maxRequestBytes := opts.Config.Server.MaxRequestBytes
	reauth := opts.Reauthentication

	r := chi.NewRouter()

	// The streaming procedures are lifted out of the request deadlines the
	// chain above them sets: a stream's whole job is to outlive the timeouts
	// a unary call is bounded by. The paths are the contract's own, so a
	// renamed procedure breaks the build rather than losing its exemption.
	r.Use(fwmiddleware.UnboundedFor(rpcStreamingProcedures...))

	server := connect.NewServer(rpcInterceptors(reauth)...)
	systemv1connect.RegisterHealthServiceHandler(server, handler.NewRPCHealthService(checker))

	// The engines' own administrative surface: the queue's tables and the
	// scheduler's state rows an operations console reads and acts on. The
	// guard names every procedure administrative, so an anonymous miss is a
	// 404 before the handler ever runs.
	systemv1connect.RegisterQueueServiceHandler(
		server,
		handler.NewRPCQueueService(opts.QueueClient, opts.DB, opts.Audit, opts.Logger),
	)
	systemv1connect.RegisterSchedulerServiceHandler(
		server,
		handler.NewRPCSchedulerService(opts.Scheduler, opts.DB, opts.Audit, opts.Logger),
	)

	// A module's procedures register beside the transport's own, on the same
	// server, so they share the interceptors, the codec, and the not-found
	// boundary.
	kernel.MountRPC(server, modules...)

	mountOptions := rpcMountOptions(maxRequestBytes)
	connecthttp.Mount(r, server, mountOptions...)

	// The error writer owns the wire form of a miss, so the answer carries the
	// code and the HTTP status the Connect specification assigns it rather
	// than a shape invented here or the SPA document.
	writer := connecthttp.NewErrorWriter(mountOptions...)
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
	return middleware.BearerAuth(auth, rpcPublicProcedures, r)
}

// rpcRefuseWith answers a limited RPC request in its own protocol. The bound
// is threaded so the error writer spells refusals with the same options the
// procedures were mounted with.
func rpcRefuseWith(maxRequestBytes int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		rpcRefuse(w, r, maxRequestBytes)
	}
}

// writeRPCError answers a request that reached no procedure, in the protocol the caller used.
func writeRPCError(writer *connecthttp.ErrorWriter, w http.ResponseWriter, r *http.Request, code connect.Code, message string) {
	_ = writer.Write(w, r, connect.NewError(code, message))
}

// rpcRefuse answers a limited procedure call in the protocol the caller used:
// `resource_exhausted`, the code the Connect specification maps to 429. The
// X-RateLimit-* and Retry-After headers are already on the response — the
// middleware wrote them before refusing. The error writer is built from the
// same transport options the procedures were mounted with, so the refusal is
// serialized exactly like a refusal from a procedure itself.
func rpcRefuse(w http.ResponseWriter, r *http.Request, maxRequestBytes int) {
	writer := connecthttp.NewErrorWriter(rpcMountOptions(maxRequestBytes)...)
	writeRPCError(writer, w, r, connect.CodeResourceExhausted, "rate limit exceeded")
}
