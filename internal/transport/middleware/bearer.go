package middleware

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/authn"
	"connectrpc.com/connect"

	"github.com/riipandi/tango/internal/guard"
	"github.com/riipandi/tango/pkg/responder"
)

// Authenticator authenticates one request and answers the identity the
// procedures read from the context. It is the authn library's own shape, so
// the middleware below and the transport that mounts it share one type.
type Authenticator = authn.AuthFunc

// BearerAuth wraps the RPC surface with authentication. The authn middleware
// runs before a request is decoded — an unauthenticated call costs no
// unmarshal — and it receives the same handler options the procedures are
// registered with, so a refusal is marshaled in the protocol the caller used.
//
// A procedure named in public is answered without a caller; every other path
// requires one, so a new procedure is protected by default and a public one
// is a deliberate entry in the guard table. A nil authenticator returns the
// handler unchanged, which is the state a test that reads only responses is
// in.
//
// Authentication is the whole of this middleware's job: whether the caller
// may run the procedure is the guard interceptor's decision, made once the
// request is decoded and the target it names is readable.
func BearerAuth(auth Authenticator, public map[string]struct{}, options []connect.HandlerOption, inner http.Handler) http.Handler {
	if auth == nil {
		return inner
	}
	return authn.NewMiddleware(publicOnly(auth, public), options...).Wrap(inner)
}

// publicOnly excuses the public procedures from the authenticator. An unknown
// path is not public, so it is refused as unauthenticated rather than
// unimplemented — the refusal hides which procedures exist from a caller
// without a token.
func publicOnly(auth Authenticator, public map[string]struct{}) Authenticator {
	return func(ctx context.Context, req *http.Request) (any, error) {
		if procedure, ok := authn.InferProcedure(req.URL); ok {
			if _, isPublic := public[procedure]; isPublic {
				return nil, nil
			}
		}
		return auth(ctx, req)
	}
}

// RESTBearer guards the REST surface's routes: it authenticates the caller
// and applies the rule the guard table declares for the route, in one pass.
//
// The two steps are one middleware rather than two because they share the
// table. A route whose rule is public is served without a caller — the key
// set, a picture fetched by an <img> tag — and every other route requires
// one, so a new route is protected by default and a public one is a
// deliberate entry in internal/guard.
//
// The verified caller travels through the context the authn library reads,
// the same store the RPC surface's middleware fills, so a handler reads its
// caller the same way on both transports. A refusal is the REST envelope,
// because the caller here is one that reads envelopes.
//
// A nil authenticator answers with the inner handler unchanged, which is the
// state a test that reads only responses is in.
func RESTBearer(auth Authenticator, rules []guard.RestEntry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if auth == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rule, target := guard.MatchRest(rules, r.Method, r.URL.Path)
			if guard.IsPublic(rule) {
				next.ServeHTTP(w, r)
				return
			}

			info, err := auth(r.Context(), r)
			if err != nil {
				responder.Fail(w, r, http.StatusUnauthorized, "authentication required")
				return
			}

			ctx := authn.SetInfo(r.Context(), info)
			if err := rule(guard.CallerOf(info), target); err != nil {
				refuseREST(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// refuseREST writes a rule's refusal in the envelope the REST surface answers.
//
// The mapping is the one the RPC surface applies, expressed in status codes:
// a missing caller is 401 because the answer is to present a credential, and
// every other refusal is 404 — the answer that discloses least, because a
// caller without the role cannot tell an administrative route from an absent
// one, and a caller naming another account learns nothing about whether the
// account exists.
func refuseREST(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, guard.ErrUnauthenticated) {
		responder.Fail(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	responder.Fail(w, r, http.StatusNotFound, "not found")
}
