package middleware

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"

	"github.com/riipandi/saka/internal/guard"
	"github.com/riipandi/saka/pkg/jwtutils"
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
// router would have to repeat. It is registered on the server, so a module's
// procedure is guarded exactly like the transport's own.
//
// The step-up enforcer rides construction: a procedure the table marks as
// Reauthenticated has its proof spent here, after the rule passes and before
// the procedure runs. A nil enforcer fails closed — the guarded procedure
// refuses rather than runs unproven — which is the state a bare test router
// is in.
//
// The interceptor wraps the procedure's stream and judges the rule on the
// first message the stream yields: a unary procedure receives exactly one,
// so its rule reads the message a self rule compares an identifier against;
// a streaming procedure's rules are the ones that need no target — a self
// rule on a stream is a table defect the guard's table test catches — so the
// first event carries no judgment.
func Guard(reauth guard.ReauthConsumer) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			header, _ := connect.CallInfoForServerContext(ctx)
			guarded := &guardStream{
				ServerStream: stream,
				ctx:          ctx,
				procedure:    spec.Procedure,
				header:       header,
				reauth:       reauth,
			}
			return next(ctx, spec, guarded)
		}
	}
}

// guardStream is the procedure's stream with the rule table riding its first
// receive. The zero Message judgment happens once: the request is a single
// message on a unary call, and a stream's messages carry no rule target, so
// the first Receive settles the caller's standing for the whole call.
type guardStream struct {
	connect.ServerStream

	ctx       context.Context
	procedure string
	header    *connect.CallInfo
	reauth    guard.ReauthConsumer
	enforced  bool
}

func (s *guardStream) Receive(msg any) error {
	if err := s.ServerStream.Receive(msg); err != nil {
		return err
	}
	if s.enforced {
		return nil
	}
	s.enforced = true
	if err := s.enforce(msg); err != nil {
		return guardError(err)
	}
	return nil
}

// enforce runs the rule one procedure gets, before the procedure runs, so a
// refusal costs no service call and reaches no database. A procedure the
// table marks for step-up then has its proof spent — the one consumption
// point, so the requirement is judged identically everywhere.
func (s *guardStream) enforce(message any) error {
	rule := guard.RuleFor(s.procedure)
	caller, _ := jwtutils.CallerFrom(s.ctx)
	if err := rule(caller, guard.Target{Message: message}); err != nil {
		return err
	}
	if !guard.RequiresReauthentication(s.procedure) {
		return nil
	}
	if s.reauth == nil {
		return guard.ErrUnauthenticated
	}
	token := s.header.RequestHeader().Get(guard.ReauthenticationHeader)
	if token == "" {
		return guard.ErrUnauthenticated
	}
	// The consumer's own error text stays inside: a spent, foreign, and
	// unknown proof answer the same unauthenticated refusal, the way the
	// guard's other refusals do.
	if err := s.reauth.ConsumeReauthentication(s.ctx, caller, token); err != nil {
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
		return connect.NewError(connect.CodeUnauthenticated, "authentication required")
	default:
		return connect.NewError(connect.CodeNotFound, "not found")
	}
}
