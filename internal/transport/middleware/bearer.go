package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/internal/guard"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// Authenticator authenticates one request and answers the identity the
// procedures read from the context. The middleware below and the transport
// that mounts it share the one type.
type Authenticator func(ctx context.Context, req *http.Request) (any, error)

// bearerRefusals owns the wire form of an authentication refusal, the way the
// mount owns the wire form of every other miss: the answer carries the code
// and the HTTP status the Connect specification assigns it, in the protocol
// the caller used.
var bearerRefusals = connecthttp.NewErrorWriter()

// BearerAuth wraps the RPC surface with authentication. The middleware runs
// before a request is decoded — an unauthenticated call costs no unmarshal —
// and its refusal is marshaled in the protocol the caller used.
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
func BearerAuth(auth Authenticator, public map[string]struct{}, inner http.Handler) http.Handler {
	if auth == nil {
		return inner
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A public procedure answers without a caller. A bearer presented on
		// one is still verified, best-effort: the optional-session shapes —
		// the MFA enrollment's session-or-bridge caller — read the claims
		// when the request carries them and answer the public refusal when it
		// does not. An unknown path is not public, so it is refused as
		// unauthenticated rather than unimplemented — the refusal hides which
		// procedures exist from a caller without a token.
		info, err := auth(r.Context(), r)
		if err != nil {
			if procedure, ok := inferProcedure(r.URL); ok {
				if _, isPublic := public[procedure]; isPublic {
					inner.ServeHTTP(w, r)
					return
				}
			}
			refuseBearer(w, r, err)
			return
		}
		inner.ServeHTTP(w, r.WithContext(jwtutils.SetInfo(r.Context(), info)))
	})
}

// refuseBearer answers a request that presented no usable credential, in the
// protocol the caller used. The authenticator's own *connect.Error carries
// the code; anything else is an unauthenticated refusal, so a refusal is
// always written on purpose.
func refuseBearer(w http.ResponseWriter, r *http.Request, err error) {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		cerr = connect.NewError(connect.CodeUnauthenticated, "authentication required")
	}
	_ = bearerRefusals.Write(w, r, cerr)
}

// inferProcedure reports the "/service/Method" suffix a request path ends
// with — the name the guard table and the public list key on.
func inferProcedure(u *url.URL) (string, bool) {
	ultimate := strings.LastIndex(u.Path, "/")
	penultimate := strings.LastIndex(u.Path[:ultimate], "/")
	if ultimate < 0 || penultimate < 0 || ultimate == len(u.Path)-1 || penultimate == ultimate-1 {
		return "", false
	}
	return u.Path[penultimate:], true
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
// The verified caller travels through the context store `pkg/jwtutils` owns,
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

			// A public route never refuses a caller. A caller who presents a
			// token anyway is still verified, so the handler sees who asked:
			// the configuration read widens its answer for an administrator
			// this way. A token that does not verify is the anonymous case —
			// the route is public, so there is nothing the credential could
			// have added, and no refusal either.
			if guard.IsPublic(rule) {
				if r.Header.Get("Authorization") == "" {
					next.ServeHTTP(w, r)
					return
				}
				info, err := auth(r.Context(), r)
				if err != nil {
					next.ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r.WithContext(jwtutils.SetInfo(r.Context(), info)))
				return
			}

			info, err := auth(r.Context(), r)
			if err != nil {
				webutil.Fail(w, r, http.StatusUnauthorized, "authentication required")
				return
			}

			ctx := jwtutils.SetInfo(r.Context(), info)
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
		webutil.Fail(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	webutil.Fail(w, r, http.StatusNotFound, "not found")
}
