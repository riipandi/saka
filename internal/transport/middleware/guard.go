package middleware

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/riipandi/tango/internal/guard"
	"github.com/riipandi/tango/pkg/jwtutils"
)

// Guard is the authorization interceptor every procedure on the RPC surface
// is served behind.
//
// It runs the rule the guard table declares for the procedure, after the
// authenticator has resolved the caller and after the request is decoded —
// a self rule compares an identifier, so it needs the message — and before
// the procedure runs, so a refusal costs no service call and reaches no
// database.
//
// Being an interceptor rather than a middleware is what makes the rule
// per-procedure: the request carries the procedure it calls, so one
// interceptor answers for the whole surface instead of a path table the
// router would have to repeat. It is registered on the handler options, so a
// module's procedure is guarded exactly like the transport's own.
func Guard() connect.Interceptor {
	return guardInterceptor{}
}

// guardInterceptor carries the rule table over both procedure shapes. The
// unary half reads the decoded request, so a self rule can compare the
// identifier it names; the streaming half reads no message — a stream's
// rules are the ones that need no target, and a self rule on a stream is a
// table defect the guard's table test catches.
type guardInterceptor struct{}

func (guardInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := enforce(ctx, req.Spec().Procedure, req.Any()); err != nil {
			return nil, guardError(err)
		}
		return next(ctx, req)
	}
}

func (guardInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (guardInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := enforce(ctx, conn.Spec().Procedure, nil); err != nil {
			return guardError(err)
		}
		return next(ctx, conn)
	}
}

// enforce runs the rule one procedure gets, before the procedure runs, so a
// refusal costs no service call and reaches no database.
func enforce(ctx context.Context, procedure string, message any) error {
	rule := guard.RuleFor(procedure)
	caller, _ := jwtutils.CallerFrom(ctx)
	return rule(caller, guard.Target{Message: message})
}

// guardError maps a rule's refusal onto the wire.
//
// A missing caller is `unauthenticated`, because the answer is to present a
// credential. Every other refusal is `not_found`, which is the shape that
// discloses least: a caller who may not act on an account learns nothing
// about whether it exists, and a caller without the role cannot tell an
// administrative procedure from an absent one. The distinction survives in
// the audit record and the server log, which an operator reads.
func guardError(err error) error {
	switch {
	case errors.Is(err, guard.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	default:
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
}
