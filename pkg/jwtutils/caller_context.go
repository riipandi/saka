package jwtutils

import (
	"context"

	"connectrpc.com/authn"
)

// CallerFrom reads the authenticated caller a seam attached to the context.
//
// The caller travels through connectrpc's own context store, because that is
// the one the authenticator middleware and the guard interceptor already
// share. Reading it through this function rather than through authn directly
// keeps one type in play: a feature asks for the caller and gets the subject,
// the claims, and the delegation together, instead of type-asserting a shape
// it has to know about.
//
// The second answer is false when no caller is present, which is the state of
// a public procedure and of a hand-built handler in a test.
func CallerFrom(ctx context.Context) (*Caller, bool) {
	caller, ok := authn.GetInfo(ctx).(*Caller)
	return caller, ok
}
