package identity

import (
	"context"
	"net/http"

	"connectrpc.com/connect/v2"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// Authenticate verifies the Bearer token a request carries and answers the
// caller's claims. It is the bearer half of the authwall the transport-wide
// middleware runs: the key and algorithm resolve on every call through the
// area's key service, so a rotation is picked up without a restart, and the
// verifier is built with that exact pair, so the accepted algorithm is pinned
// by construction.
//
// The machine credential is the transport's composition, not this function's:
// the bearer answers the header it knows, the API-key wrapper beside it
// answers the one it knows, and the composition root joins the two.
//
// The failure text names nothing a caller could aim at: a missing token and a
// bad one answer alike.
func Authenticate(keys *jwks.Service, cfg config.Config) func(context.Context, *http.Request) (any, error) {
	verifier := jwtutils.NewAccessVerifier(keys, cfg.Auth.Issuer)
	return func(ctx context.Context, req *http.Request) (any, error) {
		token, ok := jwtutils.BearerToken(req)
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, "authentication required")
		}
		caller, err := verifier.VerifyCaller(ctx, token)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, "invalid or expired token")
		}
		return caller, nil
	}
}
