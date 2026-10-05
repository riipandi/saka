package audit

import "context"

// ClientInfo is where a request came from, as far as the server can tell.
//
// Every field is optional on its own, because the facts arrive by different
// routes and a client controls most of them: the address and the User-Agent
// come from the connection and the header, the fingerprint from a header the
// frontend sets, and the location is resolved from the address when a
// resolver is configured. A missing fact is recorded as missing, never
// guessed.
type ClientInfo struct {
	// IPAddress is the caller's address. A proxy deployment sees the proxy's
	// address, which is the honest answer until a trusted-proxy setting
	// decides to look further.
	IPAddress string
	// UserAgent is the browser's own identification, taken from the header
	// the browser sends. It is written as it arrived: parsing it into a
	// product and an OS is a reader's job, and a stored parse cannot be
	// redone when a parser improves.
	UserAgent string
	// Fingerprint is the browser fingerprint the frontend computes and sends
	// in a header of its own. It is opaque here — the server stores what it
	// is given and interprets nothing.
	Fingerprint string
	// Country and City are resolved from IPAddress by a resolver, when one
	// is configured. Nothing fills them today: the resolver is not wired, so
	// both stay empty and the columns stay NULL.
	Country string
	City    string
}

// contextKey is this package's own context key type, so no other package can
// collide with it.
type contextKey struct{}

// clientKey is the key the transport stores the request's client facts under.
var clientKey = contextKey{}

// WithClientInfo returns a context carrying where the request came from. The
// transport calls it once per request, before the procedure runs, and every
// seam below reads the same facts — which is why the recorder does not take
// them as parameters through four layers of service code.
func WithClientInfo(ctx context.Context, info ClientInfo) context.Context {
	return context.WithValue(ctx, clientKey, info)
}

// ClientFromContext answers the client facts the transport stored. A context
// without them — a job, a test, a command — answers the zero value, so a
// record written outside a request carries no client rather than failing.
func ClientFromContext(ctx context.Context) ClientInfo {
	info, _ := ctx.Value(clientKey).(ClientInfo)
	return info
}
