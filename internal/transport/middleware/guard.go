package middleware

import (
	"context"
	"errors"
	"net/http"

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
//
// The step-up enforcer rides construction: a procedure the table marks as
// Reauthenticated has its proof spent here, after the rule passes and before
// the procedure runs. A nil enforcer fails closed — the guarded procedure
// refuses rather than runs unproven — which is the state a bare test router
// is in.
func Guard(reauth guard.ReauthConsumer) connect.Interceptor {
	return guardInterceptor{reauth: reauth}
}

// guardInterceptor carries the rule table over both procedure shapes. The
// unary half reads the decoded request, so a self rule can compare the
// identifier it names; the streaming half reads no message — a stream's
// rules are the ones that need no target, and a self rule on a stream is a
// table defect the guard's table test catches.
type guardInterceptor struct {
	// reauth spends the step-up proof a Reauthenticated procedure demands.
	// Nil fails closed, at the check below rather than at a dereference.
	reauth guard.ReauthConsumer
}

func (i guardInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := i.enforce(ctx, req.Spec().Procedure, req.Any(), req.Header()); err != nil {
			return nil, guardError(err)
		}
		return next(ctx, req)
	}
}

func (i guardInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i guardInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := i.enforce(ctx, conn.Spec().Procedure, nil, conn.RequestHeader()); err != nil {
			return guardError(err)
		}
		return next(ctx, conn)
	}
}

// enforce runs the rule one procedure gets, before the procedure runs, so a
// refusal costs no service call and reaches no database. A procedure the
// table marks for step-up then has its proof spent — the one consumption
// point, so the requirement is judged identically everywhere.
func (i guardInterceptor) enforce(ctx context.Context, procedure string, message any, header http.Header) error {
	rule := guard.RuleFor(procedure)
	caller, _ := jwtutils.CallerFrom(ctx)
	if err := rule(caller, guard.Target{Message: message}); err != nil {
		return err
	}
	if !guard.RequiresReauthentication(procedure) {
		return nil
	}
	if i.reauth == nil {
		return guard.ErrUnauthenticated
	}
	token := header.Get(guard.ReauthenticationHeader)
	if token == "" {
		return guard.ErrUnauthenticated
	}
	// The consumer's own error text stays inside: a spent, foreign, and
	// unknown proof answer the same unauthenticated refusal, the way the
	// guard's other refusals do.
	if err := i.reauth.ConsumeReauthentication(ctx, caller, token); err != nil {
		return guard.ErrUnauthenticated
	}
	return nil
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
