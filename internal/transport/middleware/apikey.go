package middleware

import (
	"context"
	"net/http"

	"connectrpc.com/connect/v2"

	"github.com/riipandi/saka/pkg/jwtutils"
)

// apiKeyHeader is the header a machine credential travels under.
const apiKeyHeader = "X-API-Key"

// KeyAuthenticator is the machine-credential half of the authwall: the
// service that turns a presented API key into the caller it acts as. It is
// an interface the middleware defines rather than a module it imports — the
// middleware carries the mechanism, the area that owns the credential
// carries the rules, and the composition root joins the two.
type KeyAuthenticator interface {
	AuthenticateAPIKey(ctx context.Context, presented string) (*jwtutils.Caller, error)
}

// APIKeyAuth wraps an authenticator with the machine credential: a request
// that carries the API key header is answered through the key service, and
// every other request — a bearer token, or none — is answered by the
// authenticator it wraps.
//
// The bearer takes precedence when both headers arrive: an explicit token is
// the credential the caller named first, and a key that rode along silently
// must not rescue a bad one. The wrapped authenticator still answers the
// refusal for a request that carries neither, so the shape of "no
// credential" has one author.
//
// The wrapper composes at the composition root, where both halves are
// resolved; the middleware itself imports no module.
func APIKeyAuth(auth Authenticator, machine KeyAuthenticator) Authenticator {
	return func(ctx context.Context, req *http.Request) (any, error) {
		if _, ok := jwtutils.BearerToken(req); ok {
			return auth(ctx, req)
		}
		if presented := req.Header.Get(apiKeyHeader); presented != "" && machine != nil {
			caller, err := machine.AuthenticateAPIKey(ctx, presented)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, "invalid or expired token")
			}
			return caller, nil
		}
		return auth(ctx, req)
	}
}
