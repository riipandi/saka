package middleware

import (
	"net/http"

	fwmiddleware "github.com/riipandi/saka/framework/middleware"

	"github.com/riipandi/saka/internal/audit"
)

// FingerprintHeader is the header the frontend sends its browser fingerprint
// in.
//
// It is a header rather than a field in every request message because the
// fingerprint is a property of the client, not of the call: a frontend sets
// it once in its fetch interceptor and every procedure carries it, without a
// contract change per request and without a client that forgets it being
// unable to call anything. A request without the header records no
// fingerprint, which is the state every non-browser caller is in.
//
// The value is stored as it arrived. It is opaque here: the frontend computes
// it, and a server that tried to interpret it would be asserting something it
// cannot know.
const FingerprintHeader = "X-Device-Fingerprint"

// ClientInfo captures where a request came from, so a seam below can write it
// into a record without knowing it is inside a request at all.
//
// It is mounted once, on the router that holds every route and every
// procedure, rather than per surface: the facts come from the HTTP request in
// both cases, and a second mount is a second place to forget. The context it
// fills is read through audit.ClientFromContext, which is what keeps a
// service from taking four parameters it only passes on.
//
// The address is resolved by the framework resolver and read back through
// its ClientIP, so the record and the rate limiter agree about who the
// client is. A header a caller can set is not believed: only headers
// the deployment declares its proxy overwrites are read at all.
func ClientInfo(trustedProxyHeaders []string) func(http.Handler) http.Handler {
	resolve := fwmiddleware.ClientIPResolver(trustedProxyHeaders)
	return func(next http.Handler) http.Handler {
		return resolve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := audit.ClientInfo{
				IPAddress:   fwmiddleware.ClientIP(r),
				UserAgent:   r.Header.Get("User-Agent"),
				Fingerprint: r.Header.Get(FingerprintHeader),
			}
			next.ServeHTTP(w, r.WithContext(audit.WithClientInfo(r.Context(), info)))
		}))
	}
}
