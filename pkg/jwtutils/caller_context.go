package jwtutils

import (
	"context"
	"net/http"
	"strings"
)

// infoKey is the context store the authenticated caller travels through.
type infoKey struct{}

// SetInfo attaches the authenticated caller a seam verified to the context.
// The authenticator middleware and the guard interceptor share this store, so
// a handler reads its caller the same way on both transports.
func SetInfo(ctx context.Context, info any) context.Context {
	if info == nil {
		return ctx
	}
	return context.WithValue(ctx, infoKey{}, info)
}

// GetInfo retrieves the authentication information a seam attached, if any.
func GetInfo(ctx context.Context) any {
	return ctx.Value(infoKey{})
}

// CallerFrom reads the authenticated caller a seam attached to the context.
//
// The caller travels through this package's own context store, which the
// authenticator middleware and the guard interceptor already share. Reading
// it through this function keeps one type in play: a feature asks for the
// caller and gets the subject, the claims, and the delegation together,
// instead of type-asserting a shape it has to know about.
//
// The second answer is false when no caller is present, which is the state of
// a public procedure and of a hand-built handler in a test.
func CallerFrom(ctx context.Context) (*Caller, bool) {
	caller, ok := GetInfo(ctx).(*Caller)
	return caller, ok
}

// BearerToken returns the bearer token provided in the request's
// Authorization header, if any. The prefix match is case insensitive, as RFC
// 9110 Section 11.1 requires.
func BearerToken(request *http.Request) (string, bool) {
	const prefix = "Bearer "
	auth := request.Header.Get("Authorization")
	if len(auth) < len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}
	return auth[len(prefix):], true
}
